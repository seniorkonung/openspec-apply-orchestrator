//go:build paseo_integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

func TestProductionПользовательПослеУведомленияПродолжаетТоЖеПоручение(t *testing.T) {
	scenario := startProductionScenario(t)
	harness := scenario.harness
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, scenario)
	receiver := startIntegrationNtfyReceiver(t, scenario)
	writeProductionConfigWithNotification(t, harness.Workspace(), receiver.URL(), "high")
	makeProductionRepositoryDirty(t, scenario)
	harness.SetBehavior(t, testpaseo.BehaviorWorking)

	process := startProductionCommand(t, scenario)
	sessionID := waitForOnlyOwnSession(t, scenario, process)
	waitForRecordedCommandEvent(t, scenario, testpaseo.CommandStarted, "wait")
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
	waitForRecordedCommandEventCount(t, scenario, testpaseo.CommandStarted, "wait", 2)
	if err := validateInterventionContinuationEvents(harness.RecordedCommandEvents(t), sessionID); err != nil {
		t.Fatalf("переход от idle к продолжению нарушен: %v\nсобытия: %#v", err, harness.RecordedCommandEvents(t))
	}

	runTool(t, scenario, harness.Workspace(), "git", "add", "--all")
	runTool(
		t,
		scenario,
		harness.Workspace(),
		"git",
		"-c", "user.name=OpenSpec Apply Integration",
		"-c", "user.email=integration@example.invalid",
		"commit", "-m", "test: complete continued intervention",
	)
	harness.ReleasePrompt(t)
	waitForRecordedCommandEventCount(t, scenario, testpaseo.CommandFinished, "wait", 2)
	harness.RunCLI(t, "archive", sessionID, "--json")
	result := process.wait(t)
	if result.exitCode != exitSuccess {
		t.Fatalf("продолженная production-команда завершилась с кодом %d:\n%s", result.exitCode, result.output)
	}
	if status := gitOutput(t, scenario, "status", "--porcelain=v1"); status != "" {
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
	context  context.Context
	server   *httptest.Server
	requests chan capturedIntegrationNtfyRequest
}

func startIntegrationNtfyReceiver(t *testing.T, scenario *productionScenario) *integrationNtfyReceiver {
	t.Helper()
	receiver := &integrationNtfyReceiver{
		context:  scenario.context,
		requests: make(chan capturedIntegrationNtfyRequest, 4),
	}
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
	case <-receiver.context.Done():
		t.Fatalf("истёк deadline пользовательского сценария: %v", receiver.context.Err())
		return capturedIntegrationNtfyRequest{}
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
	scenario *productionScenario,
	phase testpaseo.CommandPhase,
	name string,
	want int,
) {
	t.Helper()
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		if countCommandEvents(scenario.harness.RecordedCommandEvents(t), phase, name) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf(
		"не дождаться %d событий %q команды %q: %#v",
		want,
		phase,
		name,
		scenario.harness.RecordedCommandEvents(t),
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
