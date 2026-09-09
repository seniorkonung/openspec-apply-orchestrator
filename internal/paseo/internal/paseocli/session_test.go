package paseocli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestАдаптерЧитаетСессииТочнымиФильтрамиИВозвращаетМинимальнуюПроекцию(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	adapter := newCatalogTestAdapter(t)
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{
		{
			"id":       "agent-123",
			"shortId":  "agent-1",
			"name":     "агент",
			"provider": "codex/gpt-5",
			"thinking": "high",
			"status":   "running",
			"cwd":      "/tmp/repo",
			"created":  "только что",
			"новое":    map[string]any{"поле": true},
		},
	}))

	sessions, err := adapter.ListSessions(context.Background(), []SessionLabel{
		{Key: "oa.owner", Value: "openspec-apply-orchestrator"},
		{Key: "oa.change", Value: "change-key"},
	})
	if err != nil {
		t.Fatalf("прочитать активные сессии: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID() != "agent-123" || sessions[0].State() != SessionRunning {
		t.Fatalf("неожиданная проекция сессий: %#v", sessions)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	want := "ls\n--global\n--label\noa.owner=openspec-apply-orchestrator\n" +
		"--label\noa.change=change-key\n--json\n"
	if string(recorded) != want {
		t.Fatalf("неожиданная команда списка сессий:\n%s", recorded)
	}
}

func TestАдаптерПроверяетInspectИОтбрасываетНеиспользуемыеПоля(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	adapter := newCatalogTestAdapter(t)
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	inspection := sessionInspectionFixture("agent-123", "running", "/tmp/repo")
	inspection["PendingPermissions"] = []map[string]any{{"id": "permission-1", "tool": "Bash"}}
	inspection["новое"] = map[string]any{"секрет": "не переносить"}
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, inspection))

	got, err := adapter.InspectSession(context.Background(), "agent-123")
	if err != nil {
		t.Fatalf("прочитать inspect: %v", err)
	}
	if got.ID() != "agent-123" || got.State() != SessionRunning || got.CWD() != "/tmp/repo" ||
		got.Archived() || !got.HasPendingPermission() {
		t.Fatalf("неожиданная проекция inspect: %#v", got)
	}
	if _, hasParent := got.ParentID(); hasParent {
		t.Fatalf("неожиданная родительская сессия: %#v", got)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(recorded) != "inspect\nagent-123\n--json\n" {
		t.Fatalf("неожиданная команда inspect:\n%s", recorded)
	}
}

func TestАдаптерОтклоняетНедостоверныеСведенияОСессиях(t *testing.T) {
	tests := []struct {
		name string
		want error
		read func(*testing.T, *Adapter) error
	}{
		{
			name: "список равен null",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				t.Setenv("FAKE_PASEO_STDOUT", "null")
				_, err := adapter.ListSessions(context.Background(), nil)
				return err
			},
		},
		{
			name: "список не содержит состояние",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{{"id": "agent-123"}}))
				_, err := adapter.ListSessions(context.Background(), nil)
				return err
			},
		},
		{
			name: "список содержит неизвестное состояние",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{{
					"id": "agent-123", "status": "sleeping",
				}}))
				_, err := adapter.ListSessions(context.Background(), nil)
				return err
			},
		},
		{
			name: "список повторяет полный ID",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{
					{"id": "agent-123", "status": "running"},
					{"id": "agent-123", "status": "idle"},
				}))
				_, err := adapter.ListSessions(context.Background(), nil)
				return err
			},
		},
		{
			name: "inspect вернул другой ID",
			want: ErrSessionIdentityMismatch,
			read: func(t *testing.T, adapter *Adapter) error {
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, sessionInspectionFixture(
					"agent-other", "running", "/tmp/repo",
				)))
				_, err := adapter.InspectSession(context.Background(), "agent-123")
				return err
			},
		},
		{
			name: "inspect не содержит cwd",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				value := sessionInspectionFixture("agent-123", "running", "/tmp/repo")
				delete(value, "Cwd")
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, value))
				_, err := adapter.InspectSession(context.Background(), "agent-123")
				return err
			},
		},
		{
			name: "архивирование противоречит работе",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				value := sessionInspectionFixture("agent-123", "running", "/tmp/repo")
				value["Archived"] = true
				value["ArchivedAt"] = "2026-09-07T09:20:00Z"
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, value))
				_, err := adapter.InspectSession(context.Background(), "agent-123")
				return err
			},
		},
		{
			name: "permission не содержит tool",
			want: ErrUnexpectedJSON,
			read: func(t *testing.T, adapter *Adapter) error {
				value := sessionInspectionFixture("agent-123", "idle", "/tmp/repo")
				value["PendingPermissions"] = []map[string]any{{"id": "permission-1"}}
				t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, value))
				_, err := adapter.InspectSession(context.Background(), "agent-123")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := newCatalogTestAdapter(t)
			if err := tt.read(t, adapter); !errors.Is(err, tt.want) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.want, err)
			}
		})
	}
}

func sessionInspectionFixture(id, status, cwd string) map[string]any {
	return map[string]any{
		"Id":                 id,
		"Name":               "агент",
		"Provider":           "codex",
		"Model":              "gpt-5",
		"Thinking":           "high",
		"Status":             status,
		"Archived":           false,
		"ArchivedAt":         nil,
		"Mode":               "default",
		"Cwd":                cwd,
		"CreatedAt":          "2026-09-07T09:00:00Z",
		"UpdatedAt":          "2026-09-07T09:10:00Z",
		"LastUsage":          nil,
		"Capabilities":       nil,
		"AvailableModes":     nil,
		"PendingPermissions": []map[string]any{},
		"Worktree":           nil,
		"ParentAgentId":      nil,
	}
}
