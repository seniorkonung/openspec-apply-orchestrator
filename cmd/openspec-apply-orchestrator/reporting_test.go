package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

func TestPrepareCommitsПодробныйРежимДобавляетТехническиеРезультаты(t *testing.T) {
	tests := []struct {
		name          string
		arguments     []string
		wantTechnical bool
	}{
		{
			name:      "обычный режим скрывает технические чтения",
			arguments: []string{"prepare-commits", "--change", "selected-change"},
		},
		{
			name:          "verbose сохраняет обычный ход и добавляет технические чтения",
			arguments:     []string{"prepare-commits", "--change", "selected-change", "--verbose"},
			wantTechnical: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCommandFixture(t, orchestrator.CleanWorkingTree{})
			fixture.paseo.workspaceExists = true
			var output bytes.Buffer

			code := runCommand(
				context.Background(),
				tt.arguments,
				fixture.workingRoot,
				&output,
				fixture.dependencies(),
			)

			if code != exitSuccess {
				t.Fatalf("ожидался успешный код, получен %d: %s", code, output.String())
			}
			for _, fragment := range []string{
				"Проверяю OpenSpec change selected-change.",
				"Сопровождение подготовки коммитов запущено.",
				"Поручение не требуется",
			} {
				if !strings.Contains(output.String(), fragment) {
					t.Fatalf("обычный ход не содержит %q: %s", fragment, output.String())
				}
			}
			technical := []string{
				"Подробно: OpenSpec change прочитан.",
				"Подробно: рабочий Git открыт.",
				"Подробно: снимок конфигурации получен.",
				"Подробно: локальное владение change установлено.",
				"Подробно: совместимость Paseo подтверждена.",
				"Подробно: состояние workspace прочитано.",
				"Подробно: список собственных сессий прочитан.",
				"Подробно: состояние Git прочитано.",
			}
			for _, fragment := range technical {
				if got := strings.Contains(output.String(), fragment); got != tt.wantTechnical {
					t.Fatalf("наличие технического сообщения %q = %v, ожидалось %v:\n%s", fragment, got, tt.wantTechnical, output.String())
				}
			}
		})
	}
}

func TestPrepareCommitsРепортёрСохраняетХронологиюПовторнойДоставкиИЗакрытия(t *testing.T) {
	fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
	fixture.paseo.workspaceExists = true
	fixture.paseo.sessionID = "session-existing"
	fixture.paseo.sessionStatus = "idle"
	fixture.paseo.attentionReason = "finished"
	fixture.deliveryFailures = 2
	fixture.onDelivery = func(attempt int, _ notify.Intervention) {
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
		t.Fatalf("ожидался успех после закрытия с чистым Git, получен %d: %s", code, output.String())
	}
	assertTextOrder(t, output.String(),
		"Восстановлена собственная сессия session-existing.",
		"Требуется участие человека",
		"Уведомление не доставлено",
		"Повторяю доставку уведомления",
		"Подготовка коммитов завершена",
	)
}

func TestРепортёрНеНазываетНовуюПотребностьПовторомПослеВозобновившегосяХода(t *testing.T) {
	known, err := notify.NewKnownSession("session-existing", "paseo://h/server-1/agent/session-existing")
	if err != nil {
		t.Fatalf("создать известную сессию: %v", err)
	}
	event, err := notify.NewIntervention("selected-change", notify.ReasonTurnFinished, known)
	if err != nil {
		t.Fatalf("создать событие потребности в человеке: %v", err)
	}
	change, err := orchestrator.NewChangeKey("change-key")
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	workspace, err := orchestrator.NewWorkspaceID("workspace-1")
	if err != nil {
		t.Fatalf("создать ID workspace: %v", err)
	}
	observation, err := orchestrator.ObserveOwnSessions(change, workspace, []orchestrator.UntrustedOwnSession{{
		ID:          "session-existing",
		WorkspaceID: workspace.String(),
		Status:      "running",
		Labels: map[string]string{
			orchestrator.LabelOwner:     orchestrator.ManagedOwner,
			orchestrator.LabelVersion:   orchestrator.CurrentOwnershipVersion,
			orchestrator.LabelChange:    change.String(),
			orchestrator.LabelKind:      orchestrator.CommitPreparationKind,
			orchestrator.LabelWorkspace: workspace.String(),
		},
	}})
	if err != nil {
		t.Fatalf("создать наблюдение работающей сессии: %v", err)
	}
	var output bytes.Buffer
	delivery := &snapshotInterventionDelivery{
		reporter: newCommandReporter(&output, false),
		resolved: true,
		delivery: &fakeInterventionDeliverer{failures: 2, events: newEventRecorder()},
	}

	if result := delivery.Deliver(context.Background(), event); result == nil {
		t.Fatal("первая доставка должна завершиться неподтверждённо")
	}
	delivery.observeSessionProgress(observation)
	if result := delivery.Deliver(context.Background(), event); result == nil {
		t.Fatal("доставка новой потребности должна завершиться неподтверждённо")
	}

	if strings.Count(output.String(), "Требуется участие человека") != 2 {
		t.Fatalf("новая потребность после хода не показана отдельно: %s", output.String())
	}
	if strings.Contains(output.String(), "Повторяю доставку уведомления") {
		t.Fatalf("новый эпизод ошибочно назван повтором: %s", output.String())
	}
}

func TestPrepareCommitsРепортёрНеРаскрываетВнешниеДанные(t *testing.T) {
	const (
		privateURL = "https://notify.example/private-topic"
		token      = "secret-token-value"
		prompt     = "секретный первоначальный промпт"
		diff       = "diff --git a/private b/private"
		history    = "приватная история разговора"
		message    = "непрозрачный message результата wait"
	)
	unsafeError := errors.New(strings.Join(
		[]string{privateURL, token, prompt, diff, history, message, "\x1b[31mПОДМЕНЁННАЯ СТРОКА\r\n"},
		" ",
	))

	t.Run("ошибка снимка канала оставляет только безопасную ссылку сессии", func(t *testing.T) {
		fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
		fixture.paseo.workspaceExists = true
		fixture.paseo.sessionID = "session-existing"
		fixture.paseo.sessionStatus = "idle"
		fixture.paseo.attentionReason = "finished"
		fixture.channelError = unsafeError
		ctx, cancel := context.WithCancel(context.Background())
		var output synchronizedBuffer
		result := make(chan int, 1)
		go func() {
			result <- runCommand(
				ctx,
				[]string{"prepare-commits", "--change", "selected-change", "--verbose"},
				fixture.workingRoot,
				&output,
				fixture.dependencies(),
			)
		}()
		output.waitForCount(t, "Уведомление не доставлено", 1)
		cancel()
		if code := <-result; code != exitObstacle {
			t.Fatalf("ожидался код препятствия 1, получен %d", code)
		}
		assertSafeReporterOutput(t, output.String(), privateURL, token, prompt, diff, history, message, "ПОДМЕНЁННАЯ", "\x1b", "\r")
		if !strings.Contains(output.String(), "paseo://h/server-1/agent/session-existing") {
			t.Fatalf("локальная ссылка известной сессии потеряна: %s", output.String())
		}
	})

	t.Run("ошибка wait не выводит непрозрачное внешнее сообщение", func(t *testing.T) {
		fixture := newCommandFixture(t, orchestrator.DirtyWorkingTree{})
		fixture.paseo.workspaceExists = true
		fixture.paseo.sessionID = "session-working"
		fixture.paseo.sessionStatus = "running"
		fixture.paseo.waitError = unsafeError
		var output bytes.Buffer

		code := runCommand(
			context.Background(),
			[]string{"prepare-commits", "--change", "selected-change", "--verbose"},
			fixture.workingRoot,
			&output,
			fixture.dependencies(),
		)

		if code != exitObstacle {
			t.Fatalf("ожидался код препятствия 1, получен %d", code)
		}
		assertSafeReporterOutput(t, output.String(), privateURL, token, prompt, diff, history, message, "ПОДМЕНЁННАЯ", "\x1b", "\r")
	})
}

func TestPrepareCommitsНеПредоставляетМашиночитаемыйРежим(t *testing.T) {
	for _, arguments := range [][]string{
		{"prepare-commits", "--change", "selected-change", "--json"},
		{"prepare-commits", "--change", "selected-change", "--log-format", "json"},
	} {
		fixture := newCommandFixture(t, orchestrator.CleanWorkingTree{})
		var output bytes.Buffer

		code := runCommand(context.Background(), arguments, fixture.workingRoot, &output, fixture.dependencies())

		if code != exitUsageOrConfiguration {
			t.Fatalf("аргументы %v должны вернуть код 2, получен %d", arguments, code)
		}
		if len(fixture.events.snapshot()) != 0 {
			t.Fatalf("аргументы %v достигли адаптеров: %v", arguments, fixture.events.snapshot())
		}
	}
}

func assertTextOrder(t *testing.T, output string, fragments ...string) {
	t.Helper()
	position := 0
	for _, fragment := range fragments {
		index := strings.Index(output[position:], fragment)
		if index < 0 {
			t.Fatalf("фрагмент %q отсутствует после позиции %d:\n%s", fragment, position, output)
		}
		position += index + len(fragment)
	}
}

func assertSafeReporterOutput(t *testing.T, output string, forbidden ...string) {
	t.Helper()
	for _, fragment := range forbidden {
		if strings.Contains(output, fragment) {
			t.Fatalf("репортёр раскрыл %q:\n%s", fragment, output)
		}
	}
}
