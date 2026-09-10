//go:build paseo_integration

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

func TestProductionКомандаДоставляетКаждуюПричинуВТочнуюСессию(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		behavior      testpaseo.Behavior
		message       string
		priority      config.NtfyPriority
		configuration string
	}{
		{
			name:     "грязный Git после хода",
			behavior: testpaseo.BehaviorFinish,
			message:  "После хода агента в Git остались незакоммиченные изменения.",
			priority: config.NtfyPriorityDefault,
		},
		{
			name:          "ошибка агента",
			behavior:      testpaseo.BehaviorError,
			message:       "Агент сообщил об ошибке и ожидает участия пользователя.",
			priority:      config.NtfyPriorityHigh,
			configuration: "high",
		},
		{
			name:     "запрос разрешения",
			behavior: testpaseo.BehaviorPermission,
			message:  "Сессия ожидает решения пользователя по запросу разрешения.",
			priority: config.NtfyPriorityDefault,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := startProductionHarness(t)
			harness.EnableCommandRecording(t)
			prepareProductionRepository(t, harness.Workspace())
			receiver := startIntegrationNtfyReceiver(t)
			writeProductionConfigWithNotification(
				t,
				harness.Workspace(),
				receiver.URL(),
				test.configuration,
			)
			makeProductionRepositoryDirty(t, harness.Workspace())
			harness.SetBehavior(t, testpaseo.BehaviorWorking)

			process := startProductionCommand(t, buildProductionCommand(t), harness)
			sessionID := waitForOnlyOwnSession(t, harness, process)
			waitForRecordedCommandEvent(t, harness, testpaseo.CommandStarted, "wait")
			receiver.AssertNoRequest(t)

			harness.SetBehavior(t, test.behavior)
			request := receiver.WaitRequest(t)
			expected := expectedIntegrationNtfyRequest{
				change:      productionIntegrationChange,
				message:     test.message,
				sessionID:   sessionID,
				sessionLink: integrationSessionLink(t, harness, sessionID),
				priority:    test.priority,
			}
			if err := validateIntegrationNtfyRequest(request, expected); err != nil {
				t.Fatalf("ntfy-запрос не соответствует контракту: %v\nзапрос: %#v", err, request)
			}

			if err := process.command.Process.Signal(os.Interrupt); err != nil {
				t.Fatalf("прервать production-команду после доставки: %v", err)
			}
			result := process.wait(t)
			if result.exitCode != 130 {
				t.Fatalf("прерванная команда вернула код %d вместо 130:\n%s", result.exitCode, result.output)
			}
			assertSingleCommand(t, harness.RecordedCommands(t), "run")
			assertOnlyOwnSession(t, harness, sessionID)
		})
	}
}

func TestProductionКомандаПродолжаетТуЖеСессиюПослеУведомления(t *testing.T) {
	t.Parallel()
	harness := startProductionHarness(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	receiver := startIntegrationNtfyReceiver(t)
	writeProductionConfigWithNotification(t, harness.Workspace(), receiver.URL(), "high")
	makeProductionRepositoryDirty(t, harness.Workspace())
	harness.SetBehavior(t, testpaseo.BehaviorWorking)

	process := startProductionCommand(t, buildProductionCommand(t), harness)
	sessionID := waitForOnlyOwnSession(t, harness, process)
	waitForRecordedCommandEvent(t, harness, testpaseo.CommandStarted, "wait")
	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	request := receiver.WaitRequest(t)
	if err := validateIntegrationNtfyRequest(request, expectedIntegrationNtfyRequest{
		change:      productionIntegrationChange,
		message:     "После хода агента в Git остались незакоммиченные изменения.",
		sessionID:   sessionID,
		sessionLink: integrationSessionLink(t, harness, sessionID),
		priority:    config.NtfyPriorityHigh,
	}); err != nil {
		t.Fatalf("ntfy-запрос не соответствует контракту: %v\nзапрос: %#v", err, request)
	}

	const followUp = "Продолжить подготовку коммитов в той же сессии"
	harness.SetBehavior(t, testpaseo.BehaviorAwaitRelease)
	harness.RunCLI(t, "send", sessionID, followUp, "--no-wait", "--json")
	waitForRecordedCommandEventCount(t, harness, testpaseo.CommandStarted, "wait", 2)
	if err := validateInterventionContinuationEvents(harness.RecordedCommandEvents(t), sessionID); err != nil {
		t.Fatalf("переход от idle к продолжению нарушен: %v\nсобытия: %#v", err, harness.RecordedCommandEvents(t))
	}

	runTool(t, harness.Workspace(), "git", "add", "--all")
	runTool(
		t,
		harness.Workspace(),
		"git",
		"-c", "user.name=OpenSpec Apply Integration",
		"-c", "user.email=integration@example.invalid",
		"commit", "-m", "test: complete continued intervention",
	)
	harness.ReleasePrompt(t)
	waitForRecordedCommandEventCount(t, harness, testpaseo.CommandFinished, "wait", 2)
	harness.RunCLI(t, "archive", sessionID, "--json")
	result := process.wait(t)
	if result.exitCode != exitSuccess {
		t.Fatalf("продолженная production-команда завершилась с кодом %d:\n%s", result.exitCode, result.output)
	}
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status != "" {
		t.Fatalf("после продолжения Git остался изменённым:\n%s", status)
	}
	assertSessionArchived(t, harness, sessionID)
	assertCommandCount(t, harness.RecordedCommands(t), "run", 1)
	receiver.AssertNoRequest(t)

	prompts := harness.Prompts(t)
	if len(prompts) != 2 || strings.TrimSpace(prompts[1]) != followUp {
		t.Fatalf("продолжение не доставлено тому же агенту: %#v", prompts)
	}
}

func TestProductionКомандаСоздаётНовуюПопыткуПослеЗакрытияСГрязнымGit(t *testing.T) {
	t.Parallel()
	harness := startProductionHarness(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	receiver := startIntegrationNtfyReceiver(t)
	writeProductionConfigWithNotification(t, harness.Workspace(), receiver.URL(), "high")
	makeProductionRepositoryDirty(t, harness.Workspace())
	harness.SetBehavior(t, testpaseo.BehaviorWorking)
	binary := buildProductionCommand(t)

	firstProcess := startProductionCommand(t, binary, harness)
	firstSessionID := waitForOnlyOwnSession(t, harness, firstProcess)
	waitForRecordedCommandEvent(t, harness, testpaseo.CommandStarted, "wait")
	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	request := receiver.WaitRequest(t)
	if err := validateIntegrationNtfyRequest(request, expectedIntegrationNtfyRequest{
		change:      productionIntegrationChange,
		message:     "После хода агента в Git остались незакоммиченные изменения.",
		sessionID:   firstSessionID,
		sessionLink: integrationSessionLink(t, harness, firstSessionID),
		priority:    config.NtfyPriorityHigh,
	}); err != nil {
		t.Fatalf("ntfy-запрос не соответствует контракту: %v\nзапрос: %#v", err, request)
	}

	harness.RunCLI(t, "archive", firstSessionID, "--json")
	firstResult := firstProcess.wait(t)
	if firstResult.exitCode != exitObstacle {
		t.Fatalf(
			"закрытая при грязном Git команда завершилась с кодом %d вместо %d:\n%s",
			firstResult.exitCode,
			exitObstacle,
			firstResult.output,
		)
	}
	if !strings.Contains(
		firstResult.output,
		"Сессия "+firstSessionID+" закрыта, но Git содержит незакоммиченные изменения.",
	) {
		t.Fatalf("вывод не сообщает о закрытии с грязным Git:\n%s", firstResult.output)
	}
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status == "" {
		t.Fatal("закрытие сессии неожиданно очистило Git")
	}
	assertSessionArchived(t, harness, firstSessionID)

	harness.ResetCommandRecording(t)
	harness.SetBehavior(t, testpaseo.BehaviorWorking)
	secondProcess := startProductionCommand(t, binary, harness)
	secondSessionID := waitForOnlyOwnSession(t, harness, secondProcess)
	waitForRecordedCommandEvent(t, harness, testpaseo.CommandStarted, "wait")
	if secondSessionID == firstSessionID {
		t.Fatalf("новая явная попытка восстановила закрытую сессию %s", firstSessionID)
	}
	assertCommandCount(t, harness.RecordedCommands(t), "run", 1)
	receiver.AssertNoRequest(t)

	prompts := harness.Prompts(t)
	if len(prompts) != 2 ||
		strings.TrimSpace(prompts[0]) != strings.TrimSpace(promptsPackageText()) ||
		strings.TrimSpace(prompts[1]) != strings.TrimSpace(promptsPackageText()) {
		t.Fatalf("новая попытка не получила новое исходное поручение: %#v", prompts)
	}
	if err := secondProcess.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать новую явную попытку: %v", err)
	}
	if result := secondProcess.wait(t); result.exitCode != 130 {
		t.Fatalf("прерванная новая попытка вернула код %d вместо 130:\n%s", result.exitCode, result.output)
	}
}

type capturedIntegrationNtfyRequest struct {
	method        string
	title         string
	actions       string
	actionHeaders int
	click         string
	clickHeaders  int
	priority      string
	body          string
}

type integrationNtfyReceiver struct {
	server   *httptest.Server
	requests chan capturedIntegrationNtfyRequest
}

func startIntegrationNtfyReceiver(t *testing.T) *integrationNtfyReceiver {
	t.Helper()
	receiver := &integrationNtfyReceiver{requests: make(chan capturedIntegrationNtfyRequest, 4)}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("прочитать тело сквозного ntfy-запроса: %v", err)
		}
		receiver.requests <- capturedIntegrationNtfyRequest{
			method:        request.Method,
			title:         request.Header.Get("Title"),
			actions:       request.Header.Get("Actions"),
			actionHeaders: len(request.Header.Values("Actions")),
			click:         request.Header.Get("Click"),
			clickHeaders:  len(request.Header.Values("Click")),
			priority:      request.Header.Get("Priority"),
			body:          string(body),
		}
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (receiver *integrationNtfyReceiver) URL() string {
	return receiver.server.URL + "/integration-topic"
}

func (receiver *integrationNtfyReceiver) WaitRequest(t *testing.T) capturedIntegrationNtfyRequest {
	t.Helper()
	select {
	case request := <-receiver.requests:
		return request
	case <-time.After(productionIntegrationEventTimeout):
		t.Fatal("не дождаться сквозного ntfy-запроса")
		return capturedIntegrationNtfyRequest{}
	}
}

func (receiver *integrationNtfyReceiver) AssertNoRequest(t *testing.T) {
	t.Helper()
	select {
	case request := <-receiver.requests:
		t.Fatalf("запуск агента неожиданно вызвал ntfy-запрос: %#v", request)
	default:
	}
}

type expectedIntegrationNtfyRequest struct {
	change      string
	message     string
	sessionID   string
	sessionLink string
	priority    config.NtfyPriority
}

func validateIntegrationNtfyRequest(
	request capturedIntegrationNtfyRequest,
	expected expectedIntegrationNtfyRequest,
) error {
	if request.method != http.MethodPost {
		return fmt.Errorf("ожидался POST, получен %q", request.method)
	}
	if want := "Подготовка коммитов: " + expected.change; request.title != want {
		return fmt.Errorf("ожидался Title %q, получен %q", want, request.title)
	}
	wantAction := "view, Открыть сессию, " + expected.sessionLink + ", clear=true"
	if request.actions != wantAction || request.actionHeaders != 1 {
		return fmt.Errorf("ожидался один Actions %q, получено %d: %q", wantAction, request.actionHeaders, request.actions)
	}
	if request.click != "" || request.clickHeaders != 0 {
		return fmt.Errorf("Click должен отсутствовать, получено %d: %q", request.clickHeaders, request.click)
	}
	if request.priority != string(expected.priority) {
		return fmt.Errorf("ожидался Priority %q, получен %q", expected.priority, request.priority)
	}
	if request.body != expected.message {
		return fmt.Errorf("ожидалось тело %q, получено %q", expected.message, request.body)
	}
	for _, private := range []string{expected.sessionID, expected.sessionLink} {
		if strings.Contains(request.title, private) || strings.Contains(request.body, private) {
			return fmt.Errorf("пользовательское представление раскрывает %q", private)
		}
	}
	return nil
}

func integrationSessionLink(t *testing.T, harness *testpaseo.Harness, sessionID string) string {
	t.Helper()
	status := harness.RunCLI(t, "status", "--json")
	var observation struct {
		ServerID string `json:"serverId"`
	}
	if err := json.Unmarshal(status.Stdout, &observation); err != nil {
		t.Fatalf("прочитать serverId изолированного Paseo: %v\n%s", err, status.Stdout)
	}
	if strings.TrimSpace(observation.ServerID) == "" {
		t.Fatalf("изолированный Paseo не сообщил serverId: %s", status.Stdout)
	}
	return "paseo://h/" + observation.ServerID + "/agent/" + sessionID
}

func writeProductionConfigWithNotification(t *testing.T, root, address, priority string) {
	t.Helper()
	priorityField := ""
	if priority != "" {
		priorityField = fmt.Sprintf(",\n      \"priority\": %q", priority)
	}
	content := fmt.Sprintf(`{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": %q,
      "model": %q
    }
  },
  "notifications": {
    "intervention": {
      "type": "ntfy",
      "url": %q%s
    }
  }
}
`, testpaseo.ProviderID, testpaseo.ModelID, address, priorityField)
	writeIntegrationFile(t, filepath.Join(root, config.FileName), content)
}

func waitForRecordedCommandEventCount(
	t *testing.T,
	harness *testpaseo.Harness,
	phase testpaseo.CommandPhase,
	name string,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(productionIntegrationEventTimeout)
	for time.Now().Before(deadline) {
		if countCommandEvents(harness.RecordedCommandEvents(t), phase, name) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf(
		"не дождаться %d событий %q команды %q: %#v",
		want,
		phase,
		name,
		harness.RecordedCommandEvents(t),
	)
}

func countCommandEvents(events []testpaseo.CommandEvent, phase testpaseo.CommandPhase, name string) int {
	count := 0
	for _, event := range events {
		if event.Phase == phase && hasCommandPrefix(event.Arguments, name) {
			count++
		}
	}
	return count
}

func validateInterventionContinuationEvents(events []testpaseo.CommandEvent, sessionID string) error {
	firstWait := commandEventIndex(events, testpaseo.CommandStarted, "wait", 0)
	firstFinish := commandEventIndex(events, testpaseo.CommandFinished, "wait", firstWait+1)
	secondWait := commandEventIndex(events, testpaseo.CommandStarted, "wait", firstFinish+1)
	if firstWait < 0 || firstFinish < 0 || secondWait < 0 {
		return fmt.Errorf("не зафиксированы завершение первого и начало второго wait")
	}
	if !containsArgument(events[firstWait].Arguments, sessionID) ||
		!containsArgument(events[firstFinish].Arguments, sessionID) ||
		!containsArgument(events[secondWait].Arguments, sessionID) {
		return fmt.Errorf("wait относится не к одной сессии %s", sessionID)
	}
	if countCommandEvents(events, testpaseo.CommandStarted, "wait") != 2 {
		return fmt.Errorf("ожидалось ровно два запуска wait")
	}

	observations := make([][]string, 0, 8)
	for _, event := range events[firstFinish+1 : secondWait] {
		if event.Phase != testpaseo.CommandStarted {
			continue
		}
		if hasCommandPrefix(event.Arguments, "workspace", "ls") ||
			hasCommandPrefix(event.Arguments, "ls") ||
			hasCommandPrefix(event.Arguments, "inspect") {
			observations = append(observations, event.Arguments)
		}
	}
	want := [][]string{
		{"workspace", "ls"}, {"ls"}, {"ls"}, {"inspect"},
		{"workspace", "ls"}, {"ls"}, {"ls"}, {"inspect"},
	}
	if len(observations) != len(want) {
		return fmt.Errorf("ожидалось два ограниченных наблюдения, получено %#v", observations)
	}
	for index := range want {
		if !hasCommandPrefix(observations[index], want[index]...) {
			return fmt.Errorf("наблюдение %d ожидало %q, получено %#v", index+1, want[index], observations[index])
		}
	}
	if argumentCount(observations[1], "--label") != 2 ||
		argumentCount(observations[2], "--label") != 5 ||
		argumentCount(observations[5], "--label") != 2 ||
		argumentCount(observations[6], "--label") != 5 {
		return fmt.Errorf("ожидались широкий и точный фильтры каждой пары ls")
	}
	if !containsArgument(observations[3], sessionID) || !containsArgument(observations[7], sessionID) {
		return fmt.Errorf("inspect относится не к сессии %s", sessionID)
	}
	return nil
}
