package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
)

func TestNtfyДоставляетОсмысленноеПредставлениеСАвторизациейТолькоИзОкружения(t *testing.T) {
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
					title:         request.Header.Get("Title"),
					actions:       request.Header.Get("Actions"),
					actionHeaders: len(request.Header.Values("Actions")),
					click:         request.Header.Get("Click"),
					clickHeaders:  len(request.Header.Values("Click")),
					priority:      request.Header.Get("Priority"),
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
			if request.title != "Подготовка коммитов: "+event.Change() {
				t.Errorf("неожиданный Title: %q", request.title)
			}
			wantAction := "view, Открыть сессию, " + event.SessionLink().String() + ", clear=true"
			if request.actions != wantAction || request.actionHeaders != 1 {
				t.Errorf("ожидался один точный Actions, получено %d: %q", request.actionHeaders, request.actions)
			}
			if request.click != "" || request.clickHeaders != 0 {
				t.Errorf("верхнеуровневый Click не должен передаваться, получено %d: %q", request.clickHeaders, request.click)
			}
			if request.priority != string(config.NtfyPriorityDefault) {
				t.Errorf("неожиданный Priority: %q", request.priority)
			}
			if request.authorization != tt.wantAuthorization {
				t.Errorf("неожиданная авторизация: %q", request.authorization)
			}
			if request.contentType != "text/plain; charset=utf-8" {
				t.Errorf("неожиданный Content-Type: %q", request.contentType)
			}
			if request.body != event.Message() {
				t.Errorf("неожиданное тело запроса: %q", request.body)
			}
			for _, private := range []string{event.SessionID().String(), event.SessionLink().String()} {
				if strings.Contains(request.title, private) || strings.Contains(request.body, private) {
					t.Errorf("пользовательское представление раскрывает %q: title=%q body=%q", private, request.title, request.body)
				}
			}
		})
	}
}

func TestNtfyПередаётКаждыйРазрешённыйПриоритетБезПодмены(t *testing.T) {
	tests := []struct {
		configured string
		want       config.NtfyPriority
	}{
		{want: config.NtfyPriorityDefault},
		{configured: "min", want: config.NtfyPriorityMin},
		{configured: "low", want: config.NtfyPriorityLow},
		{configured: "default", want: config.NtfyPriorityDefault},
		{configured: "high", want: config.NtfyPriorityHigh},
		{configured: "max", want: config.NtfyPriorityMax},
	}

	for _, tt := range tests {
		name := tt.configured
		if name == "" {
			name = "отсутствующий"
		}
		t.Run(name, func(t *testing.T) {
			priorities := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				priorities <- request.Header.Get("Priority")
				writer.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			channel := readNtfyChannelWithPriority(t, server.URL+"/topic", "", tt.configured)
			deliverer, err := NewNtfy(channel)
			if err != nil {
				t.Fatalf("создать адаптер ntfy: %v", err)
			}
			if deliveryErr := deliverer.Deliver(context.Background(), testNtfyIntervention(t)); deliveryErr != nil {
				t.Fatalf("доставить уведомление: %v", deliveryErr)
			}
			if got := <-priorities; got != string(tt.want) {
				t.Fatalf("ожидался Priority %q, получен %q", tt.want, got)
			}
		})
	}
}

func TestNtfyПовторяетТоЖеПредставлениеИПриоритетЧерезОдинАдаптер(t *testing.T) {
	requests := make(chan capturedNtfyRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("прочитать тело повторного ntfy-запроса: %v", err)
		}
		requests <- capturedNtfyRequest{
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
	defer server.Close()

	deliverer, err := NewNtfy(readNtfyChannelWithPriority(t, server.URL+"/topic", "", "high"))
	if err != nil {
		t.Fatalf("создать адаптер ntfy: %v", err)
	}
	event := testNtfyIntervention(t)
	for attempt := 1; attempt <= 2; attempt++ {
		if deliveryErr := deliverer.Deliver(context.Background(), event); deliveryErr != nil {
			t.Fatalf("выполнить отправку %d: %v", attempt, deliveryErr)
		}
	}

	first := <-requests
	second := <-requests
	if first != second {
		t.Fatalf("повтор изменил представление: первый=%#v второй=%#v", first, second)
	}
	if first.priority != string(config.NtfyPriorityHigh) {
		t.Fatalf("повтор не сохранил выбранный приоритет: %q", first.priority)
	}
	wantAction := "view, Открыть сессию, " + event.SessionLink().String() + ", clear=true"
	if first.actions != wantAction || first.actionHeaders != 1 || first.click != "" || first.clickHeaders != 0 {
		t.Fatalf(
			"повтор не сохранил единственное очищающее действие: actions=%d:%q click=%d:%q",
			first.actionHeaders,
			first.actions,
			first.clickHeaders,
			first.click,
		)
	}
}

func TestNtfyИспользуетСохранённуюПаруКаналаПослеИзмененияФайла(t *testing.T) {
	var originalRequests atomic.Int32
	originalAuthorization := make(chan string, 1)
	original := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		originalRequests.Add(1)
		originalAuthorization <- request.Header.Get("Authorization")
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
	if authorization := <-originalAuthorization; authorization != "Bearer original-token" {
		t.Fatalf("снимок подменил переменную токена: %q", authorization)
	}
}

func TestNtfyЧитаетЗначениеТокенаНепосредственноПередКаждойОтправкой(t *testing.T) {
	const tokenEnvironment = "ROTATED_NTFY_TOKEN"
	authorizations := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorizations <- request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv(tokenEnvironment, "token-before-construction")
	deliverer, err := NewNtfy(readNtfyChannel(t, server.URL+"/topic", tokenEnvironment))
	if err != nil {
		t.Fatalf("создать адаптер ntfy: %v", err)
	}
	event := testNtfyIntervention(t)

	t.Setenv(tokenEnvironment, "token-before-first-delivery")
	if deliveryErr := deliverer.Deliver(context.Background(), event); deliveryErr != nil {
		t.Fatalf("выполнить первую доставку: %v", deliveryErr)
	}
	t.Setenv(tokenEnvironment, "token-before-second-delivery")
	if deliveryErr := deliverer.Deliver(context.Background(), event); deliveryErr != nil {
		t.Fatalf("выполнить вторую доставку: %v", deliveryErr)
	}

	if authorization := <-authorizations; authorization != "Bearer token-before-first-delivery" {
		t.Fatalf("первая доставка использовала несвежее значение токена: %q", authorization)
	}
	if authorization := <-authorizations; authorization != "Bearer token-before-second-delivery" {
		t.Fatalf("вторая доставка использовала несвежее значение токена: %q", authorization)
	}
}

func TestNtfyНеОбращаетсяКСетиБезНепустогоТокена(t *testing.T) {
	tests := []struct {
		name    string
		present bool
		value   string
	}{
		{name: "переменная отсутствует"},
		{name: "переменная пуста", present: true},
		{name: "переменная содержит только пробелы", present: true, value: " \t "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const tokenEnvironment = "MISSING_NTFY_TOKEN"
			if tt.present {
				t.Setenv(tokenEnvironment, tt.value)
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

func TestNtfyНеСледуетПеренаправлениямСТокеномИБезНего(t *testing.T) {
	redirects := []struct {
		name string
		kind redirectKind
	}{
		{name: "same-origin", kind: redirectSameOrigin},
		{name: "cross-origin", kind: redirectCrossOrigin},
		{name: "HTTPS в HTTP", kind: redirectHTTPSDowngrade},
	}
	for _, redirect := range redirects {
		for _, authorized := range []bool{false, true} {
			name := redirect.name + "/без токена"
			if authorized {
				name = redirect.name + "/с токеном"
			}
			t.Run(name, func(t *testing.T) {
				testNtfyRedirect(t, redirect.kind, authorized)
			})
		}
	}
}

func TestNtfyОграничиваетВремяЗапросаИУчитываетОтмену(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		timeout time.Duration
	}{
		{
			name: "внутренний тайм-аут",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			timeout: 20 * time.Millisecond,
		},
		{
			name: "отмена вызывающего кода",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			timeout: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestFinished := make(chan error, 1)
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				requestFinished <- request.Context().Err()
				return nil, request.Context().Err()
			})
			deliverer := newTestNtfy(t, "http://127.0.0.1/topic", "", ntfyDependencies{
				transport:         transport,
				lookupEnvironment: os.LookupEnv,
				timeout:           tt.timeout,
				maximumResponse:   maximumNtfyResponseBytes,
			})
			ctx, cancel := tt.context()
			defer cancel()

			started := time.Now()
			if deliveryErr := deliverer.Deliver(ctx, testNtfyIntervention(t)); deliveryErr == nil {
				t.Fatal("прерванный запрос не должен подтверждать доставку")
			}
			if time.Since(started) > time.Second {
				t.Fatal("прерванный запрос не завершился в ограниченное время")
			}
			select {
			case observed := <-requestFinished:
				if !errors.Is(observed, context.Canceled) && !errors.Is(observed, context.DeadlineExceeded) {
					t.Fatalf("transport получил неожиданную причину завершения: %v", observed)
				}
			default:
				// Отменённый до отправки запрос может не достигнуть RoundTripper.
			}
		})
	}
}

func TestNtfyОграничиваетОтветИПодтверждаетТолькоУспешныйСтатус(t *testing.T) {
	t.Run("ответ превышает предел", func(t *testing.T) {
		reader := &countingReader{reader: strings.NewReader(strings.Repeat("x", 128))}
		deliverer := newTestNtfy(t, "http://127.0.0.1/topic", "", ntfyDependencies{
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(reader),
					Header:     make(http.Header),
				}, nil
			}),
			lookupEnvironment: os.LookupEnv,
			timeout:           time.Second,
			maximumResponse:   16,
		})

		if deliveryErr := deliverer.Deliver(context.Background(), testNtfyIntervention(t)); deliveryErr == nil {
			t.Fatal("слишком большой ответ не должен подтверждать доставку")
		}
		if reader.read > 17 {
			t.Fatalf("адаптер прочитал %d байт при пределе 16", reader.read)
		}
	})

	t.Run("неуспешный ответ не раскрывает внешние данные", func(t *testing.T) {
		const privateResponse = "private response with token secret-token-value"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(privateResponse))
		}))
		defer server.Close()
		deliverer, err := NewNtfy(readNtfyChannel(t, server.URL+"/private-topic", ""))
		if err != nil {
			t.Fatalf("создать адаптер ntfy: %v", err)
		}
		event := testNtfyIntervention(t)

		deliveryErr := deliverer.Deliver(context.Background(), event)
		if deliveryErr == nil {
			t.Fatal("неуспешный HTTP-статус не должен подтверждать доставку")
		}
		for _, private := range []string{privateResponse, server.URL, event.Message(), event.SessionLink().String()} {
			if strings.Contains(deliveryErr.Error(), private) {
				t.Fatalf("ошибка доставки раскрыла внешние данные %q: %q", private, deliveryErr.Error())
			}
		}
	})
}

type redirectKind uint8

const (
	redirectSameOrigin redirectKind = iota + 1
	redirectCrossOrigin
	redirectHTTPSDowngrade
)

func testNtfyRedirect(t *testing.T, kind redirectKind, authorized bool) {
	t.Helper()
	var sourceRequests atomic.Int32
	var redirectedRequests atomic.Int32

	var destination *httptest.Server
	if kind == redirectCrossOrigin || kind == redirectHTTPSDowngrade {
		destination = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			redirectedRequests.Add(1)
			writer.WriteHeader(http.StatusOK)
		}))
		defer destination.Close()
	}

	var source *httptest.Server
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			redirectedRequests.Add(1)
			writer.WriteHeader(http.StatusOK)
			return
		}
		sourceRequests.Add(1)
		location := source.URL + "/redirected"
		if destination != nil {
			location = destination.URL + "/redirected"
		}
		http.Redirect(writer, request, location, http.StatusTemporaryRedirect)
	})
	if kind == redirectHTTPSDowngrade {
		source = httptest.NewTLSServer(handler)
	} else {
		source = httptest.NewServer(handler)
	}
	defer source.Close()

	tokenEnvironment := ""
	if authorized {
		tokenEnvironment = "REDIRECT_NTFY_TOKEN"
		t.Setenv(tokenEnvironment, "redirect-secret-token")
	}
	transport := http.DefaultTransport
	if kind == redirectHTTPSDowngrade {
		transport = source.Client().Transport
	}
	deliverer := newTestNtfy(t, source.URL+"/topic", tokenEnvironment, ntfyDependencies{
		transport:         transport,
		lookupEnvironment: os.LookupEnv,
		timeout:           time.Second,
		maximumResponse:   maximumNtfyResponseBytes,
	})

	deliveryErr := deliverer.Deliver(context.Background(), testNtfyIntervention(t))
	if deliveryErr == nil {
		t.Fatal("перенаправление не должно подтверждать доставку")
	}
	if sourceRequests.Load() != 1 {
		t.Fatalf("исходный сервер получил %d запросов, ожидался один", sourceRequests.Load())
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("адрес из Location получил %d запросов", redirectedRequests.Load())
	}
	for _, private := range []string{source.URL, "redirect-secret-token"} {
		if strings.Contains(deliveryErr.Error(), private) {
			t.Fatalf("ошибка редиректа раскрыла %q: %q", private, deliveryErr.Error())
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	read, err := reader.reader.Read(buffer)
	reader.read += read
	return read, err
}

func newTestNtfy(
	t *testing.T,
	address string,
	tokenEnvironment string,
	dependencies ntfyDependencies,
) *ntfyDeliverer {
	t.Helper()
	channel := readNtfyChannel(t, address, tokenEnvironment)
	deliverer, err := newNtfy(channel, dependencies)
	if err != nil {
		t.Fatalf("создать тестовый адаптер ntfy: %v", err)
	}
	return deliverer
}

type capturedNtfyRequest struct {
	method        string
	title         string
	actions       string
	actionHeaders int
	click         string
	clickHeaders  int
	priority      string
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
	return readNtfyChannelWithPriority(t, address, tokenEnvironment, "")
}

func readNtfyChannelWithPriority(
	t *testing.T,
	address string,
	tokenEnvironment string,
	priority string,
) config.InterventionChannel {
	t.Helper()
	root := testConfigRoot(t)
	writeNtfyConfigWithPriority(t, root, address, tokenEnvironment, priority)
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
	writeNtfyConfigWithPriority(t, root, address, tokenEnvironment, "")
}

func writeNtfyConfigWithPriority(
	t *testing.T,
	root config.RepositoryRoot,
	address string,
	tokenEnvironment string,
	priority string,
) {
	t.Helper()
	tokenField := ""
	if tokenEnvironment != "" {
		tokenField = fmt.Sprintf(",\"tokenEnv\":%q", tokenEnvironment)
	}
	priorityField := ""
	if priority != "" {
		priorityField = fmt.Sprintf(",\"priority\":%q", priority)
	}
	document := fmt.Sprintf(
		`{"version":1,"notifications":{"intervention":{"type":"ntfy","url":%q%s%s}}}`,
		address,
		tokenField,
		priorityField,
	)
	if err := os.WriteFile(filepath.Join(root.String(), config.FileName), []byte(document), 0o600); err != nil {
		t.Fatalf("записать конфигурацию: %v", err)
	}
}
