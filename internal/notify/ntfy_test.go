package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
)

func TestNtfyДоставляетСсылкуСАвторизациейТолькоИзОкружения(t *testing.T) {
	tests := []struct {
		name              string
		tokenEnvironment  string
		token             string
		wantAuthorization string
	}{
		{name: "без токена", wantAuthorization: ""},
		{
			name:              "с Bearer-токеном",
			tokenEnvironment:  "TEST_NTFY_TOKEN",
			token:             "secret-token-value",
			wantAuthorization: "Bearer secret-token-value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tokenEnvironment != "" {
				t.Setenv(tt.tokenEnvironment, tt.token)
			}

			requests := make(chan capturedNtfyRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("прочитать тело ntfy-запроса: %v", err)
				}
				requests <- capturedNtfyRequest{
					method:        request.Method,
					click:         request.Header.Get("Click"),
					authorization: request.Header.Get("Authorization"),
					contentType:   request.Header.Get("Content-Type"),
					body:          string(body),
				}
				writer.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			channel := readNtfyChannel(t, server.URL+"/trusted-topic", tt.tokenEnvironment)
			deliverer, err := NewNtfy(channel)
			if err != nil {
				t.Fatalf("создать адаптер ntfy: %v", err)
			}
			event := testNtfyIntervention(t)

			if deliveryErr := deliverer.Deliver(context.Background(), event); deliveryErr != nil {
				t.Fatalf("доставить событие: %v", deliveryErr)
			}

			request := <-requests
			if request.method != http.MethodPost {
				t.Errorf("ожидался POST, получен %q", request.method)
			}
			if request.click != event.SessionLink().String() {
				t.Errorf("заголовок Click не содержит ссылку сессии: %q", request.click)
			}
			if request.authorization != tt.wantAuthorization {
				t.Errorf("неожиданная авторизация: %q", request.authorization)
			}
			if request.contentType != "text/plain; charset=utf-8" {
				t.Errorf("неожиданный Content-Type: %q", request.contentType)
			}
			for _, want := range []string{event.Change(), event.SessionID().String(), event.Message()} {
				if !strings.Contains(request.body, want) {
					t.Errorf("тело запроса не содержит %q: %q", want, request.body)
				}
			}
		})
	}
}

func TestNtfyИспользуетСохранённуюПаруКаналаПослеИзмененияФайла(t *testing.T) {
	var originalRequests atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		originalRequests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer original.Close()

	var changedRequests atomic.Int32
	changed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		changedRequests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer changed.Close()

	root := testConfigRoot(t)
	writeNtfyConfig(t, root, original.URL+"/original-topic", "ORIGINAL_NTFY_TOKEN")
	snapshot := config.ReadSnapshot(root)
	writeNtfyConfig(t, root, changed.URL+"/changed-topic", "CHANGED_NTFY_TOKEN")
	t.Setenv("ORIGINAL_NTFY_TOKEN", "original-token")
	t.Setenv("CHANGED_NTFY_TOKEN", "changed-token")

	channel, err := snapshot.InterventionChannel()
	if err != nil {
		t.Fatalf("получить канал из снимка: %v", err)
	}
	deliverer, err := NewNtfy(channel)
	if err != nil {
		t.Fatalf("создать адаптер ntfy: %v", err)
	}
	if deliveryErr := deliverer.Deliver(context.Background(), testNtfyIntervention(t)); deliveryErr != nil {
		t.Fatalf("доставить событие через исходный снимок: %v", deliveryErr)
	}

	if originalRequests.Load() != 1 {
		t.Fatalf("исходный адрес получил %d запросов, ожидался один", originalRequests.Load())
	}
	if changedRequests.Load() != 0 {
		t.Fatalf("изменённый адрес получил %d запросов", changedRequests.Load())
	}
}

func TestNtfyНеОбращаетсяКСетиБезНепустогоТокена(t *testing.T) {
	for _, token := range []string{"", "отсутствует"} {
		t.Run(fmt.Sprintf("значение_%q", token), func(t *testing.T) {
			const tokenEnvironment = "MISSING_NTFY_TOKEN"
			if token == "" {
				t.Setenv(tokenEnvironment, "")
			} else {
				_ = os.Unsetenv(tokenEnvironment)
			}

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writer.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			deliverer, err := NewNtfy(readNtfyChannel(t, server.URL+"/topic", tokenEnvironment))
			if err != nil {
				t.Fatalf("создать адаптер ntfy: %v", err)
			}
			deliveryErr := deliverer.Deliver(context.Background(), testNtfyIntervention(t))
			if deliveryErr == nil {
				t.Fatal("доставка без токена не должна подтверждаться")
			}
			if requests.Load() != 0 {
				t.Fatalf("без токена выполнено сетевых запросов: %d", requests.Load())
			}
		})
	}
}

type capturedNtfyRequest struct {
	method        string
	click         string
	authorization string
	contentType   string
	body          string
}

func testNtfyIntervention(t *testing.T) Intervention {
	t.Helper()
	session, err := NewKnownSession("agent-local-123", "paseo://h/server-local/agent/agent-local-123")
	if err != nil {
		t.Fatalf("создать известную сессию: %v", err)
	}
	event, err := NewIntervention("orchestrate-commit-preparation", ReasonAgentError, session)
	if err != nil {
		t.Fatalf("создать событие: %v", err)
	}
	return event
}

func readNtfyChannel(t *testing.T, address, tokenEnvironment string) config.InterventionChannel {
	t.Helper()
	root := testConfigRoot(t)
	writeNtfyConfig(t, root, address, tokenEnvironment)
	channel, err := config.ReadSnapshot(root).InterventionChannel()
	if err != nil {
		t.Fatalf("прочитать канал ntfy: %v", err)
	}
	return channel
}

func testConfigRoot(t *testing.T) config.RepositoryRoot {
	t.Helper()
	root, err := config.NewRepositoryRoot(t.TempDir())
	if err != nil {
		t.Fatalf("создать корень конфигурации: %v", err)
	}
	return root
}

func writeNtfyConfig(t *testing.T, root config.RepositoryRoot, address, tokenEnvironment string) {
	t.Helper()
	tokenField := ""
	if tokenEnvironment != "" {
		tokenField = fmt.Sprintf(",\"tokenEnv\":%q", tokenEnvironment)
	}
	document := fmt.Sprintf(
		`{"version":1,"notifications":{"intervention":{"type":"ntfy","url":%q%s}}}`,
		address,
		tokenField,
	)
	if err := os.WriteFile(filepath.Join(root.String(), config.FileName), []byte(document), 0o600); err != nil {
		t.Fatalf("записать конфигурацию: %v", err)
	}
}
