package paseocli

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestАктивныйКонтрактPaseoЛокализованВоВнутреннемАдаптере(t *testing.T) {
	root := repositoryRoot(t)
	obsoleteHarness := filepath.Join(root, "internal", "testpaseo")
	if _, err := os.Stat(obsoleteHarness); err == nil {
		t.Fatalf("процессный стенд остался вне модуля Paseo: %s", obsoleteHarness)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("проверить прежнее расположение стенда: %v", err)
	}

	harnessRoot := filepath.Join(root, "internal", "paseo", "testpaseo")
	if info, err := os.Stat(harnessRoot); err != nil || !info.IsDir() {
		t.Fatalf("процессный стенд не находится внутри модуля Paseo: %s", harnessRoot)
	}

	contract := ActiveContract()
	mode, supported := contract.FullAccessMode("codex")
	if !supported {
		t.Fatal("активный контракт не содержит проверенный режим полного доступа")
	}
	forbiddenFragments := []string{
		contract.CLIVersion(),
		contract.DaemonVersion(),
		strconv.Quote(mode.provider),
		strconv.Quote(mode.id),
		strconv.Quote(mode.approvalPolicy),
		strconv.Quote(mode.sandbox),
		"paseo://",
	}

	adapterRoot := filepath.Join(root, "internal", "paseo", "internal", "paseocli")
	for _, sourceRoot := range []string{filepath.Join(root, "cmd"), filepath.Join(root, "internal")} {
		err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path == adapterRoot || path == harnessRoot {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, fragment := range forbiddenFragments {
				if strings.Contains(string(content), fragment) {
					t.Errorf("деталь активного контракта %q вышла за адаптер: %s", fragment, path)
				}
			}
			if containsPaseoCLIInvocation(t, path, content) {
				t.Errorf("аргументы команды Paseo вышли за активный адаптер: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("проверить locality Go-исходников: %v", err)
		}
	}
}

func containsPaseoCLIInvocation(t *testing.T, path string, content []byte) bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, content, 0)
	if err != nil {
		t.Fatalf("разобрать Go-файл %s: %v", path, err)
	}

	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !isStringSlice(literal.Type) {
			return true
		}
		arguments, ok := literalStrings(literal.Elts)
		if ok && isPaseoCLIArguments(arguments) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isStringSlice(expression ast.Expr) bool {
	array, ok := expression.(*ast.ArrayType)
	if !ok {
		return false
	}
	identifier, ok := array.Elt.(*ast.Ident)
	return ok && identifier.Name == "string"
}

func literalStrings(expressions []ast.Expr) ([]string, bool) {
	values := make([]string, 0, len(expressions))
	for _, expression := range expressions {
		literal, ok := expression.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return nil, false
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func isPaseoCLIArguments(arguments []string) bool {
	for _, prefix := range [][]string{
		{"provider", "ls"},
		{"provider", "models"},
		{"workspace", "ls"},
		{"workspace", "create"},
		{"ls", "--global"},
		{"inspect"},
		{"wait"},
		{"run", "--background"},
		{"archive"},
	} {
		if len(arguments) < len(prefix) {
			continue
		}
		matches := true
		for index := range prefix {
			if arguments[index] != prefix[index] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не определить путь теста locality")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
}
