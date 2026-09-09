package paseocli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestАдаптерЧитаетМинимальнуюПроекциюАктивныхWorkspace(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	adapter := newCatalogTestAdapter(t)
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{
		{
			"workspaceId": "workspace-1",
			"project":     "репозиторий",
			"name":        "oa-v1-workspace",
			"isolation":   "local",
			"cwd":         "/tmp/repo",
			"новое":       map[string]any{"поле": true},
		},
	}))

	workspaces, err := adapter.ListActiveWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("прочитать активные workspace: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].ID() != "workspace-1" ||
		workspaces[0].Name() != "oa-v1-workspace" || workspaces[0].CWD() != "/tmp/repo" {
		t.Fatalf("неожиданная проекция workspace: %#v", workspaces)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(recorded) != "workspace\nls\n--json\n" {
		t.Fatalf("неожиданная команда списка workspace:\n%s", recorded)
	}
}

func TestАдаптерОтклоняетНедостоверныйСписокWorkspace(t *testing.T) {
	tests := []struct {
		name   string
		output any
	}{
		{
			name: "отсутствует обязательное имя",
			output: []map[string]any{{
				"workspaceId": "workspace-1",
				"isolation":   "local",
				"cwd":         "/tmp/repo",
			}},
		},
		{
			name: "неизвестная изоляция",
			output: []map[string]any{{
				"workspaceId": "workspace-1",
				"name":        "oa-v1-workspace",
				"isolation":   "remote",
				"cwd":         "/tmp/repo",
			}},
		},
		{
			name: "повтор полного ID",
			output: []map[string]any{
				{
					"workspaceId": "workspace-1",
					"name":        "oa-v1-workspace",
					"isolation":   "local",
					"cwd":         "/tmp/repo",
				},
				{
					"workspaceId": "workspace-1",
					"name":        "другой",
					"isolation":   "worktree",
					"cwd":         "/tmp/worktree",
				},
			},
		},
		{name: "null вместо списка", output: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recordPath := filepath.Join(t.TempDir(), "вызовы")
			adapter := newCatalogTestAdapter(t)
			t.Setenv("FAKE_PASEO_RECORD", recordPath)
			t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, tt.output))
			_, err := adapter.ListActiveWorkspaces(context.Background())
			if !errors.Is(err, ErrUnexpectedJSON) {
				t.Fatalf("ожидалась ошибка значимого JSON-контракта, получено %v", err)
			}
			recorded, readErr := os.ReadFile(recordPath)
			if readErr != nil {
				t.Fatalf("прочитать журнал отказа: %v", readErr)
			}
			if string(recorded) != "workspace\nls\n--json\n" {
				t.Fatalf("при отказе ожидалась только команда чтения:\n%s", recorded)
			}
		})
	}
}
