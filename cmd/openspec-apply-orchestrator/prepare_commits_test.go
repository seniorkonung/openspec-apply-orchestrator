package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

func TestPrepareCommitsСохраняетStoreИВыполняетPreflightДоСопровождения(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.CleanWorkingTree{})
	fixture.paseo.workspaceExists = true
	var output bytes.Buffer

	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change", "--store", "platform-specs"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitSuccess {
		t.Fatalf("ожидался успешный код, получен %d, вывод: %s", code, output.String())
	}
	wantPrefix := []string{
		"openspec:new",
		"openspec:resolve:selected-change:platform-specs",
		"git:open",
		"config:read",
		"local:check",
		"lock:acquire",
		"paseo:check",
		"openspec:resolve:selected-change:platform-specs",
		"paseo:workspace:read",
		"paseo:sessions:read",
		"git:read",
	}
	if got := fixture.events.snapshot(); len(got) < len(wantPrefix) || !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("неожиданный порядок preflight и сопровождения:\nполучено: %v\nожидался префикс: %v", got, wantPrefix)
	}
	if fixture.snapshotReads != 1 || fixture.inputsLoaded != 0 || fixture.paseo.createdWorkspaces != 0 || fixture.paseo.createdSessions != 0 {
		t.Fatalf("чистый Git вызвал чтение входов или мутацию: %v", fixture.events.snapshot())
	}
	if !strings.Contains(output.String(), "Поручение не требуется") {
		t.Fatalf("вывод не различает отсутствие работы: %s", output.String())
	}
}

func TestPrepareCommitsБезКаналаЗавершаетсяКодомДваДоМутаций(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.channelError = &config.FieldError{
		Path: "notifications.intervention",
		Kind: config.ErrMissingField,
	}
	var output bytes.Buffer

	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitUsageOrConfiguration {
		t.Fatalf("ожидался код конфигурации 2, получен %d", code)
	}
	if fixture.paseo.createdWorkspaces != 0 || fixture.paseo.createdSessions != 0 {
		t.Fatalf("отсутствующий канал привёл к мутации: %v", fixture.events.snapshot())
	}
	if !strings.Contains(output.String(), "notifications.intervention") {
		t.Fatalf("вывод не содержит путь отсутствующего канала: %s", output.String())
	}
	assertEventOrder(t, fixture.events.snapshot(), "git:read", "config:intervention:view")
	if countEvent(fixture.events.snapshot(), "config:session:view") != 0 {
		t.Fatalf("после ошибки канала прочитаны настройки новой сессии: %v", fixture.events.snapshot())
	}
}

func TestPrepareCommitsВосстанавливаетСессиюБезЧтенияТекущейКонфигурации(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-existing"
	fixture.paseo.sessionStatus = "idle"
	fixture.paseo.attentionReason = "finished"
	fixture.channelError = errors.New("снимок канала повреждён")
	ctx, cancel := context.WithCancel(context.Background())
	var output synchronizedBuffer
	result := make(chan int, 1)
	go func() {
		result <- runCommand(
			ctx,
			[]string{"prepare-commits", "--change", "selected-change"},
			fixture.workingRoot,
			&output,
			fixture.dependencies(),
		)
	}()
	output.waitForCount(t, "снимок канала повреждён", 1)
	cancel()
	code := <-result

	if code != exitObstacle {
		t.Fatalf("ожидался код препятствия 1, получен %d", code)
	}
	if fixture.snapshotReads != 1 || fixture.inputsLoaded != 0 || fixture.paseo.createdSessions != 0 || fixture.paseo.archivedSessions != 0 {
		t.Fatalf("восстановление прочитало входы создания или изменило сессию: %v", fixture.events.snapshot())
	}
	for _, fragment := range []string{"Восстановлена собственная сессия", "Требуется участие человека", "paseo://h/server-1/agent/session-existing", "перезапустите CLI"} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("вывод восстановления не содержит %q: %s", fragment, output.String())
		}
	}
}

func TestPrepareCommitsПродолжаетСопровождатьПереданнуюЧеловекуСессию(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-existing"
	fixture.paseo.sessionStatus = "idle"
	fixture.paseo.attentionReason = "finished"
	ctx, cancel := context.WithCancel(context.Background())
	var output synchronizedBuffer
	result := make(chan int, 1)

	go func() {
		result <- runCommand(
			ctx,
			[]string{"prepare-commits", "--change", "selected-change"},
			fixture.workingRoot,
			&output,
			fixture.dependencies(),
		)
	}()

	fixture.events.waitFor(t, "notify:deliver")
	select {
	case code := <-result:
		t.Fatalf("команда прекратила сопровождение переданной сессии с кодом %d: %s", code, output.String())
	default:
	}
	cancel()

	select {
	case code := <-result:
		if code != exitObstacle {
			t.Fatalf("ожидался код отменённого сопровождения 1, получен %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("команда не завершилась после отмены")
	}
	if fixture.paseo.createdSessions != 0 {
		t.Fatalf("сопровождение существующей сессии создало замену: %v", fixture.events.snapshot())
	}
}

func TestPrepareCommitsСообщаетФактическийЗапросРазрешенияБезВыводаОПричине(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-permission"
	fixture.paseo.sessionStatus = "idle"
	fixture.paseo.attentionReason = "permission"
	ctx, cancel := context.WithCancel(context.Background())
	var output synchronizedBuffer
	result := make(chan int, 1)
	go func() {
		result <- runCommand(
			ctx,
			[]string{"prepare-commits", "--change", "selected-change"},
			fixture.workingRoot,
			&output,
			fixture.dependencies(),
		)
	}()
	output.waitForCount(t, "Paseo запросил разрешение", 1)
	cancel()
	code := <-result

	if code != exitObstacle {
		t.Fatalf("ожидался код препятствия 1, получен %d", code)
	}
	if !strings.Contains(output.String(), "Paseo запросил разрешение") {
		t.Fatalf("вывод не сообщает фактический запрос разрешения: %s", output.String())
	}
	report := ""
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.Contains(line, "Требуется участие человека") {
			report = line
			break
		}
	}
	if report == "" {
		t.Fatalf("отчёт о потребности в человеке отсутствует: %s", output.String())
	}
	for _, forbidden := range []string{"вопреки", "наруш", "совместим", "provider", "plugin"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("отчёт приписал причину запроса разрешения через %q: %s", forbidden, report)
		}
	}
}

func TestPrepareCommitsАрхивируетЗавершённуюСессиюПриЧистомGit(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.CleanWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-completed"
	fixture.paseo.sessionStatus = "idle"
	fixture.paseo.attentionReason = "finished"
	var output bytes.Buffer

	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitSuccess {
		t.Fatalf("ожидался успешный код, получен %d, вывод: %s", code, output.String())
	}
	if fixture.inputsLoaded != 0 || fixture.paseo.archivedSessions != 1 {
		t.Fatalf("завершённая сессия обработана неверно: %v", fixture.events.snapshot())
	}
	for _, fragment := range []string{"Восстановлена собственная сессия", "Архивирую собственную сессию", "Подготовка коммитов завершена"} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("вывод завершения не содержит %q: %s", fragment, output.String())
		}
	}
}

func TestPrepareCommitsСоздаётСессиюТолькоПослеПроверенныхВходов(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.waitError = context.Canceled
	var output bytes.Buffer

	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitObstacle {
		t.Fatalf("ожидалась остановка сопровождения с кодом 1, получен %d", code)
	}
	if fixture.inputsLoaded != 1 || fixture.paseo.createdWorkspaces != 1 || fixture.paseo.createdSessions != 1 {
		t.Fatalf("ожидались одни входы, workspace и сессия: inputs=%d workspace=%d session=%d",
			fixture.inputsLoaded, fixture.paseo.createdWorkspaces, fixture.paseo.createdSessions)
	}
	events := fixture.events.snapshot()
	assertEventOrder(t, events, "config:read", "catalog:read")
	assertEventOrder(t, events, "catalog:read", "paseo:workspace:create")
	assertEventOrder(t, events, "paseo:workspace:create", "paseo:session:create")
	if !strings.Contains(output.String(), "Создана собственная сессия session-created") ||
		strings.Contains(output.String(), "Восстановлена собственная сессия session-created") ||
		strings.Contains(output.String(), prompts.CommitPreparation().Text()) {
		t.Fatalf("вывод не различает создание либо раскрыл промпт: %s", output.String())
	}
}

func TestPrepareCommitsРазрешаетСтандартныйКаналДоПервойМутации(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	writeCommandConfiguration(t, fixture.workingRoot, "")
	fixture.paseo.waitError = context.Canceled
	var output bytes.Buffer

	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitObstacle {
		t.Fatalf("ожидалась остановка сопровождения с кодом 1, получен %d", code)
	}
	if fixture.snapshotReads != 1 || fixture.deliveryBuilds != 1 || fixture.deliverer == nil {
		t.Fatalf("ожидались один снимок и один адаптер: reads=%d builds=%d", fixture.snapshotReads, fixture.deliveryBuilds)
	}
	if got := fixture.deliverer.channel.Priority(); got != config.NtfyPriorityDefault {
		t.Fatalf("ожидался стандартный приоритет %q, получен %q", config.NtfyPriorityDefault, got)
	}
	events := fixture.events.snapshot()
	assertEventOrder(t, events, "git:read", "config:intervention:view")
	assertEventOrder(t, events, "config:intervention:view", "notify:build")
	assertEventOrder(t, events, "notify:build", "paseo:workspace:create")
}

func TestPrepareCommitsПовторяетДоставкуТемЖеАдаптеромИСнимком(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-existing"
	fixture.paseo.sessionStatus = "idle"
	fixture.paseo.attentionReason = "finished"
	fixture.deliveryFailures = 2
	fixture.onDelivery = func(attempt int, _ notify.Intervention) {
		if attempt == 1 {
			writeCommandConfiguration(t, fixture.workingRoot, "min")
		}
		if attempt == 2 {
			fixture.paseo.sessionStatus = "closed"
			fixture.paseo.attentionReason = ""
			fixture.repository.state = orchestrator.CleanWorkingTree{}
		}
	}
	fixture.interventionClock.tick()
	fixture.interventionClock.tick()
	var output bytes.Buffer

	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitSuccess {
		t.Fatalf("ожидался успех после ручного закрытия с чистым Git, получен %d: %s", code, output.String())
	}
	if fixture.snapshotReads != 1 || fixture.deliveryBuilds != 1 || fixture.deliverer == nil {
		t.Fatalf("повтор создал новый снимок или адаптер: reads=%d builds=%d", fixture.snapshotReads, fixture.deliveryBuilds)
	}
	records := fixture.deliverer.snapshot()
	if len(records) != 2 || records[0] != records[1] {
		t.Fatalf("повторы изменили пользовательское представление: %v", records)
	}
	if records[0].priority != config.NtfyPriorityHigh {
		t.Fatalf("изменение файла подменило приоритет текущего запуска: %q", records[0].priority)
	}
	if fixture.paseo.createdSessions != 0 {
		t.Fatalf("ошибка доставки создала нового агента: %v", fixture.events.snapshot())
	}
}

func TestPrepareCommitsРучноеЗакрытиеОпределяетКодПоСвежемуGit(t *testing.T) {
	for _, tt := range []struct {
		name       string
		finalGit   orchestrator.WorkingTreeObservation
		wantCode   int
		newAttempt bool
	}{
		{name: "чистый Git завершает поручение", finalGit: orchestrator.CleanWorkingTree{}, wantCode: exitSuccess},
		{name: "грязный Git завершает попытку и разрешает следующую", finalGit: orchestrator.DirtyWorkingTree{}, wantCode: exitObstacle, newAttempt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
			fixture.paseo.workspaceExists = true
			fixture.paseo.sessionID = "session-existing"
			fixture.paseo.sessionStatus = "idle"
			fixture.paseo.attentionReason = "finished"
			fixture.onDelivery = func(_ int, _ notify.Intervention) {
				fixture.paseo.sessionStatus = "closed"
				fixture.paseo.attentionReason = ""
				fixture.repository.state = tt.finalGit
			}
			fixture.interventionClock.tick()
			var output bytes.Buffer

			code := runCommand(
				context.Background(),
				[]string{"prepare-commits", "--change", "selected-change"},
				fixture.workingRoot,
				&output,
				fixture.dependencies(),
			)

			if code != tt.wantCode {
				t.Fatalf("ожидался код %d, получен %d: %s", tt.wantCode, code, output.String())
			}
			if !tt.newAttempt {
				return
			}

			fixture.onDelivery = nil
			fixture.paseo.waitError = context.Canceled
			output.Reset()
			code = runCommand(
				context.Background(),
				[]string{"prepare-commits", "--change", "selected-change"},
				fixture.workingRoot,
				&output,
				fixture.dependencies(),
			)
			if code != exitObstacle || fixture.paseo.createdSessions != 1 {
				t.Fatalf("следующий запуск не создал ровно одну новую попытку: code=%d sessions=%d events=%v", code, fixture.paseo.createdSessions, fixture.events.snapshot())
			}
		})
	}
}

func TestPrepareCommitsОшибкаКаталогаКлассифицируетсяБезFallbackИМутаций(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{name: "неизвестная модель является ошибкой настройки", err: paseo.ErrModelNotFound, code: exitUsageOrConfiguration},
		{name: "повреждённый каталог является ошибкой источника", err: paseo.ErrUnexpectedJSON, code: exitObstacle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
			fixture.loadInputsError = tt.err
			var output bytes.Buffer

			code := runCommand(
				context.Background(),
				[]string{"prepare-commits", "--change", "selected-change"},
				fixture.workingRoot,
				&output,
				fixture.dependencies(),
			)

			if code != tt.code {
				t.Fatalf("ожидался код %d, получен %d", tt.code, code)
			}
			if fixture.paseo.createdWorkspaces != 0 || fixture.paseo.createdSessions != 0 {
				t.Fatalf("ошибка входов вызвала мутацию: %v", fixture.events.snapshot())
			}
			if fixture.paseo.fallbackAttempts != 0 {
				t.Fatalf("после отказа полного режима выполнен fallback: %d", fixture.paseo.fallbackAttempts)
			}
		})
	}
}

func TestPrepareCommitsПоказываетЛокальныйПрогрессWaitБезОпросаPaseo(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-working"
	fixture.paseo.sessionStatus = "running"
	fixture.paseo.waitUntilCanceled = true
	clock := newManualWaitClock()
	fixture.clock = clock
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output synchronizedBuffer
	result := make(chan int, 1)

	go func() {
		result <- runCommand(
			ctx,
			[]string{"prepare-commits", "--change", "selected-change"},
			fixture.workingRoot,
			&output,
			fixture.dependencies(),
		)
	}()
	fixture.events.waitFor(t, "paseo:wait")
	clock.tick()
	output.waitForCount(t, "Ожидание продолжается", 1)
	clock.tick()
	output.waitForCount(t, "Ожидание продолжается", 2)
	cancel()

	select {
	case code := <-result:
		if code != exitObstacle {
			t.Fatalf("ожидался код отменённого сопровождения 1, получен %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("команда не завершилась после отмены")
	}

	if countEvent(fixture.events.snapshot(), "paseo:wait") != 1 ||
		countEvent(fixture.events.snapshot(), "paseo:workspace:read") != 1 ||
		countEvent(fixture.events.snapshot(), "paseo:sessions:read") != 1 {
		t.Fatalf("локальный таймер вызвал опрос Paseo: %v", fixture.events.snapshot())
	}
	if strings.Count(output.String(), "Ожидание продолжается") != 2 {
		t.Fatalf("ожидались два локальных сообщения прогресса: %s", output.String())
	}
}

func TestНеверныеАргументыИОшибкиИсточникаВозвращаютРазныеКоды(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.CleanWorkingTree{})
	var output bytes.Buffer
	if code := runCommand(context.Background(), []string{"prepare-commits"}, fixture.workingRoot, &output, fixture.dependencies()); code != exitUsageOrConfiguration {
		t.Fatalf("неверные аргументы должны вернуть 2, получено %d", code)
	}

	fixture = newCommandFixture(t, orchestrator.CleanWorkingTree{})
	fixture.resolveError = errors.New("OpenSpec недоступен")
	output.Reset()
	if code := runCommand(context.Background(), []string{"prepare-commits", "--change", "selected-change"}, fixture.workingRoot, &output, fixture.dependencies()); code != exitObstacle {
		t.Fatalf("ошибка источника должна вернуть 1, получено %d", code)
	}
}

func TestУправляющиеСимволыАргументаОтклоняютсяДоВывода(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.CleanWorkingTree{})
	var output bytes.Buffer
	code := runCommand(
		context.Background(),
		[]string{"prepare-commits", "--change", "selected-change\nПОДМЕНЁННАЯ СТРОКА"},
		fixture.workingRoot,
		&output,
		fixture.dependencies(),
	)

	if code != exitUsageOrConfiguration {
		t.Fatalf("ожидался код неверных аргументов 2, получен %d", code)
	}
	if strings.Contains(output.String(), "ПОДМЕНЁННАЯ") {
		t.Fatalf("непроверенный аргумент попал в stdout: %q", output.String())
	}
	if len(fixture.events.snapshot()) != 0 {
		t.Fatalf("непроверенный аргумент достиг адаптеров: %v", fixture.events.snapshot())
	}
}

func TestСигналыВозвращаютСтандартныйКодИОтменяютКоманду(t *testing.T) {
	for _, tt := range []struct {
		name string
		sig  os.Signal
		code int
	}{
		{name: "SIGINT", sig: os.Interrupt, code: 130},
		{name: "SIGTERM", sig: syscall.SIGTERM, code: 143},
	} {
		t.Run(tt.name, func(t *testing.T) {
			signals := make(chan os.Signal, 1)
			started := make(chan struct{})
			canceled := make(chan struct{})
			result := make(chan int, 1)
			go func() {
				result <- runWithSignals(signals, func(ctx context.Context) int {
					close(started)
					<-ctx.Done()
					close(canceled)
					return exitObstacle
				})
			}()
			<-started
			signals <- tt.sig

			if code := <-result; code != tt.code {
				t.Fatalf("ожидался код %d, получен %d", tt.code, code)
			}
			select {
			case <-canceled:
			default:
				t.Fatal("сигнал не отменил контекст команды")
			}
		})
	}
}

type commandFixture struct {
	t                 *testing.T
	workingRoot       string
	planningHome      string
	changeRoot        string
	events            *eventRecorder
	repository        *fakeRepository
	paseo             *fakePaseoRuntime
	clock             waitClock
	interventionClock *manualInterventionClock
	resolveError      error
	loadInputsError   error
	channelError      error
	snapshotReads     int
	inputsLoaded      int
	deliveryBuilds    int
	deliverer         *fakeInterventionDeliverer
	deliveryFailures  int
	onDelivery        func(int, notify.Intervention)
}

func newCommandFixture(t *testing.T, git orchestrator.WorkingTreeObservation) *commandFixture {
	t.Helper()
	workingRoot := t.TempDir()
	planningHome := filepath.Join(t.TempDir(), "planning")
	changeRoot := filepath.Join(planningHome, "openspec", "changes", "selected-change")
	if err := os.MkdirAll(changeRoot, 0o700); err != nil {
		t.Fatalf("создать корень change: %v", err)
	}
	events := newEventRecorder()
	writeCommandConfiguration(t, workingRoot, "high")
	return &commandFixture{
		t:                 t,
		workingRoot:       workingRoot,
		planningHome:      planningHome,
		changeRoot:        changeRoot,
		events:            events,
		repository:        &fakeRepository{root: workingRoot, state: git, events: events},
		paseo:             &fakePaseoRuntime{serverID: "server-1", events: events},
		clock:             newManualWaitClock(),
		interventionClock: newManualInterventionClock(),
	}
}

func (fixture *commandFixture) dependencies() commandDependencies {
	return commandDependencies{
		newChangeSource: func(workingDirectory string) (changeSource, error) {
			fixture.events.add("openspec:new")
			return &fakeChangeSource{fixture: fixture}, nil
		},
		openRepository: func(_ context.Context, workingDirectory string) (workingTreeRepository, error) {
			fixture.events.add("git:open")
			if workingDirectory != fixture.workingRoot {
				return nil, errors.New("неожиданный рабочий каталог")
			}
			return fixture.repository, nil
		},
		acquireChangeLock: func(workingRoot, changeRoot string) (io.Closer, error) {
			fixture.events.add("local:check")
			if workingRoot != fixture.workingRoot || changeRoot != fixture.changeRoot {
				return nil, errors.New("неожиданные корни локальной среды")
			}
			fixture.events.add("lock:acquire")
			return recordingCloser{events: fixture.events}, nil
		},
		openPaseo: func(context.Context) (paseoRuntime, error) {
			fixture.events.add("paseo:check")
			return fixture.paseo, nil
		},
		readConfiguration: func(root string) (configurationSnapshot, error) {
			fixture.snapshotReads++
			fixture.events.add("config:read")
			if root != fixture.workingRoot {
				return nil, errors.New("конфигурация прочитана не из корня рабочего дерева")
			}
			configurationRoot, err := config.NewRepositoryRoot(root)
			if err != nil {
				return nil, err
			}
			return &recordingConfigurationSnapshot{
				fixture:  fixture,
				snapshot: config.ReadSnapshot(configurationRoot),
			}, nil
		},
		loadNewSessionInputs: func(ctx context.Context, snapshot configurationSnapshot, runtime paseoRuntime) (newSessionInputs, error) {
			fixture.inputsLoaded++
			fixture.events.add("config:session:view")
			if fixture.loadInputsError != nil {
				return newSessionInputs{}, fixture.loadInputsError
			}
			settings, err := snapshot.CommitPreparation()
			if err != nil {
				return newSessionInputs{}, err
			}
			verified, err := runtime.VerifySessionSettings(ctx, settings)
			if err != nil {
				return newSessionInputs{}, err
			}
			return newSessionInputs{settings: verified, prompt: prompts.CommitPreparation()}, nil
		},
		newInterventionDelivery: func(channel config.InterventionChannel) (notify.Deliverer, error) {
			fixture.deliveryBuilds++
			fixture.events.add("notify:build")
			fixture.deliverer = &fakeInterventionDeliverer{
				channel:    channel,
				events:     fixture.events,
				failures:   fixture.deliveryFailures,
				onDelivery: fixture.onDelivery,
			}
			return fixture.deliverer, nil
		},
		waitClock:         fixture.clock,
		interventionClock: fixture.interventionClock,
	}
}

func writeCommandConfiguration(t *testing.T, root, priority string) {
	t.Helper()
	priorityField := ""
	if priority != "" {
		priorityField = `, "priority": "` + priority + `"`
	}
	document := `{
		"version": 1,
		"sessions": {"commit-preparation": {"provider": "codex", "model": "gpt-5", "reasoning": "high"}},
		"notifications": {"intervention": {"type": "ntfy", "url": "https://notify.example/topic"` + priorityField + `}}
	}`
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(document), 0o600); err != nil {
		t.Fatalf("записать конфигурацию: %v", err)
	}
}

type recordingConfigurationSnapshot struct {
	fixture  *commandFixture
	snapshot config.Snapshot
}

func (snapshot *recordingConfigurationSnapshot) CommitPreparation() (config.UntrustedAgentSettings, error) {
	return snapshot.snapshot.CommitPreparation()
}

func (snapshot *recordingConfigurationSnapshot) InterventionChannel() (config.InterventionChannel, error) {
	snapshot.fixture.events.add("config:intervention:view")
	if snapshot.fixture.channelError != nil {
		return config.InterventionChannel{}, snapshot.fixture.channelError
	}
	return snapshot.snapshot.InterventionChannel()
}

type fakeInterventionDeliverer struct {
	channel    config.InterventionChannel
	events     *eventRecorder
	deliveries int
	failures   int
	onDelivery func(int, notify.Intervention)
	records    []deliveredIntervention
}

func (delivery *fakeInterventionDeliverer) Deliver(_ context.Context, event notify.Intervention) *notify.DeliveryError {
	delivery.deliveries++
	delivery.records = append(delivery.records, deliveredIntervention{
		change:    event.Change(),
		message:   event.Message(),
		sessionID: event.SessionID().String(),
		link:      event.SessionLink().String(),
		priority:  delivery.channel.Priority(),
	})
	delivery.events.add("notify:deliver")
	if delivery.onDelivery != nil {
		delivery.onDelivery(delivery.deliveries, event)
	}
	if delivery.deliveries <= delivery.failures {
		return notify.NewDeliveryError()
	}
	return nil
}

func (delivery *fakeInterventionDeliverer) snapshot() []deliveredIntervention {
	return append([]deliveredIntervention(nil), delivery.records...)
}

type deliveredIntervention struct {
	change    string
	message   string
	sessionID string
	link      string
	priority  config.NtfyPriority
}

type fakeChangeSource struct {
	fixture *commandFixture
}

func (source *fakeChangeSource) Resolve(_ context.Context, selection changeSelection) (resolvedChange, error) {
	source.fixture.events.add("openspec:resolve:" + selection.changeName + ":" + selection.storeID)
	if source.fixture.resolveError != nil {
		return resolvedChange{}, source.fixture.resolveError
	}
	return resolvedChange{
		name:             selection.changeName,
		planningHomeRoot: source.fixture.planningHome,
		changeRoot:       source.fixture.changeRoot,
	}, nil
}

type fakeRepository struct {
	root   string
	state  orchestrator.WorkingTreeObservation
	events *eventRecorder
}

func (repository *fakeRepository) Root() string {
	return repository.root
}

func (repository *fakeRepository) Read(context.Context) (orchestrator.WorkingTreeObservation, error) {
	repository.events.add("git:read")
	return repository.state, nil
}

type fakePaseoRuntime struct {
	serverID          string
	events            *eventRecorder
	workspaceExists   bool
	sessionID         string
	sessionStatus     string
	attentionReason   string
	waitError         error
	waitUntilCanceled bool
	createdWorkspaces int
	createdSessions   int
	archivedSessions  int
	fallbackAttempts  int
}

func (runtime *fakePaseoRuntime) ServerID() string {
	return runtime.serverID
}

func (runtime *fakePaseoRuntime) SessionLink(session orchestrator.SessionID) string {
	return "paseo://h/" + runtime.serverID + "/agent/" + session.String()
}

func (runtime *fakePaseoRuntime) FindActiveWorkspace(_ context.Context, _ orchestrator.ChangeKey, _ string) (orchestrator.ManagedWorkspaceObservation, error) {
	runtime.events.add("paseo:workspace:read")
	if runtime.workspaceExists {
		return orchestrator.OneManagedWorkspace{ID: mustCommandWorkspaceID(runtime)}, nil
	}
	return orchestrator.NoManagedWorkspace{}, nil
}

func (runtime *fakePaseoRuntime) FindOwnSessions(_ context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, _ string) (orchestrator.OwnSessionObservation, error) {
	runtime.events.add("paseo:sessions:read")
	if runtime.sessionStatus == "closed" {
		return orchestrator.NoActiveOwnSession{}, nil
	}
	return runtime.sessionObservation(change, workspace)
}

func (runtime *fakePaseoRuntime) ObserveOwnSession(_ context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, _ string, _ orchestrator.SessionID) (orchestrator.OwnSessionObservation, error) {
	runtime.events.add("paseo:session:observe")
	return runtime.sessionObservation(change, workspace)
}

func (runtime *fakePaseoRuntime) CreateWorkspace(context.Context, orchestrator.ChangeKey, string) error {
	runtime.events.add("paseo:workspace:create")
	runtime.createdWorkspaces++
	runtime.workspaceExists = true
	return nil
}

func (runtime *fakePaseoRuntime) CreateOwnSession(_ context.Context, _ orchestrator.ChangeKey, _ orchestrator.WorkspaceID, _ string, _ paseo.VerifiedSessionSettings, _ prompts.CommitPreparationPrompt) (orchestrator.SessionID, error) {
	runtime.events.add("paseo:session:create")
	runtime.createdSessions++
	runtime.sessionID = "session-created"
	runtime.sessionStatus = "running"
	runtime.attentionReason = ""
	id, _ := orchestrator.NewSessionID(runtime.sessionID)
	return id, nil
}

func (runtime *fakePaseoRuntime) WaitOwnSession(ctx context.Context, _ orchestrator.SessionID) error {
	runtime.events.add("paseo:wait")
	if runtime.waitUntilCanceled {
		<-ctx.Done()
		return ctx.Err()
	}
	return runtime.waitError
}

func (runtime *fakePaseoRuntime) ArchiveOwnSession(context.Context, orchestrator.ChangeKey, orchestrator.WorkspaceID, string, orchestrator.ManagedSession) error {
	runtime.events.add("paseo:session:archive")
	runtime.archivedSessions++
	return nil
}

func (runtime *fakePaseoRuntime) VerifySessionSettings(context.Context, paseo.UntrustedSessionSettings) (paseo.VerifiedSessionSettings, error) {
	runtime.events.add("catalog:read")
	return paseo.VerifiedSessionSettings{}, nil
}

func (runtime *fakePaseoRuntime) sessionObservation(change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID) (orchestrator.OwnSessionObservation, error) {
	if runtime.sessionID == "" {
		return orchestrator.NoActiveOwnSession{}, nil
	}
	return orchestrator.ObserveOwnSessions(change, workspace, []orchestrator.UntrustedOwnSession{{
		ID:                runtime.sessionID,
		WorkspaceID:       workspace.String(),
		Status:            runtime.sessionStatus,
		RequiresAttention: runtime.attentionReason != "",
		AttentionReason:   runtime.attentionReason,
		Labels: map[string]string{
			orchestrator.LabelOwner:     orchestrator.ManagedOwner,
			orchestrator.LabelVersion:   orchestrator.CurrentOwnershipVersion,
			orchestrator.LabelChange:    change.String(),
			orchestrator.LabelKind:      orchestrator.CommitPreparationKind,
			orchestrator.LabelWorkspace: workspace.String(),
		},
	}})
}

func mustCommandWorkspaceID(runtime *fakePaseoRuntime) orchestrator.WorkspaceID {
	id, err := orchestrator.NewWorkspaceID("workspace-1")
	if err != nil {
		panic(err)
	}
	return id
}

type recordingCloser struct {
	events *eventRecorder
}

func (closer recordingCloser) Close() error {
	closer.events.add("lock:close")
	return nil
}

type manualWaitClock struct {
	ticks chan time.Time
}

func newManualWaitClock() *manualWaitClock {
	return &manualWaitClock{ticks: make(chan time.Time, 8)}
}

func (clock *manualWaitClock) NewTicker(time.Duration) waitTicker {
	return manualWaitTicker{ticks: clock.ticks}
}

func (clock *manualWaitClock) tick() {
	clock.ticks <- time.Now()
}

type manualWaitTicker struct {
	ticks <-chan time.Time
}

func (ticker manualWaitTicker) C() <-chan time.Time {
	return ticker.ticks
}

func (manualWaitTicker) Stop() {}

type manualInterventionClock struct {
	ticks chan struct{}
}

func newManualInterventionClock() *manualInterventionClock {
	return &manualInterventionClock{ticks: make(chan struct{}, 8)}
}

func (clock *manualInterventionClock) Pause(ctx context.Context, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-clock.ticks:
		return nil
	}
}

func (clock *manualInterventionClock) tick() {
	clock.ticks <- struct{}{}
}

type eventRecorder struct {
	mu      sync.Mutex
	events  []string
	changed chan struct{}
}

func newEventRecorder() *eventRecorder {
	return &eventRecorder{changed: make(chan struct{}, 1)}
}

func (recorder *eventRecorder) add(event string) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
	select {
	case recorder.changed <- struct{}{}:
	default:
	}
}

func (recorder *eventRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

func (recorder *eventRecorder) waitFor(t *testing.T, expected string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if countEvent(recorder.snapshot(), expected) > 0 {
			return
		}
		select {
		case <-recorder.changed:
		case <-deadline.C:
			t.Fatalf("событие %q не получено: %v", expected, recorder.snapshot())
		}
	}
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(data)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.String()
}

func (buffer *synchronizedBuffer) waitForCount(t *testing.T, fragment string, expected int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Count(buffer.String(), fragment) >= expected {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("фрагмент %q не появился %d раз: %s", fragment, expected, buffer.String())
		}
	}
}

func assertEventOrder(t *testing.T, events []string, before, after string) {
	t.Helper()
	beforeIndex := eventIndex(events, before)
	afterIndex := eventIndex(events, after)
	if beforeIndex < 0 || afterIndex <= beforeIndex {
		t.Fatalf("событие %q не предшествует %q: %v", before, after, events)
	}
}

func eventIndex(events []string, expected string) int {
	for index, event := range events {
		if event == expected {
			return index
		}
	}
	return -1
}

func countEvent(events []string, expected string) int {
	count := 0
	for _, event := range events {
		if event == expected {
			count++
		}
	}
	return count
}
