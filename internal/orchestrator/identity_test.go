package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestКлючПодготовкиКоммитовДетерминированПоКаноническомуКонтексту(t *testing.T) {
	root := t.TempDir()
	workingTree := filepath.Join(root, "working-tree")
	planningHome := filepath.Join(root, "planning-home")
	for _, path := range []string{workingTree, planningHome} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("создать каталог %q: %v", path, err)
		}
	}
	workingLink := filepath.Join(root, "working-link")
	planningLink := filepath.Join(root, "planning-link")
	if err := os.Symlink(workingTree, workingLink); err != nil {
		t.Fatalf("создать ссылку рабочего дерева: %v", err)
	}
	if err := os.Symlink(planningHome, planningLink); err != nil {
		t.Fatalf("создать ссылку planning home: %v", err)
	}

	direct, err := NewCommitPreparationChangeKey(CommitPreparationIdentity{
		WorkingTreeRoot:  workingTree,
		PlanningHomeRoot: planningHome,
		ChangeName:       "orchestrate-commit-preparation",
		ServerID:         "local-server",
	})
	if err != nil {
		t.Fatalf("создать ключ по прямым путям: %v", err)
	}
	linked, err := NewCommitPreparationChangeKey(CommitPreparationIdentity{
		WorkingTreeRoot:  workingLink,
		PlanningHomeRoot: planningLink,
		ChangeName:       "orchestrate-commit-preparation",
		ServerID:         "local-server",
	})
	if err != nil {
		t.Fatalf("создать ключ по символическим ссылкам: %v", err)
	}

	if direct != linked {
		t.Fatalf("канонически эквивалентные пути дали разные ключи: %q и %q", direct.String(), linked.String())
	}
	if !strings.HasPrefix(direct.String(), "change-v1-") || len(direct.String()) != len("change-v1-")+64 {
		t.Fatalf("ключ не имеет версионированную digest-форму: %q", direct.String())
	}
	if strings.Contains(direct.String(), workingTree) || strings.Contains(direct.String(), planningHome) {
		t.Fatalf("ключ раскрыл абсолютный путь: %q", direct.String())
	}
	if direct.ChangeName() != "orchestrate-commit-preparation" {
		t.Fatalf("идентичность потеряла имя выбранного change: %q", direct.ChangeName())
	}
}

func TestКаждыйПризнакМеняетКлючПодготовкиКоммитов(t *testing.T) {
	root := t.TempDir()
	workingTree := filepath.Join(root, "working-tree")
	otherWorkingTree := filepath.Join(root, "other-working-tree")
	planningHome := filepath.Join(root, "planning-home")
	otherPlanningHome := filepath.Join(root, "other-planning-home")
	for _, path := range []string{workingTree, otherWorkingTree, planningHome, otherPlanningHome} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("создать каталог %q: %v", path, err)
		}
	}

	base := CommitPreparationIdentity{
		WorkingTreeRoot:  workingTree,
		PlanningHomeRoot: planningHome,
		ChangeName:       "selected-change",
		ServerID:         "server-a",
	}
	want, err := NewCommitPreparationChangeKey(base)
	if err != nil {
		t.Fatalf("создать исходный ключ: %v", err)
	}

	variants := []struct {
		name     string
		identity CommitPreparationIdentity
	}{
		{name: "другое рабочее дерево", identity: CommitPreparationIdentity{otherWorkingTree, planningHome, "selected-change", "server-a"}},
		{name: "другой planning home", identity: CommitPreparationIdentity{workingTree, otherPlanningHome, "selected-change", "server-a"}},
		{name: "другой change", identity: CommitPreparationIdentity{workingTree, planningHome, "other-change", "server-a"}},
		{name: "другой server", identity: CommitPreparationIdentity{workingTree, planningHome, "selected-change", "server-b"}},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			got, err := NewCommitPreparationChangeKey(variant.identity)
			if err != nil {
				t.Fatalf("создать вариант ключа: %v", err)
			}
			if got == want {
				t.Fatalf("изменённый признак не изменил ключ %q", got.String())
			}
		})
	}
}

func TestНекорректнаяИдентичностьПодготовкиКоммитовОтклоняется(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("создать обычный файл: %v", err)
	}

	tests := []struct {
		name     string
		identity CommitPreparationIdentity
	}{
		{name: "пустой рабочий корень", identity: CommitPreparationIdentity{"", root, "change", "server"}},
		{name: "несуществующий planning home", identity: CommitPreparationIdentity{root, filepath.Join(root, "missing"), "change", "server"}},
		{name: "рабочий корень не каталог", identity: CommitPreparationIdentity{file, root, "change", "server"}},
		{name: "пустое имя change", identity: CommitPreparationIdentity{root, root, "", "server"}},
		{name: "пустой server ID", identity: CommitPreparationIdentity{root, root, "change", ""}},
		{name: "управляющий символ в server ID", identity: CommitPreparationIdentity{root, root, "change", "server\nsecret"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewCommitPreparationChangeKey(tt.identity); err == nil {
				t.Fatal("ожидалась ошибка идентичности")
			}
		})
	}
}
