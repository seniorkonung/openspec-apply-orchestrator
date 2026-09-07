package paseo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

func TestСлужебноеИмяWorkspaceОпределяетсяDigestКлючаChange(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")

	got := managedWorkspaceName(change)
	want := "oa-v1-d2d0663f369fabec5f892e5375cb00247316a2a3956a9f32c53ee4c87dee4860"
	if got != want {
		t.Fatalf("неожиданное служебное имя workspace: %q", got)
	}
	if got == managedWorkspaceName(mustDirectoryChangeKey(t, "другой-change")) {
		t.Fatal("разные ключи change получили одинаковое служебное имя workspace")
	}
}

func TestАктивныйWorkspaceНаходитсяПоИмениИCanonicalCWD(t *testing.T) {
	realCWD := t.TempDir()
	linkedRoot := t.TempDir()
	linkedCWD := filepath.Join(linkedRoot, "repo-link")
	if err := os.Symlink(realCWD, linkedCWD); err != nil {
		t.Fatalf("создать символическую ссылку: %v", err)
	}

	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_WORKSPACES", encodeDirectoryJSON(t, []map[string]any{
		workspaceListItem("workspace-other", "ручной", realCWD),
		workspaceListItem("workspace-1", managedWorkspaceName(change), realCWD),
	}))

	observation, err := client.FindActiveWorkspace(context.Background(), change, linkedCWD)
	if err != nil {
		t.Fatalf("найти активный workspace: %v", err)
	}
	found, ok := observation.(OneActiveWorkspace)
	if !ok {
		t.Fatalf("ожидался один активный workspace, получено %T", observation)
	}
	if found.Workspace.ID().String() != "workspace-1" {
		t.Fatalf("неожиданный workspace: %q", found.Workspace.ID().String())
	}
	if found.Workspace.CWD() != realCWD {
		t.Fatalf("cwd не канонизирован: %q", found.Workspace.CWD())
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(recorded) != "workspace\nls\n--json\n" {
		t.Fatalf("ожидался только список активных workspace, получено %q", recorded)
	}
}

func TestОтсутствиеИНесколькоWorkspaceРазличаются(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	cwd := t.TempDir()

	t.Run("подходящий workspace отсутствует", func(t *testing.T) {
		client := newFakeClient(t)
		t.Setenv("FAKE_PASEO_WORKSPACES", encodeDirectoryJSON(t, []map[string]any{
			workspaceListItem("workspace-other", "ручной", cwd),
		}))

		observation, err := client.FindActiveWorkspace(context.Background(), change, cwd)
		if err != nil {
			t.Fatalf("найти активный workspace: %v", err)
		}
		if _, ok := observation.(NoActiveWorkspace); !ok {
			t.Fatalf("ожидалось отсутствие workspace, получено %T", observation)
		}
	})

	t.Run("два подходящих workspace неоднозначны", func(t *testing.T) {
		client := newFakeClient(t)
		name := managedWorkspaceName(change)
		t.Setenv("FAKE_PASEO_WORKSPACES", encodeDirectoryJSON(t, []map[string]any{
			workspaceListItem("workspace-1", name, cwd),
			workspaceListItem("workspace-2", name, cwd),
		}))

		observation, err := client.FindActiveWorkspace(context.Background(), change, cwd)
		if err != nil {
			t.Fatalf("найти активный workspace: %v", err)
		}
		ambiguous, ok := observation.(AmbiguousActiveWorkspaces)
		if !ok {
			t.Fatalf("ожидалась неоднозначность workspace, получено %T", observation)
		}
		if len(ambiguous.Workspaces) != 2 {
			t.Fatalf("ожидались два workspace, получено %d", len(ambiguous.Workspaces))
		}
	})
}

func TestНекорректныйJSONWorkspaceНеОзначаетОтсутствие(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	cwd := t.TempDir()

	for _, output := range []string{
		`[{"workspaceId":"workspace-1"}]`,
		`[{"workspaceId":"workspace-1","project":"repo","name":"name","isolation":"local","cwd":"/repo","extra":true}]`,
		`[{"workspaceId":"workspace-1"`,
	} {
		client := newFakeClient(t)
		t.Setenv("FAKE_PASEO_WORKSPACES", output)

		_, err := client.FindActiveWorkspace(context.Background(), change, cwd)
		if !errors.Is(err, ErrUnexpectedJSON) && !errors.Is(err, ErrTruncatedJSON) {
			t.Fatalf("ожидалась ошибка JSON workspace, получено %v", err)
		}
	}
}

func workspaceListItem(id, name, cwd string) map[string]any {
	return map[string]any{
		"workspaceId": id,
		"project":     "репозиторий",
		"name":        name,
		"isolation":   "local",
		"cwd":         cwd,
	}
}

func encodeDirectoryJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("собрать JSON каталога Paseo: %v", err)
	}
	return string(encoded)
}

func mustDirectoryChangeKey(t *testing.T, value string) orchestrator.ChangeKey {
	t.Helper()
	key, err := orchestrator.NewChangeKey(value)
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	return key
}
