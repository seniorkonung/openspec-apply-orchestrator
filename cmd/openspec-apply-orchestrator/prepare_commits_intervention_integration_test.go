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
