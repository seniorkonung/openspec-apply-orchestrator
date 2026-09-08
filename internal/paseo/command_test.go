package paseo

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
	recordPath := filepath.Join(t.TempDir(), "аргументы")
	runner := newFakeRunner(t, runnerConfig{
		timeout:     time.Second,
		stdoutLimit: 1024,
		stderrLimit: 1024,
	})
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STDOUT", `{"ok":true}`)

	argument := `$(touch НЕ_ВЫПОЛНЯТЬ); значение с пробелами`
	stdout, err := runner.run(context.Background(), command{
		name: "status",
		args: []string{"status", "--json", argument},
	})
	if err != nil {
		t.Fatalf("выполнить команду: %v", err)
	}
	if string(stdout) != `{"ok":true}` {
		t.Fatalf("неожиданный stdout: %q", stdout)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать аргументы: %v", err)
	}
	if string(recorded) != "status\n--json\n"+argument+"\n" {
		t.Fatalf("аргументы изменены: %q", recorded)
	}
}

func TestНеуспешноеЗавершениеКомандыИмеетТипизированнуюОшибку(t *testing.T) {
	runner := newFakeRunner(t, runnerConfig{
		timeout:     time.Second,
		stdoutLimit: 1024,
		stderrLimit: 1024,
	})
	t.Setenv("FAKE_PASEO_STDOUT", "секретный промпт")
	t.Setenv("FAKE_PASEO_STDERR", "секретный токен")
	t.Setenv("FAKE_PASEO_EXIT", "17")

	_, err := runner.run(context.Background(), command{name: "status", args: []string{"status", "--json"}})
	if !errors.Is(err, ErrCommandExit) {
		t.Fatalf("ожидалась ошибка кода завершения, получено %v", err)
	}
	var exitErr *CommandExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("ожидался тип CommandExitError, получено %T", err)
	}
	if exitErr.ExitCode != 17 {
		t.Fatalf("неожиданный код завершения: %d", exitErr.ExitCode)
	}
	if strings.Contains(err.Error(), "секретный") {
		t.Fatalf("диагностика раскрыла вывод команды: %v", err)
	}
}

func TestТаймаутИОтменаРазличаются(t *testing.T) {
	t.Run("таймаут команды", func(t *testing.T) {
		runner := newFakeRunner(t, runnerConfig{
			timeout:     20 * time.Millisecond,
			stdoutLimit: 1024,
			stderrLimit: 1024,
		})
		t.Setenv("FAKE_PASEO_SLEEP", "5")

		_, err := runner.run(context.Background(), command{name: "status", args: []string{"status", "--json"}})
		if !errors.Is(err, ErrCommandTimeout) {
			t.Fatalf("ожидался тайм-аут, получено %v", err)
		}
	})

	t.Run("отмена вызывающего контекста", func(t *testing.T) {
		runner := newFakeRunner(t, runnerConfig{
			timeout:     time.Second,
			stdoutLimit: 1024,
			stderrLimit: 1024,
		})
		t.Setenv("FAKE_PASEO_SLEEP", "5")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := runner.run(ctx, command{name: "status", args: []string{"status", "--json"}})
		if !errors.Is(err, ErrCommandCanceled) {
			t.Fatalf("ожидалась отмена, получено %v", err)
		}
		if ctx.Err() != context.Canceled {
			t.Fatalf("контекст вызывающей стороны изменён неожиданно: %v", ctx.Err())
		}
	})
}

func TestПереполнениеStdoutИStderrРазличается(t *testing.T) {
	t.Run("stdout", func(t *testing.T) {
		runner := newFakeRunner(t, runnerConfig{
			timeout:     time.Second,
			stdoutLimit: 8,
			stderrLimit: 1024,
		})
		t.Setenv("FAKE_PASEO_STDOUT", "123456789")

		_, err := runner.run(context.Background(), command{name: "status", args: []string{"status"}})
		if !errors.Is(err, ErrStdoutLimit) {
			t.Fatalf("ожидалось переполнение stdout, получено %v", err)
		}
	})

	t.Run("stderr", func(t *testing.T) {
		runner := newFakeRunner(t, runnerConfig{
			timeout:     time.Second,
			stdoutLimit: 1024,
			stderrLimit: 8,
		})
		t.Setenv("FAKE_PASEO_STDERR", "123456789")
		t.Setenv("FAKE_PASEO_EXIT", "1")

		_, err := runner.run(context.Background(), command{name: "status", args: []string{"status"}})
		if !errors.Is(err, ErrStderrLimit) {
			t.Fatalf("ожидалось переполнение stderr, получено %v", err)
		}
	})
}

func TestОтсутствующийPaseoИНекорректныеЛимитыОтклоняются(t *testing.T) {
	t.Run("исполняемый файл не найден", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := newRunner(defaultRunnerConfig())
		if !errors.Is(err, ErrExecutableNotFound) {
			t.Fatalf("ожидалась ошибка поиска paseo, получено %v", err)
		}
	})

	t.Run("нулевой лимит", func(t *testing.T) {
		_, err := newRunner(runnerConfig{timeout: time.Second, stdoutLimit: 0, stderrLimit: 1})
		if !errors.Is(err, ErrInvalidRunnerConfig) {
			t.Fatalf("ожидалась ошибка конфигурации, получено %v", err)
		}
	})
}

func newFakeRunner(t *testing.T, config runnerConfig) *runner {
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
stdout="$FAKE_PASEO_STDOUT"
if [ "$1" = "--version" ]; then
  stdout="${FAKE_PASEO_VERSION-$stdout}"
elif [ "$1" = "status" ]; then
  stdout="${FAKE_PASEO_STATUS-$stdout}"
elif [ "$1" = "provider" ] && [ "$2" = "ls" ]; then
  stdout="${FAKE_PASEO_PROVIDERS-$stdout}"
elif [ "$1" = "provider" ] && [ "$2" = "models" ]; then
  stdout="${FAKE_PASEO_MODELS-$stdout}"
elif [ "$1" = "workspace" ] && [ "$2" = "ls" ]; then
  stdout="${FAKE_PASEO_WORKSPACES-$stdout}"
elif [ "$1" = "workspace" ] && [ "$2" = "create" ]; then
  stdout="${FAKE_PASEO_WORKSPACE_CREATE-$stdout}"
elif [ "$1" = "run" ]; then
  stdout="${FAKE_PASEO_RUN-$stdout}"
elif [ "$1" = "wait" ]; then
  stdout="${FAKE_PASEO_WAIT-$stdout}"
elif [ "$1" = "archive" ]; then
  stdout="${FAKE_PASEO_ARCHIVE-$stdout}"
  if [ -n "$FAKE_PASEO_ARCHIVE_STATE" ]; then
    : > "$FAKE_PASEO_ARCHIVE_STATE"
  fi
elif [ "$1" = "ls" ]; then
  exact=""
  for argument in "$@"; do
    case "$argument" in
      oa.workspace=*) exact="yes" ;;
    esac
  done
  if [ -n "$exact" ]; then
    stdout="${FAKE_PASEO_LS_EXACT-$stdout}"
  else
    stdout="${FAKE_PASEO_LS_BROAD-$stdout}"
  fi
elif [ "$1" = "inspect" ]; then
  if [ -n "$FAKE_PASEO_ARCHIVE_STATE" ] && [ -e "$FAKE_PASEO_ARCHIVE_STATE" ]; then
    stdout="${FAKE_PASEO_INSPECT_AFTER_ARCHIVE-$stdout}"
  else
    stdout="${FAKE_PASEO_INSPECT-$stdout}"
  fi
fi
if [ -n "$stdout" ]; then
  printf '%s' "$stdout"
fi
if [ -n "$FAKE_PASEO_STDERR" ]; then
  printf '%s' "$FAKE_PASEO_STDERR" >&2
fi
if [ -n "$FAKE_PASEO_SLEEP" ]; then
  exec /bin/sleep "$FAKE_PASEO_SLEEP"
fi
exit "${FAKE_PASEO_EXIT:-0}"
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("создать подменный paseo: %v", err)
	}
	t.Setenv("PATH", dir)

	runner, err := newRunner(config)
	if err != nil {
		t.Fatalf("создать исполнитель команд: %v", err)
	}
	return runner
}
