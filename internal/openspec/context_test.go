package openspec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestРазрешениеRepoLocalChangeНеЗависитОтСхемыИПлана(t *testing.T) {
	changeName := "legacy_change"
	root, changeRoot := createOpenSpecRoot(t, changeName)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	client := newFakeClient(t, root, defaultRunnerConfig())
	t.Setenv("FAKE_OPENSPEC_RECORD", recordPath)
	t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, root, "nearest", "", nil))
	t.Setenv("FAKE_OPENSPEC_STATUS", statusJSON(t, root, changeRoot, changeName, "workflow-without-plan", nil))

	selection, err := NewSelection(changeName, "")
	if err != nil {
		t.Fatalf("создать выбор change: %v", err)
	}
	resolved, err := client.ResolveChange(context.Background(), selection)
	if err != nil {
		t.Fatalf("разрешить change: %v", err)
	}

	if resolved.Name() != changeName {
		t.Fatalf("неожиданное имя change: %q", resolved.Name())
	}
	if resolved.SchemaName() != "workflow-without-plan" {
		t.Fatalf("неожиданная схема: %q", resolved.SchemaName())
	}
	if resolved.PlanningHomeRoot() != root {
		t.Fatalf("неожиданный planning home: %q", resolved.PlanningHomeRoot())
	}
	if resolved.ChangeRoot() != changeRoot {
		t.Fatalf("неожиданный корень change: %q", resolved.ChangeRoot())
	}
	if storeID, selected := resolved.StoreID(); selected || storeID != "" {
		t.Fatalf("repo-local change неожиданно получил store %q", storeID)
	}
	if roots := resolved.AllowedEditRoots(); len(roots) != 1 || roots[0] != root {
		t.Fatalf("неожиданные разрешённые корни: %q", roots)
	}

	assertRecordedCommands(t, recordPath,
		"context\n--json\n",
		"status\n--change\n"+changeName+"\n--json\n",
	)
}

func TestЯвныйStoreСохраняетсяВоВсехВызовах(t *testing.T) {
	workingDirectory := t.TempDir()
	changeName := "prepare-commits"
	storeID := "platform-specs"
	storeRoot, changeRoot := createOpenSpecRoot(t, changeName)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	client := newFakeClient(t, workingDirectory, defaultRunnerConfig())
	t.Setenv("FAKE_OPENSPEC_RECORD", recordPath)
	t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, storeRoot, "store", storeID, nil))
	t.Setenv("FAKE_OPENSPEC_STATUS", statusJSONWithRoot(t, storeRoot, changeRoot, changeName, "spec-driven", "store", storeID, nil))

	selection, err := NewSelection(changeName, storeID)
	if err != nil {
		t.Fatalf("создать выбор change: %v", err)
	}
	resolved, err := client.ResolveChange(context.Background(), selection)
	if err != nil {
		t.Fatalf("разрешить change из store: %v", err)
	}
	if actual, selected := resolved.StoreID(); !selected || actual != storeID {
		t.Fatalf("ожидался store %q, получено %q, selected=%v", storeID, actual, selected)
	}

	assertRecordedCommands(t, recordPath,
		"context\n--json\n--store\n"+storeID+"\n",
		"status\n--change\n"+changeName+"\n--json\n--store\n"+storeID+"\n",
	)
}

func TestДиагностикаСвязанногоStoreНеСкрываетВыбранныйChange(t *testing.T) {
	changeName := "selected-change"
	root, changeRoot := createOpenSpecRoot(t, changeName)
	client := newFakeClient(t, root, defaultRunnerConfig())
	t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, root, "nearest", "", func(document map[string]any) {
		document["status"] = []map[string]any{
			{
				"severity": "warning",
				"code":     "relationship_registry_unreadable",
				"message":  "реестр связанных store недоступен",
				"fix":      "проверить реестр отдельно",
			},
		}
	}))
	t.Setenv("FAKE_OPENSPEC_STATUS", statusJSON(t, root, changeRoot, changeName, "spec-driven", nil))

	selection, err := NewSelection(changeName, "")
	if err != nil {
		t.Fatalf("создать выбор change: %v", err)
	}
	if _, err := client.ResolveChange(context.Background(), selection); err != nil {
		t.Fatalf("разрешить change при диагностике связанного store: %v", err)
	}
}

func TestОтсутствующийChangeОтличаетсяОтОшибкиПроцесса(t *testing.T) {
	root := t.TempDir()
	client := newFakeClient(t, root, defaultRunnerConfig())
	t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, root, "nearest", "", nil))
	t.Setenv("FAKE_OPENSPEC_STATUS", failureJSON(t, "change_error"))
	t.Setenv("FAKE_OPENSPEC_STATUS_EXIT", "1")

	selection, err := NewSelection("missing-change", "")
	if err != nil {
		t.Fatalf("создать выбор change: %v", err)
	}
	_, err = client.ResolveChange(context.Background(), selection)
	if !errors.Is(err, ErrChangeUnavailable) {
		t.Fatalf("ожидалась ошибка отсутствующего change, получено %v", err)
	}
	if errors.Is(err, ErrCommandExit) {
		t.Fatalf("отсутствующий change не должен выглядеть как общая ошибка процесса: %v", err)
	}
}

func TestПовреждённыйJSONИНесогласованныеКорниОтклоняются(t *testing.T) {
	t.Run("обрезанный JSON", func(t *testing.T) {
		root := t.TempDir()
		client := newFakeClient(t, root, defaultRunnerConfig())
		t.Setenv("FAKE_OPENSPEC_CONTEXT", `{"root":`)
		selection, err := NewSelection("selected-change", "")
		if err != nil {
			t.Fatalf("создать выбор change: %v", err)
		}
		_, err = client.ResolveChange(context.Background(), selection)
		if !errors.Is(err, ErrTruncatedJSON) {
			t.Fatalf("ожидался обрезанный JSON, получено %v", err)
		}
	})

	t.Run("неизвестное поле", func(t *testing.T) {
		root := t.TempDir()
		client := newFakeClient(t, root, defaultRunnerConfig())
		t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, root, "nearest", "", func(document map[string]any) {
			document["unexpected"] = "непроверенное значение"
		}))
		selection, err := NewSelection("selected-change", "")
		if err != nil {
			t.Fatalf("создать выбор change: %v", err)
		}
		_, err = client.ResolveChange(context.Background(), selection)
		if !errors.Is(err, ErrUnexpectedJSON) {
			t.Fatalf("ожидался неожиданный JSON, получено %v", err)
		}
		if strings.Contains(err.Error(), "непроверенное") {
			t.Fatalf("ошибка раскрыла непроверенный JSON: %v", err)
		}
	})

	t.Run("корни context и status различаются", func(t *testing.T) {
		changeName := "selected-change"
		root, _ := createOpenSpecRoot(t, changeName)
		otherRoot, otherChangeRoot := createOpenSpecRoot(t, changeName)
		client := newFakeClient(t, root, defaultRunnerConfig())
		t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, root, "nearest", "", nil))
		t.Setenv("FAKE_OPENSPEC_STATUS", statusJSON(t, otherRoot, otherChangeRoot, changeName, "spec-driven", nil))
		selection, err := NewSelection(changeName, "")
		if err != nil {
			t.Fatalf("создать выбор change: %v", err)
		}
		_, err = client.ResolveChange(context.Background(), selection)
		if !errors.Is(err, ErrInconsistentContext) {
			t.Fatalf("ожидались несогласованные корни, получено %v", err)
		}
	})

	t.Run("корень change не принадлежит changesDir", func(t *testing.T) {
		changeName := "selected-change"
		root, _ := createOpenSpecRoot(t, changeName)
		foreignChangeRoot := filepath.Join(t.TempDir(), changeName)
		if err := os.MkdirAll(foreignChangeRoot, 0o700); err != nil {
			t.Fatalf("создать чужой корень change: %v", err)
		}
		client := newFakeClient(t, root, defaultRunnerConfig())
		t.Setenv("FAKE_OPENSPEC_CONTEXT", contextJSON(t, root, "nearest", "", nil))
		t.Setenv("FAKE_OPENSPEC_STATUS", statusJSON(t, root, foreignChangeRoot, changeName, "spec-driven", nil))
		selection, err := NewSelection(changeName, "")
		if err != nil {
			t.Fatalf("создать выбор change: %v", err)
		}
		_, err = client.ResolveChange(context.Background(), selection)
		if !errors.Is(err, ErrInconsistentContext) {
			t.Fatalf("ожидался чужой корень change, получено %v", err)
		}
	})
}

func TestНекорректныйВыборОтклоняетсяДоВызоваCLI(t *testing.T) {
	for _, values := range []struct {
		change string
		store  string
	}{
		{change: "../escape"},
		{change: ".hidden"},
		{change: "archive"},
		{change: "valid-change", store: "Not Kebab"},
	} {
		if _, err := NewSelection(values.change, values.store); !errors.Is(err, ErrInvalidSelection) {
			t.Fatalf("выбор change=%q store=%q должен быть отклонён, получено %v", values.change, values.store, err)
		}
	}
}

func createOpenSpecRoot(t *testing.T, changeName string) (string, string) {
	t.Helper()
	root := t.TempDir()
	changeRoot := filepath.Join(root, "openspec", "changes", changeName)
	if err := os.MkdirAll(changeRoot, 0o700); err != nil {
		t.Fatalf("создать корень change: %v", err)
	}
	return root, changeRoot
}

func newFakeClient(t *testing.T, workingDirectory string, config runnerConfig) *Client {
	t.Helper()
	return newClient(newFakeRunner(t, workingDirectory, config))
}

func contextJSON(
	t *testing.T,
	root string,
	source string,
	storeID string,
	mutate func(map[string]any),
) string {
	t.Helper()
	rootObject := map[string]any{
		"path":   root,
		"source": source,
		"role":   "openspec_root",
	}
	if storeID != "" {
		rootObject["store_id"] = storeID
	}
	document := map[string]any{
		"root":    rootObject,
		"members": []any{},
		"status":  []any{},
	}
	if mutate != nil {
		mutate(document)
	}
	return encodeJSON(t, document)
}

func statusJSON(
	t *testing.T,
	root string,
	changeRoot string,
	changeName string,
	schemaName string,
	mutate func(map[string]any),
) string {
	t.Helper()
	return statusJSONWithRoot(t, root, changeRoot, changeName, schemaName, "nearest", "", mutate)
}

func statusJSONWithRoot(
	t *testing.T,
	root string,
	changeRoot string,
	changeName string,
	schemaName string,
	source string,
	storeID string,
	mutate func(map[string]any),
) string {
	t.Helper()
	rootObject := map[string]any{"path": root, "source": source}
	if storeID != "" {
		rootObject["store_id"] = storeID
	}
	document := map[string]any{
		"changeName": changeName,
		"schemaName": schemaName,
		"planningHome": map[string]any{
			"kind":          "repo",
			"root":          root,
			"changesDir":    filepath.Join(root, "openspec", "changes"),
			"defaultSchema": "spec-driven",
		},
		"changeRoot":         changeRoot,
		"artifactPaths":      map[string]any{},
		"isPlanningComplete": true,
		"isComplete":         true,
		"applyRequires":      []any{},
		"nextSteps":          []any{},
		"actionContext": map[string]any{
			"mode":                          "repo-local",
			"sourceOfTruth":                 "repo",
			"planningArtifacts":             []any{},
			"linkedContext":                 []any{},
			"allowedEditRoots":              []any{root},
			"requiresAffectedAreaSelection": false,
			"constraints":                   []any{},
		},
		"artifacts": []any{},
		"root":      rootObject,
	}
	if mutate != nil {
		mutate(document)
	}
	return encodeJSON(t, document)
}

func failureJSON(t *testing.T, code string) string {
	t.Helper()
	return encodeJSON(t, map[string]any{
		"status": []map[string]any{
			{
				"severity": "error",
				"code":     code,
				"message":  "Change не найден.\nДоступные change перечислены ниже.",
			},
		},
	})
}

func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("собрать JSON: %v", err)
	}
	return string(encoded)
}
