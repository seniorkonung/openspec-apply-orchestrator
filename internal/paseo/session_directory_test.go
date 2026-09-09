package paseo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

func TestСобственнаяСессияНаходитсяШирокимИТочнымФильтром(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_LS_BROAD", encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-123", "рабочий агент", "running", cwd),
	}))
	t.Setenv("FAKE_PASEO_LS_EXACT", encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-123", "любое название", "running", cwd),
	}))
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-123", "running", cwd)))

	observation, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
	if err != nil {
		t.Fatalf("найти собственную сессию: %v", err)
	}
	working, ok := observation.(orchestrator.WorkingOwnSession)
	if !ok {
		t.Fatalf("ожидалась работающая собственная сессия, получено %T", observation)
	}
	if working.Session.ID().String() != "agent-123" {
		t.Fatalf("неожиданная сессия: %q", working.Session.ID().String())
	}
	if working.Session.WorkspaceID() != workspace {
		t.Fatalf("сессия связана с неожиданным workspace: %q", working.Session.WorkspaceID().String())
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	want := strings.Join([]string{
		"ls", "--global",
		"--label", "oa.owner=openspec-apply-orchestrator",
		"--label", "oa.change=orchestrate-commit-preparation",
		"--json",
		"ls", "--global",
		"--label", "oa.owner=openspec-apply-orchestrator",
		"--label", "oa.change=orchestrate-commit-preparation",
		"--label", "oa.version=1",
		"--label", "oa.kind=commit-preparation",
		"--label", "oa.workspace=workspace-1",
		"--json",
		"inspect", "agent-123", "--json",
	}, "\n") + "\n"
	if string(recorded) != want {
		t.Fatalf("неожиданная последовательность команд:\n%s", recorded)
	}
	if strings.Contains(string(recorded), "--all") {
		t.Fatal("каталог сессий запросил архивированную историю")
	}
}

func TestРучныеИЧужиеСессииНеСтановятсяСобственными(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := newFakeClient(t)
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")

	observation, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
	if err != nil {
		t.Fatalf("найти собственные сессии: %v", err)
	}
	if _, ok := observation.(orchestrator.NoActiveOwnSession); !ok {
		t.Fatalf("ожидалось отсутствие собственной сессии, получено %T", observation)
	}
}

func TestИзвестнаяСобственнаяСессияОстаётсяЕдинственнойЦельюНаблюдения(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := clientWithOneExactSession(t, cwd, "running")
	recordPath := filepath.Join(t.TempDir(), "команды")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-123", "running", cwd)))

	observation, err := client.ObserveOwnSession(
		context.Background(), change, workspace, cwd, mustSessionID(t, "agent-123"),
	)
	if err != nil {
		t.Fatalf("наблюдать известную сессию: %v", err)
	}
	working, ok := observation.(orchestrator.WorkingOwnSession)
	if !ok || working.Session.ID().String() != "agent-123" {
		t.Fatalf("ожидалась работа известной сессии, получено %#v", observation)
	}

	recorded := readRecordedCalls(t, recordPath)
	if strings.Count(recorded, "ls\n--global\n") != 2 ||
		!strings.Contains(recorded, "inspect\nagent-123\n--json\n") {
		t.Fatalf("ожидались свежие фильтры и inspect известной цели:\n%s", recorded)
	}
}

func TestИсчезновениеИзАктивныхФильтровПодтверждаетсяInspectИзвестнойСессии(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "команды")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")
	archived := agentInspection("agent-known", "idle", cwd)
	archived["Archived"] = true
	archived["ArchivedAt"] = "2026-09-08T12:00:00Z"
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, archived))

	observation, err := client.ObserveOwnSession(
		context.Background(), change, workspace, cwd, mustSessionID(t, "agent-known"),
	)
	if err != nil {
		t.Fatalf("подтвердить закрытие известной сессии: %v", err)
	}
	closed, ok := observation.(orchestrator.ObservedOwnSessionClosed)
	if !ok || closed.Session.ID().String() != "agent-known" {
		t.Fatalf("ожидалось подтверждённое закрытие известной сессии, получено %#v", observation)
	}

	recorded := readRecordedCalls(t, recordPath)
	if !strings.Contains(recorded, "inspect\nagent-known\n--json\n") {
		t.Fatalf("исчезновение не подтверждено через inspect известной цели:\n%s", recorded)
	}
}

func TestПротиворечиеФильтровИInspectНеСчитаетсяЗакрытием(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := newFakeClient(t)
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-known", "running", cwd)))

	_, err := client.ObserveOwnSession(
		context.Background(), change, workspace, cwd, mustSessionID(t, "agent-known"),
	)
	if !errors.Is(err, ErrCorruptSessionOwnership) {
		t.Fatalf("противоречие не должно считаться закрытием, получено %v", err)
	}
}

func TestИзвестнаяСессияНеПодменяетсяДругимАктивнымID(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := clientWithOneExactSession(t, cwd, "running")
	recordPath := filepath.Join(t.TempDir(), "команды")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)

	_, err := client.ObserveOwnSession(
		context.Background(), change, workspace, cwd, mustSessionID(t, "agent-known"),
	)
	if !errors.Is(err, ErrSessionIdentityMismatch) {
		t.Fatalf("ожидалась ошибка смены ID, получено %v", err)
	}
	if recorded := readRecordedCalls(t, recordPath); strings.Contains(recorded, "inspect\n") {
		t.Fatalf("адаптер не должен inspect чужой цели после смены ID:\n%s", recorded)
	}
}

func TestНовыйПроцессНеЧитаетАрхивнуюИсториюБезИзвестногоID(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "команды")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")
	t.Setenv("FAKE_PASEO_INSPECT", "архивная история не должна читаться")

	observation, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
	if err != nil {
		t.Fatalf("найти активные сессии нового процесса: %v", err)
	}
	if _, ok := observation.(orchestrator.NoActiveOwnSession); !ok {
		t.Fatalf("ожидалось отсутствие активной сессии, получено %T", observation)
	}
	if recorded := readRecordedCalls(t, recordPath); strings.Contains(recorded, "inspect\n") {
		t.Fatalf("новый процесс прочитал архивную историю:\n%s", recorded)
	}
}

func TestРазницаШирокогоИТочногоНабораОзначаетПовреждённуюПринадлежность(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	client := newFakeClient(t)
	t.Setenv("FAKE_PASEO_LS_BROAD", encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-other-workspace", "агент", "idle", cwd),
	}))
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")

	_, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
	if !errors.Is(err, ErrCorruptSessionOwnership) {
		t.Fatalf("ожидалась ошибка повреждённой принадлежности, получено %v", err)
	}
}

func TestДвеТочныеСессииНеВыбираютсяПоНазванию(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	agents := []map[string]any{
		agentListItem("agent-1", "поздняя", "running", cwd),
		agentListItem("agent-2", "ранняя", "idle", cwd),
	}
	client := newFakeClient(t)
	t.Setenv("FAKE_PASEO_LS_BROAD", encodeDirectoryJSON(t, agents))
	t.Setenv("FAKE_PASEO_LS_EXACT", encodeDirectoryJSON(t, agents))

	observation, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
	if err != nil {
		t.Fatalf("найти собственные сессии: %v", err)
	}
	ambiguous, ok := observation.(orchestrator.AmbiguousOwnSessions)
	if !ok {
		t.Fatalf("ожидалась неоднозначность сессий, получено %T", observation)
	}
	if len(ambiguous.Sessions) != 2 {
		t.Fatalf("ожидались две сессии, получено %d", len(ambiguous.Sessions))
	}
}

func TestInspectПодтверждаетИдентичностьCWDИОтсутствиеЧужогоРодителя(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	otherCWD := t.TempDir()

	tests := []struct {
		name     string
		mutate   func(map[string]any)
		expected error
	}{
		{
			name: "inspect вернул другой полный ID",
			mutate: func(inspect map[string]any) {
				inspect["Id"] = "agent-other"
			},
			expected: ErrSessionIdentityMismatch,
		},
		{
			name: "inspect вернул другой cwd",
			mutate: func(inspect map[string]any) {
				inspect["Cwd"] = otherCWD
			},
			expected: ErrSessionWorkingDirectoryMismatch,
		},
		{
			name: "inspect обнаружил внутреннего субагента",
			mutate: func(inspect map[string]any) {
				inspect["ParentAgentId"] = "agent-parent"
			},
			expected: ErrForeignSessionParent,
		},
		{
			name: "inspect содержит неизвестное поле",
			mutate: func(inspect map[string]any) {
				inspect["Token"] = "секрет"
			},
			expected: ErrUnexpectedJSON,
		},
		{
			name: "архивированная сессия одновременно работает",
			mutate: func(inspect map[string]any) {
				inspect["Archived"] = true
				inspect["ArchivedAt"] = "2026-09-07T09:20:00Z"
			},
			expected: ErrUnexpectedJSON,
		},
		{
			name: "архивированная сессия одновременно ожидает разрешение",
			mutate: func(inspect map[string]any) {
				inspect["Status"] = "idle"
				inspect["Archived"] = true
				inspect["ArchivedAt"] = "2026-09-07T09:20:00Z"
				inspect["PendingPermissions"] = []map[string]any{{"id": "permission-1", "tool": "Bash"}}
			},
			expected: ErrUnexpectedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := clientWithOneExactSession(t, cwd, "running")
			inspect := agentInspection("agent-123", "running", cwd)
			tt.mutate(inspect)
			t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, inspect))

			_, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.expected, err)
			}
			if err != nil && strings.Contains(err.Error(), "секрет") {
				t.Fatalf("ошибка раскрыла непроверенный JSON: %v", err)
			}
		})
	}
}

func TestInspectРазличаетРаботуОжиданиеИЗакрытие(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()

	tests := []struct {
		name   string
		status string
		mutate func(map[string]any)
		assert func(*testing.T, orchestrator.OwnSessionObservation)
	}{
		{
			name:   "работа",
			status: "running",
			assert: func(t *testing.T, got orchestrator.OwnSessionObservation) {
				if _, ok := got.(orchestrator.WorkingOwnSession); !ok {
					t.Fatalf("ожидалась работа, получено %T", got)
				}
			},
		},
		{
			name:   "ожидание после завершённого хода",
			status: "idle",
			assert: func(t *testing.T, got orchestrator.OwnSessionObservation) {
				waiting, ok := got.(orchestrator.OwnSessionAwaitingAction)
				if !ok {
					t.Fatalf("ожидалось действие, получено %T", got)
				}
				if waiting.Reason != orchestrator.SessionTurnFinished {
					t.Fatalf("неожиданная причина ожидания: %v", waiting.Reason)
				}
			},
		},
		{
			name:   "запрос разрешения",
			status: "running",
			mutate: func(inspect map[string]any) {
				inspect["PendingPermissions"] = []map[string]any{{"id": "permission-1", "tool": "Bash"}}
			},
			assert: func(t *testing.T, got orchestrator.OwnSessionObservation) {
				waiting, ok := got.(orchestrator.OwnSessionAwaitingAction)
				if !ok {
					t.Fatalf("ожидалось разрешение, получено %T", got)
				}
				if waiting.Reason != orchestrator.SessionPermissionRequested {
					t.Fatalf("неожиданная причина ожидания: %v", waiting.Reason)
				}
			},
		},
		{
			name:   "ошибка агента",
			status: "error",
			assert: func(t *testing.T, got orchestrator.OwnSessionObservation) {
				waiting, ok := got.(orchestrator.OwnSessionAwaitingAction)
				if !ok {
					t.Fatalf("ожидалось участие после ошибки, получено %T", got)
				}
				if waiting.Reason != orchestrator.SessionAgentError {
					t.Fatalf("неожиданная причина ожидания: %v", waiting.Reason)
				}
			},
		},
		{
			name:   "архивирование между ls и inspect",
			status: "idle",
			mutate: func(inspect map[string]any) {
				inspect["Archived"] = true
				inspect["ArchivedAt"] = "2026-09-07T09:20:00Z"
			},
			assert: func(t *testing.T, got orchestrator.OwnSessionObservation) {
				if _, ok := got.(orchestrator.ObservedOwnSessionClosed); !ok {
					t.Fatalf("ожидалось закрытие, получено %T", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := clientWithOneExactSession(t, cwd, tt.status)
			inspect := agentInspection("agent-123", tt.status, cwd)
			if tt.mutate != nil {
				tt.mutate(inspect)
			}
			t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, inspect))

			observation, err := client.FindOwnSessions(context.Background(), change, workspace, cwd)
			if err != nil {
				t.Fatalf("найти собственную сессию: %v", err)
			}
			tt.assert(t, observation)
		})
	}
}

func clientWithOneExactSession(t *testing.T, cwd, status string) *Client {
	t.Helper()
	client := newFakeClient(t)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-123", "агент", status, cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	return client
}

func agentListItem(id, name, status, cwd string) map[string]any {
	shortID := id
	if len(shortID) > 7 {
		shortID = shortID[:7]
	}
	return map[string]any{
		"id":       id,
		"shortId":  shortID,
		"name":     name,
		"provider": "codex/gpt-5",
		"thinking": "high",
		"status":   status,
		"cwd":      cwd,
		"created":  "just now",
	}
}

func agentInspection(id, status, cwd string) map[string]any {
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

func mustDirectoryWorkspaceID(t *testing.T, value string) orchestrator.WorkspaceID {
	t.Helper()
	id, err := orchestrator.NewWorkspaceID(value)
	if err != nil {
		t.Fatalf("создать идентификатор workspace: %v", err)
	}
	return id
}
