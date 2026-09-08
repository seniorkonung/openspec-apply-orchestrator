package gitstate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestСостояниеGitРазличаетЧистоеДеревоИВидыРаботы(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		dirty   bool
	}{
		{
			name:    "чистое дерево",
			prepare: func(*testing.T, string) {},
		},
		{
			name: "изменения в индексе",
			prepare: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "staged.txt"), "изменение")
				runGit(t, root, "add", "staged.txt")
			},
			dirty: true,
		},
		{
			name: "изменённый отслеживаемый файл",
			prepare: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "tracked.txt"), "изменение")
			},
			dirty: true,
		},
		{
			name: "неотслеживаемый файл",
			prepare: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "untracked.txt"), "изменение")
			},
			dirty: true,
		},
		{
			name: "только игнорируемый файл",
			prepare: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".gitignore"), "ignored.txt\n")
				runGit(t, root, "add", ".gitignore")
				runGit(t, root, "commit", "-q", "-m", "test: add ignore rule")
				writeFile(t, filepath.Join(root, "ignored.txt"), "игнорируется")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := createRepository(t)
			tt.prepare(t, root)

			repository, err := Open(context.Background(), root)
			if err != nil {
				t.Fatalf("открыть репозиторий: %v", err)
			}
			state, err := repository.Read(context.Background())
			if err != nil {
				t.Fatalf("прочитать состояние Git: %v", err)
			}

			if tt.dirty {
				if _, ok := state.(Dirty); !ok {
					t.Fatalf("ожидалось грязное дерево, получено %T", state)
				}
				return
			}
			if _, ok := state.(Clean); !ok {
				t.Fatalf("ожидалось чистое дерево, получено %T", state)
			}
		})
	}
}

func TestКонфликтноеСостояниеСчитаетсяРаботой(t *testing.T) {
	root := createRepository(t)
	runGit(t, root, "switch", "-q", "-c", "side")
	writeFile(t, filepath.Join(root, "tracked.txt"), "ветка side")
	runGit(t, root, "commit", "-q", "-am", "test: change on side")
	runGit(t, root, "switch", "-q", "main")
	writeFile(t, filepath.Join(root, "tracked.txt"), "ветка main")
	runGit(t, root, "commit", "-q", "-am", "test: change on main")
	runGitMustFail(t, root, "merge", "side")

	repository, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("открыть репозиторий: %v", err)
	}
	state, err := repository.Read(context.Background())
	if err != nil {
		t.Fatalf("прочитать конфликтное состояние Git: %v", err)
	}
	if _, ok := state.(Dirty); !ok {
		t.Fatalf("конфликт должен считаться грязным деревом, получено %T", state)
	}
}

func TestКореньРабочегоДереваКанонизируетсяДляWorktreeИСимволическойСсылки(t *testing.T) {
	t.Run("связанный worktree имеет собственный корень", func(t *testing.T) {
		root := createRepository(t)
		worktree := filepath.Join(t.TempDir(), "linked")
		runGit(t, root, "worktree", "add", "-q", "-b", "linked", worktree)

		repository, err := Open(context.Background(), worktree)
		if err != nil {
			t.Fatalf("открыть связанный worktree: %v", err)
		}
		if repository.Root() != canonicalPath(t, worktree) {
			t.Fatalf("неожиданный корень worktree: %q", repository.Root())
		}
		state, err := repository.Read(context.Background())
		if err != nil {
			t.Fatalf("прочитать связанный worktree: %v", err)
		}
		if _, ok := state.(Clean); !ok {
			t.Fatalf("новый worktree должен быть чистым, получено %T", state)
		}
	})

	t.Run("символическая ссылка разрешается в настоящий корень", func(t *testing.T) {
		root := createRepository(t)
		link := filepath.Join(t.TempDir(), "repo-link")
		if err := os.Symlink(root, link); err != nil {
			t.Fatalf("создать символическую ссылку: %v", err)
		}

		repository, err := Open(context.Background(), link)
		if err != nil {
			t.Fatalf("открыть репозиторий по ссылке: %v", err)
		}
		if repository.Root() != canonicalPath(t, root) {
			t.Fatalf("неожиданный канонический корень: %q", repository.Root())
		}
	})
}

func TestОтсутствиеРепозиторияОтличаетсяОтЧистогоДерева(t *testing.T) {
	_, err := Open(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("ожидалась ошибка отсутствующего репозитория, получено %v", err)
	}
}

func TestОшибкаRevParseИНедостоверныйКореньНеСчитаютсяОтсутствиемРепозитория(t *testing.T) {
	t.Run("ошибка процесса остаётся ошибкой чтения", func(t *testing.T) {
		workingDirectory := t.TempDir()
		prepareFakeGit(t)
		t.Setenv("FAKE_GIT_ROOT", workingDirectory)
		t.Setenv("FAKE_GIT_REV_PARSE_EXIT", "7")

		_, err := Open(context.Background(), workingDirectory)
		if !errors.Is(err, ErrCommandExit) {
			t.Fatalf("ожидалась ошибка процесса, получено %v", err)
		}
		if errors.Is(err, ErrNotRepository) {
			t.Fatalf("ошибка чтения ошибочно классифицирована как отсутствие репозитория: %v", err)
		}
	})

	t.Run("чужой корень отклоняется как неожиданный вывод", func(t *testing.T) {
		workingDirectory := t.TempDir()
		foreignRoot := t.TempDir()
		prepareFakeGit(t)
		t.Setenv("FAKE_GIT_ROOT", foreignRoot)

		_, err := Open(context.Background(), workingDirectory)
		if !errors.Is(err, ErrUnexpectedOutput) {
			t.Fatalf("ожидалась ошибка недостоверного корня, получено %v", err)
		}
		if errors.Is(err, ErrNotRepository) {
			t.Fatalf("недостоверный корень ошибочно классифицирован как отсутствие репозитория: %v", err)
		}
	})
}

func TestСменаКаноническогоРабочегоКонтекстаОтклоняется(t *testing.T) {
	firstRoot := createRepository(t)
	secondRoot := createRepository(t)
	link := filepath.Join(t.TempDir(), "current-repository")
	if err := os.Symlink(firstRoot, link); err != nil {
		t.Fatalf("создать исходную символическую ссылку: %v", err)
	}
	repository, err := Open(context.Background(), link)
	if err != nil {
		t.Fatalf("открыть исходный репозиторий: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("удалить исходную символическую ссылку: %v", err)
	}
	if err := os.Symlink(secondRoot, link); err != nil {
		t.Fatalf("перенаправить символическую ссылку: %v", err)
	}

	state, err := repository.Read(context.Background())
	if !errors.Is(err, ErrWorkingContextChanged) {
		t.Fatalf("ожидалась ошибка смены рабочего контекста, получено state=%T err=%v", state, err)
	}
}

func TestАдаптерВыполняетТолькоЧитающиеКомандыБезShellИБлокировок(t *testing.T) {
	workingDirectory := filepath.Join(t.TempDir(), "repo-$(touch pwned)")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		t.Fatalf("создать рабочий каталог: %v", err)
	}
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	prepareFakeGit(t)
	t.Setenv("FAKE_GIT_ROOT", workingDirectory)
	t.Setenv("FAKE_GIT_RECORD", recordPath)
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")

	repository, err := Open(context.Background(), workingDirectory)
	if err != nil {
		t.Fatalf("открыть репозиторий: %v", err)
	}
	state, err := repository.Read(context.Background())
	if err != nil {
		t.Fatalf("прочитать состояние: %v", err)
	}
	if _, ok := state.(Clean); !ok {
		t.Fatalf("ожидалось чистое дерево, получено %T", state)
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, "pwned")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("рабочий путь был выполнен через shell")
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал Git: %v", err)
	}
	expected := strings.Join([]string{
		"locks=0", "rev-parse", "--show-toplevel",
		"locks=0", "rev-parse", "--show-toplevel",
		"locks=0", "status", "--porcelain=v2", "--untracked-files=normal",
		"",
	}, "\n")
	if string(recorded) != expected {
		t.Fatalf("неожиданные команды Git:\n%s", recorded)
	}
}

func TestОшибкаЧтенияТаймаутОтменаИПределыНеСчитаютсяЧистымДеревом(t *testing.T) {
	tests := []struct {
		name     string
		config   runnerConfig
		prepare  func(*testing.T)
		context  func() context.Context
		expected error
	}{
		{
			name:   "ненулевой код",
			config: defaultRunnerConfig(),
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_GIT_STATUS_EXIT", "7")
			},
			context:  context.Background,
			expected: ErrCommandExit,
		},
		{
			name:   "тайм-аут",
			config: runnerConfig{timeout: 20 * time.Millisecond, stdoutLimit: 1024, stderrLimit: 1024},
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_GIT_STATUS_SLEEP", "1")
			},
			context:  context.Background,
			expected: ErrCommandTimeout,
		},
		{
			name:   "отмена",
			config: defaultRunnerConfig(),
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_GIT_STATUS_SLEEP", "1")
			},
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			expected: ErrCommandCanceled,
		},
		{
			name:   "предел stdout",
			config: runnerConfig{timeout: time.Second, stdoutLimit: 256, stderrLimit: 1024},
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_GIT_STATUS", strings.Repeat("x", 512))
			},
			context:  context.Background,
			expected: ErrStdoutLimit,
		},
		{
			name:   "предел stderr",
			config: runnerConfig{timeout: time.Second, stdoutLimit: 1024, stderrLimit: 32},
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_GIT_STATUS_STDERR", strings.Repeat("x", 64))
			},
			context:  context.Background,
			expected: ErrStderrLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDirectory := t.TempDir()
			prepareFakeGit(t)
			t.Setenv("FAKE_GIT_ROOT", workingDirectory)
			repository, err := openWithConfig(context.Background(), workingDirectory, tt.config)
			if err != nil {
				t.Fatalf("открыть подменный репозиторий: %v", err)
			}
			tt.prepare(t)

			state, err := repository.Read(tt.context())
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено state=%T err=%v", tt.expected, state, err)
			}
			if state != nil {
				t.Fatalf("при ошибке не должно быть состояния, получено %T", state)
			}
		})
	}
}

func createRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	writeFile(t, filepath.Join(root, "tracked.txt"), "исходное содержимое")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-q", "-m", "test: initial state")
	return root
}

func runGit(t *testing.T, directory string, arguments ...string) []byte {
	t.Helper()
	command := exec.CommandContext(context.Background(), "git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return output
}

func runGitMustFail(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(context.Background(), "git", append([]string{"-C", directory}, arguments...)...)
	if err := command.Run(); err == nil {
		t.Fatalf("git %v должен завершиться неуспешно", arguments)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("записать %s: %v", path, err)
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("канонизировать %s: %v", path, err)
	}
	absolute, err := filepath.Abs(canonical)
	if err != nil {
		t.Fatalf("получить абсолютный путь %s: %v", canonical, err)
	}
	return filepath.Clean(absolute)
}

func prepareFakeGit(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "git")
	script := `#!/bin/sh
if [ -n "$FAKE_GIT_RECORD" ]; then
  printf 'locks=%s\n' "$GIT_OPTIONAL_LOCKS" >> "$FAKE_GIT_RECORD"
  printf '%s\n' "$@" >> "$FAKE_GIT_RECORD"
fi
case "$1" in
  rev-parse)
    printf '%s\n' "$FAKE_GIT_ROOT"
		exit "${FAKE_GIT_REV_PARSE_EXIT:-0}"
    ;;
  status)
    if [ -n "$FAKE_GIT_STATUS" ]; then
      printf '%s' "$FAKE_GIT_STATUS"
    fi
    if [ -n "$FAKE_GIT_STATUS_STDERR" ]; then
      printf '%s' "$FAKE_GIT_STATUS_STDERR" >&2
    fi
    if [ -n "$FAKE_GIT_STATUS_SLEEP" ]; then
      exec /bin/sleep "$FAKE_GIT_STATUS_SLEEP"
    fi
    exit "${FAKE_GIT_STATUS_EXIT:-0}"
    ;;
  *)
    exit 64
    ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("создать подменный Git: %v", err)
	}
	t.Setenv("PATH", directory)
}
