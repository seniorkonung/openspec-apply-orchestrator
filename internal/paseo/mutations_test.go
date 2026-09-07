package paseo

import (
	"context"
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
