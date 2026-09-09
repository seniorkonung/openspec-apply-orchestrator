package paseocli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestАдаптерСоздаётWorkspaceИВозвращаетПровереннуюПроекцию(t *testing.T) {
	adapter := newMutationTestAdapter(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, map[string]any{
		"workspaceId": "workspace-created",
		"project":     "репозиторий",
		"name":        "oa-v1-workspace",
		"isolation":   "local",
		"cwd":         "/tmp/repo",
		"новое":       map[string]any{"поле": true},
	}))

	workspace, err := adapter.CreateWorkspace(context.Background(), WorkspaceCreation{
		Name: "oa-v1-workspace",
		CWD:  "/tmp/repo",
	})
	if err != nil {
		t.Fatalf("создать workspace: %v", err)
	}
	if workspace.ID() != "workspace-created" || workspace.Name() != "oa-v1-workspace" ||
		workspace.CWD() != "/tmp/repo" {
		t.Fatalf("неожиданная проекция созданного workspace: %#v", workspace)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	want := "workspace\ncreate\n--isolation\nlocal\n--path\n/tmp/repo\n" +
		"--title\noa-v1-workspace\n--json\n"
	if string(recorded) != want {
		t.Fatalf("неожиданная команда создания workspace:\n%s", recorded)
	}
}

func TestАдаптерСоздаётСессиюСТочнымРежимомИОчищеннымКонтекстом(t *testing.T) {
	adapter := newMutationTestAdapter(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	environmentPath := filepath.Join(t.TempDir(), "окружение")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_ENV_RECORD", environmentPath)
	t.Setenv("PASEO_AGENT_ID", "чужой-родитель")
	t.Setenv("PASEO_WORKSPACE_ID", "чужой-workspace")
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, map[string]any{
		"agentId":  "agent-created",
		"status":   "running",
		"provider": "codex",
		"cwd":      "/tmp/repo",
		"title":    "подготовка",
		"новое":    []string{"поле"},
	}))
	mode, supported := ActiveContract().FullAccessMode("codex")
	if !supported {
		t.Fatal("активный контракт не содержит режим codex")
	}

	session, err := adapter.CreateSession(context.Background(), SessionCreation{
		WorkspaceID:  "workspace-1",
		Provider:     "codex",
		Model:        "gpt-5.6",
		Reasoning:    "high",
		HasReasoning: true,
		Mode:         mode,
		Labels: []SessionLabel{
			{Key: "oa.owner", Value: "openspec-apply-orchestrator"},
			{Key: "oa.workspace", Value: "workspace-1"},
		},
		Prompt: "Подготовь коммиты.",
	})
	if err != nil {
		t.Fatalf("создать сессию: %v", err)
	}
	if session.ID() != "agent-created" || session.CWD() != "/tmp/repo" {
		t.Fatalf("неожиданная проекция созданной сессии: %#v", session)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	want := strings.Join([]string{
		"run", "--background",
		"--workspace", "workspace-1",
		"--provider", "codex",
		"--model", "gpt-5.6",
		"--thinking", "high",
		"--mode", "full-access",
		"--label", "oa.owner=openspec-apply-orchestrator",
		"--label", "oa.workspace=workspace-1",
		"--json", "--", "Подготовь коммиты.",
	}, "\n") + "\n"
	if string(recorded) != want {
		t.Fatalf("неожиданная команда создания сессии:\n%s", recorded)
	}

	processEnvironment, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatalf("прочитать окружение Paseo: %v", err)
	}
	if string(processEnvironment) != "PASEO_AGENT_ID=unset\nPASEO_WORKSPACE_ID=unset\n" {
		t.Fatalf("контекст чужой сессии передан Paseo:\n%s", processEnvironment)
	}
}

func TestАдаптерАрхивируетСессиюБезForce(t *testing.T) {
	adapter := newMutationTestAdapter(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, map[string]any{
		"agentId":    "agent-123",
		"status":     "archived",
		"archivedAt": "2026-09-07T09:20:00Z",
		"новое":      true,
	}))

	if err := adapter.ArchiveSession(context.Background(), "agent-123"); err != nil {
		t.Fatalf("архивировать сессию: %v", err)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(recorded) != "archive\nagent-123\n--json\n" {
		t.Fatalf("неожиданная команда архивирования:\n%s", recorded)
	}
	if strings.Contains(string(recorded), "--force") {
		t.Fatal("архивирование не должно использовать --force")
	}
}

func TestАдаптерОтклоняетНедостоверныеОтветыМутаций(t *testing.T) {
	tests := []struct {
		name   string
		output map[string]any
		mutate func(context.Context, *Adapter) error
	}{
		{
			name: "созданный workspace без project",
			output: map[string]any{
				"workspaceId": "workspace-1", "name": "oa-v1-workspace",
				"isolation": "local", "cwd": "/tmp/repo",
			},
			mutate: func(ctx context.Context, adapter *Adapter) error {
				_, err := adapter.CreateWorkspace(ctx, WorkspaceCreation{Name: "oa-v1-workspace", CWD: "/tmp/repo"})
				return err
			},
		},
		{
			name: "созданная сессия с неизвестным состоянием",
			output: map[string]any{
				"agentId": "agent-123", "status": "sleeping", "provider": "codex",
				"cwd": "/tmp/repo", "title": "подготовка",
			},
			mutate: func(ctx context.Context, adapter *Adapter) error {
				mode, _ := ActiveContract().FullAccessMode("codex")
				_, err := adapter.CreateSession(ctx, SessionCreation{
					WorkspaceID: "workspace-1", Provider: "codex", Model: "gpt-5.6",
					Mode: mode, Prompt: "Подготовь коммиты.",
				})
				return err
			},
		},
		{
			name: "archive с некорректным временем",
			output: map[string]any{
				"agentId": "agent-123", "status": "archived", "archivedAt": "вчера",
			},
			mutate: func(ctx context.Context, adapter *Adapter) error {
				return adapter.ArchiveSession(ctx, "agent-123")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := newMutationTestAdapter(t)
			t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, tt.output))
			if err := tt.mutate(context.Background(), adapter); !errors.Is(err, ErrUnexpectedJSON) {
				t.Fatalf("ожидалась ошибка значимого JSON-контракта, получено %v", err)
			}
		})
	}
}

func TestАдаптерНеЗапускаетСессиюБезПроверенногоРежима(t *testing.T) {
	adapter := newMutationTestAdapter(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)

	_, err := adapter.CreateSession(context.Background(), SessionCreation{
		WorkspaceID: "workspace-1",
		Provider:    "codex",
		Model:       "gpt-5.6",
		Prompt:      "Подготовь коммиты.",
	})
	if !errors.Is(err, ErrInvalidSessionMutation) {
		t.Fatalf("ожидался отказ без проверенного режима, получено %v", err)
	}
	if _, readErr := os.ReadFile(recordPath); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("недопустимый запрос дошёл до Paseo: %v", readErr)
	}
}

func newMutationTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "paseo")
	script := `#!/bin/sh
if [ -n "$FAKE_PASEO_RECORD" ]; then
  printf '%s\n' "$@" >> "$FAKE_PASEO_RECORD"
fi
if [ -n "$FAKE_PASEO_ENV_RECORD" ]; then
  printf 'PASEO_AGENT_ID=%s\n' "${PASEO_AGENT_ID-unset}" >> "$FAKE_PASEO_ENV_RECORD"
  printf 'PASEO_WORKSPACE_ID=%s\n' "${PASEO_WORKSPACE_ID-unset}" >> "$FAKE_PASEO_ENV_RECORD"
fi
if [ -n "$FAKE_PASEO_STDOUT" ]; then
  printf '%s' "$FAKE_PASEO_STDOUT"
fi
exit "${FAKE_PASEO_EXIT:-0}"
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("создать подменный paseo: %v", err)
	}
	t.Setenv("PATH", dir)

	adapter, err := NewWithConfig(RunnerConfig{
		Timeout:     time.Second,
		StdoutLimit: 64 << 10,
		StderrLimit: 64 << 10,
	})
	if err != nil {
		t.Fatalf("создать адаптер: %v", err)
	}
	return adapter
}
