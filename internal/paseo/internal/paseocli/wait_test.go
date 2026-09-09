package paseocli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestАдаптерОжидаетСессиюОднойКомандойИВозвращаетСигналСобытия(t *testing.T) {
	tests := []struct {
		status string
		want   WaitEvent
	}{
		{status: "idle", want: WaitEventIdle},
		{status: "timeout", want: WaitEventTimeout},
		{status: "permission", want: WaitEventPermission},
		{status: "error", want: WaitEventAgentError},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			recordPath := filepath.Join(t.TempDir(), "вызовы")
			adapter := newCatalogTestAdapter(t)
			t.Setenv("FAKE_PASEO_RECORD", recordPath)
			t.Setenv("FAKE_PASEO_STDOUT", waitJSON(t, map[string]any{
				"agentId": "agent-123",
				"status":  tt.status,
				"message": "непрозрачная активность",
				"новое":   map[string]any{"поле": true},
			}))

			event, err := adapter.WaitSession(context.Background(), "agent-123")
			if err != nil {
				t.Fatalf("дождаться события сессии: %v", err)
			}
			if event != tt.want {
				t.Fatalf("неожиданный сигнал: получен %v, нужен %v", event, tt.want)
			}
			if strings.Contains(fmt.Sprintf("%#v", event), "непрозрачная активность") {
				t.Fatalf("сигнал сохранил message: %#v", event)
			}

			recorded, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatalf("прочитать журнал вызовов: %v", err)
			}
			if string(recorded) != "wait\nagent-123\n--json\n" {
				t.Fatalf("ожидалась одна команда wait без inspect:\n%s", recorded)
			}
		})
	}
}

func TestАдаптерСтрогоПроверяетРезультатWaitБезРаскрытияMessage(t *testing.T) {
	const secret = "СЕКРЕТНАЯ НЕДАВНЯЯ АКТИВНОСТЬ"
	tests := []struct {
		name   string
		output string
		want   error
	}{
		{
			name: "несовпавший полный ID",
			output: waitJSON(t, map[string]any{
				"agentId": "agent-other", "status": "idle", "message": secret,
			}),
			want: ErrWaitSessionIdentityMismatch,
		},
		{
			name: "неизвестный статус",
			output: waitJSON(t, map[string]any{
				"agentId": "agent-123", "status": "working", "message": secret,
			}),
			want: ErrUnexpectedJSON,
		},
		{
			name:   "отсутствует message",
			output: `{"agentId":"agent-123","status":"idle"}`,
			want:   ErrUnexpectedJSON,
		},
		{
			name:   "обрезанный JSON",
			output: `{"agentId":"agent-123"`,
			want:   ErrTruncatedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := newCatalogTestAdapter(t)
			t.Setenv("FAKE_PASEO_STDOUT", tt.output)

			_, err := adapter.WaitSession(context.Background(), "agent-123")
			if !errors.Is(err, tt.want) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.want, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("ошибка раскрыла message: %v", err)
			}
		})
	}
}

func waitJSON(t *testing.T, value any) string {
	t.Helper()
	return catalogJSON(t, value)
}
