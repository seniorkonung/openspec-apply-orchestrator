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

func TestWorkspaceСоздаётсяСлужебнымИменемИКаноническимCWD(t *testing.T) {
	realCWD := t.TempDir()
	linkedRoot := t.TempDir()
	linkedCWD := filepath.Join(linkedRoot, "repo-link")
	if err := os.Symlink(realCWD, linkedCWD); err != nil {
		t.Fatalf("создать символическую ссылку: %v", err)
	}

	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	environment := compatibleTestEnvironment()
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_WORKSPACE_CREATE", encodeDirectoryJSON(t,
		workspaceListItem("workspace-created", managedWorkspaceName(change), realCWD),
	))

	workspace, err := client.CreateWorkspace(context.Background(), environment, change, linkedCWD)
	if err != nil {
		t.Fatalf("создать workspace: %v", err)
	}
	if workspace.ID().String() != "workspace-created" || workspace.Name() != managedWorkspaceName(change) {
		t.Fatalf("неожиданный workspace: id=%q name=%q", workspace.ID(), workspace.Name())
	}
	if workspace.CWD() != realCWD {
		t.Fatalf("cwd не канонизирован: %q", workspace.CWD())
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	want := strings.Join([]string{
		"workspace", "create",
		"--isolation", "local",
		"--path", realCWD,
		"--title", managedWorkspaceName(change),
		"--json",
	}, "\n") + "\n"
	if string(recorded) != want {
		t.Fatalf("неожиданные аргументы создания workspace:\n%s", recorded)
	}
}

func TestСобственнаяСессияСоздаётсяОднойФоновойКомандой(t *testing.T) {
	cwd := t.TempDir()
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := ActiveWorkspace{
		id:   mustMutationWorkspaceID(t, "workspace-1"),
		name: managedWorkspaceName(change),
		cwd:  cwd,
	}
	settings, err := NewSessionSettings("codex", "gpt-5.6", "high", "default")
	if err != nil {
		t.Fatalf("создать настройки сессии: %v", err)
	}

	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	environmentPath := filepath.Join(t.TempDir(), "окружение")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_ENV_RECORD", environmentPath)
	t.Setenv("PASEO_AGENT_ID", "чужой-родитель")
	t.Setenv("PASEO_WORKSPACE_ID", "чужой-workspace")
	t.Setenv("FAKE_PASEO_RUN", encodeDirectoryJSON(t, map[string]any{
		"agentId":  "agent-created",
		"status":   "running",
		"provider": "codex",
		"cwd":      cwd,
		"title":    "подготовка",
	}))
	prompt := "Проверить механизм без изменения репозитория."

	sessionID, err := client.CreateOwnSession(
		context.Background(), compatibleTestEnvironment(), change, workspace, settings, prompt,
	)
	if err != nil {
		t.Fatalf("создать собственную сессию: %v", err)
	}
	if sessionID.String() != "agent-created" {
		t.Fatalf("неожиданный идентификатор сессии: %q", sessionID)
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
		"--mode", "default",
		"--label", "oa.owner=openspec-apply-orchestrator",
		"--label", "oa.version=1",
		"--label", "oa.change=orchestrate-commit-preparation",
		"--label", "oa.kind=commit-preparation",
		"--label", "oa.workspace=workspace-1",
		"--json", "--", prompt,
	}, "\n") + "\n"
	if string(recorded) != want {
		t.Fatalf("неожиданные аргументы создания сессии:\n%s", recorded)
	}

	processEnvironment, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatalf("прочитать окружение Paseo: %v", err)
	}
	if string(processEnvironment) != "PASEO_AGENT_ID=unset\nPASEO_WORKSPACE_ID=unset\n" {
		t.Fatalf("контекст чужой сессии передан Paseo:\n%s", processEnvironment)
	}
}

func TestПотерянныйОтветRunДаётНеопределённыйИсходБезПовтора(t *testing.T) {
	cwd := t.TempDir()
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := ActiveWorkspace{
		id:   mustMutationWorkspaceID(t, "workspace-1"),
		name: managedWorkspaceName(change),
		cwd:  cwd,
	}
	settings, err := NewSessionSettings("codex", "gpt-5.6", "", "")
	if err != nil {
		t.Fatalf("создать настройки сессии: %v", err)
	}
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_RUN", "")

	_, err = client.CreateOwnSession(
		context.Background(), compatibleTestEnvironment(), change, workspace, settings, "Проверить механизм.",
	)
	if !errors.Is(err, ErrRunOutcomeUnknown) {
		t.Fatalf("ожидался неопределённый исход run, получено %v", err)
	}
	if !errors.Is(err, ErrEmptyOutput) {
		t.Fatalf("причина потери ответа не сохранена: %v", err)
	}

	recorded, readErr := os.ReadFile(recordPath)
	if readErr != nil {
		t.Fatalf("прочитать журнал вызовов: %v", readErr)
	}
	if strings.Count(string(recorded), "run\n") != 1 {
		t.Fatalf("run должен вызываться ровно один раз:\n%s", recorded)
	}
}

func compatibleTestEnvironment() CompatibleEnvironment {
	return CompatibleEnvironment{
		serverID: ServerID{value: "server-1"},
		version:  Version{value: compatiblePaseoVersion},
	}
}

func mustMutationWorkspaceID(t *testing.T, value string) orchestrator.WorkspaceID {
	t.Helper()
	id, err := orchestrator.NewWorkspaceID(value)
	if err != nil {
		t.Fatalf("создать идентификатор workspace: %v", err)
	}
	return id
}
