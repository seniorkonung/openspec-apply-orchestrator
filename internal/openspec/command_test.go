package openspec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestКомандаПередаётАргументыБезShell(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	workingDirectory := t.TempDir()
	runner := newFakeRunner(t, workingDirectory, defaultRunnerConfig())
	t.Setenv("FAKE_OPENSPEC_RECORD", recordPath)
	t.Setenv("FAKE_OPENSPEC_CONTEXT", `{}`)
	argument := "change-$(touch pwned)"

	result, err := runner.run(context.Background(), command{
		name: "context",
		args: []string{"context", "--change", argument, "--json"},
	})
	if err != nil {
		t.Fatalf("выполнить команду: %v", err)
	}
	if string(result.stdout) != `{}` {
		t.Fatalf("неожиданный stdout: %q", result.stdout)
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, "pwned")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("аргумент был выполнен через shell")
	}
	assertRecordedCommands(t, recordPath, "context\n--change\n"+argument+"\n--json\n")
}

func TestОшибкиПроцессаТаймаутИПределВыводаРазличаются(t *testing.T) {
	tests := []struct {
		name     string
		config   runnerConfig
		prepare  func(*testing.T)
		expected error
	}{
		{
			name:   "ненулевой код",
			config: defaultRunnerConfig(),
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_OPENSPEC_CONTEXT", `{}`)
				t.Setenv("FAKE_OPENSPEC_CONTEXT_EXIT", "7")
			},
			expected: ErrCommandExit,
		},
		{
			name:   "тайм-аут",
			config: runnerConfig{timeout: 20 * time.Millisecond, stdoutLimit: 1024, stderrLimit: 1024},
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_OPENSPEC_SLEEP", "1")
			},
			expected: ErrCommandTimeout,
		},
		{
			name:   "предел stdout",
			config: runnerConfig{timeout: time.Second, stdoutLimit: 32, stderrLimit: 1024},
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_OPENSPEC_CONTEXT", strings.Repeat("x", 64))
			},
			expected: ErrStdoutLimit,
		},
		{
			name:   "предел stderr",
			config: runnerConfig{timeout: time.Second, stdoutLimit: 1024, stderrLimit: 32},
			prepare: func(t *testing.T) {
				t.Setenv("FAKE_OPENSPEC_STDERR", strings.Repeat("x", 64))
			},
			expected: ErrStderrLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newFakeRunner(t, t.TempDir(), tt.config)
			tt.prepare(t)
			_, err := runner.run(context.Background(), command{name: "context", args: []string{"context", "--json"}})
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.expected, err)
			}
		})
	}
}

func TestНедоступныйOpenSpecОтклоняетсяДоВызоваКоманды(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := newRunner(t.TempDir(), defaultRunnerConfig())
	if !errors.Is(err, ErrExecutableNotFound) {
		t.Fatalf("ожидалась ошибка поиска openspec, получено %v", err)
	}
}

func newFakeRunner(t *testing.T, workingDirectory string, config runnerConfig) *runner {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "openspec")
	script := `#!/bin/sh
if [ -n "$FAKE_OPENSPEC_RECORD" ]; then
  printf '%s\n' "$@" >> "$FAKE_OPENSPEC_RECORD"
fi
case "$1" in
  context)
    stdout="$FAKE_OPENSPEC_CONTEXT"
    exit_code="${FAKE_OPENSPEC_CONTEXT_EXIT:-0}"
    ;;
  status)
    stdout="$FAKE_OPENSPEC_STATUS"
    exit_code="${FAKE_OPENSPEC_STATUS_EXIT:-0}"
    ;;
  *)
    stdout=""
    exit_code=64
    ;;
esac
if [ -n "$stdout" ]; then
  printf '%s' "$stdout"
fi
if [ -n "$FAKE_OPENSPEC_STDERR" ]; then
  printf '%s' "$FAKE_OPENSPEC_STDERR" >&2
fi
if [ -n "$FAKE_OPENSPEC_SLEEP" ]; then
  exec /bin/sleep "$FAKE_OPENSPEC_SLEEP"
fi
exit "$exit_code"
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("создать подменный openspec: %v", err)
	}
	t.Setenv("PATH", directory)

	runner, err := newRunner(workingDirectory, config)
	if err != nil {
		t.Fatalf("создать исполнитель OpenSpec: %v", err)
	}
	return runner
}

func assertRecordedCommands(t *testing.T, path string, expected ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(data) != strings.Join(expected, "") {
		t.Fatalf("неожиданные вызовы:\n%s", data)
	}
}
