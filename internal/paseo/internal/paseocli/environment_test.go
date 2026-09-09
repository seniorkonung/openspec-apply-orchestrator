package paseocli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDaemonOwner = "1000@test-host"

func TestАдаптерПроверяетСовместимуюЛокальнуюСредуТочнымиКомандами(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	installEnvironmentTestExecutable(t)
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_VERSION", ActiveContract().CLIVersion())
	t.Setenv("FAKE_PASEO_STATUS", environmentStatusJSON(t, nil))

	adapter, err := NewWithConfig(RunnerConfig{
		Timeout:     time.Second,
		StdoutLimit: 64 << 10,
		StderrLimit: 64 << 10,
	})
	if err != nil {
		t.Fatalf("создать адаптер: %v", err)
	}
	environment, err := adapter.CheckEnvironment(context.Background(), testDaemonOwner)
	if err != nil {
		t.Fatalf("проверить среду: %v", err)
	}
	if environment.ServerID() != "srv_test123" {
		t.Fatalf("неожиданный serverId: %q", environment.ServerID())
	}
	if !environment.IsCompatible() {
		t.Fatal("проверенная среда не сохранила доказательство активного контракта")
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(recorded) != "--version\nstatus\n--json\n" {
		t.Fatalf("неожиданные команды проверки среды:\n%s", recorded)
	}
}

func TestАдаптерОтклоняетНесовместимуюИНедостовернуюСреду(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		status   func(*testing.T) string
		owner    string
		expected error
	}{
		{
			name:     "несовместимая версия CLI",
			version:  "0.8.0",
			status:   func(t *testing.T) string { return environmentStatusJSON(t, nil) },
			owner:    testDaemonOwner,
			expected: ErrIncompatibleCLIVersion,
		},
		{
			name:    "status противоречит версии CLI",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["cliVersion"] = "0.7.1"
				})
			},
			owner:    testDaemonOwner,
			expected: ErrInconsistentCLIVersion,
		},
		{
			name:    "несовместимая версия daemon",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["daemonVersion"] = "0.8.0"
				})
			},
			owner:    testDaemonOwner,
			expected: ErrIncompatibleDaemonVersion,
		},
		{
			name:    "daemon не локален",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["localDaemon"] = "stopped"
				})
			},
			owner:    testDaemonOwner,
			expected: ErrDaemonNotLocal,
		},
		{
			name:    "daemon недоступен",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["connectedDaemon"] = "unreachable"
				})
			},
			owner:    testDaemonOwner,
			expected: ErrDaemonUnavailable,
		},
		{
			name:     "владелец daemon не совпадает",
			version:  ActiveContract().CLIVersion(),
			status:   func(t *testing.T) string { return environmentStatusJSON(t, nil) },
			owner:    "2000@test-host",
			expected: ErrDaemonOwnerMismatch,
		},
		{
			name:    "serverId отсутствует",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["serverId"] = nil
				})
			},
			owner:    testDaemonOwner,
			expected: ErrInvalidServerID,
		},
		{
			name:    "serverId содержит пробел",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["serverId"] = "srv test"
				})
			},
			owner:    testDaemonOwner,
			expected: ErrInvalidServerID,
		},
		{
			name:    "hostname противоречит владельцу",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["hostname"] = "other-host"
				})
			},
			owner:    testDaemonOwner,
			expected: ErrUnexpectedJSON,
		},
		{
			name:     "status пуст",
			version:  ActiveContract().CLIVersion(),
			status:   func(*testing.T) string { return "" },
			owner:    testDaemonOwner,
			expected: ErrEmptyOutput,
		},
		{
			name:     "status содержит обрезанный JSON",
			version:  ActiveContract().CLIVersion(),
			status:   func(*testing.T) string { return `{"serverId":"srv_test123"` },
			owner:    testDaemonOwner,
			expected: ErrTruncatedJSON,
		},
		{
			name:    "status содержит неизвестное поле",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					status["секретный-токен"] = true
				})
			},
			owner:    testDaemonOwner,
			expected: ErrUnexpectedJSON,
		},
		{
			name:    "status не содержит обязательное поле",
			version: ActiveContract().CLIVersion(),
			status: func(t *testing.T) string {
				return environmentStatusJSON(t, func(status map[string]any) {
					delete(status, "providers")
				})
			},
			owner:    testDaemonOwner,
			expected: ErrUnexpectedJSON,
		},
		{
			name:     "после status есть второй JSON",
			version:  ActiveContract().CLIVersion(),
			status:   func(t *testing.T) string { return environmentStatusJSON(t, nil) + `{}` },
			owner:    testDaemonOwner,
			expected: ErrUnexpectedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installEnvironmentTestExecutable(t)
			t.Setenv("FAKE_PASEO_VERSION", tt.version)
			t.Setenv("FAKE_PASEO_STATUS", tt.status(t))

			adapter, err := NewWithConfig(RunnerConfig{
				Timeout:     time.Second,
				StdoutLimit: 64 << 10,
				StderrLimit: 64 << 10,
			})
			if err != nil {
				t.Fatalf("создать адаптер: %v", err)
			}
			_, err = adapter.CheckEnvironment(context.Background(), tt.owner)
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.expected, err)
			}
		})
	}
}

func TestАдаптерОтклоняетНедостоверныйВыводВерсии(t *testing.T) {
	for _, output := range []string{
		"0.7.2\nсекрет",
		"секрет",
		" 0.7.2",
		"0.7.2-" + strings.Repeat("a", 65),
	} {
		t.Run(output, func(t *testing.T) {
			installEnvironmentTestExecutable(t)
			t.Setenv("FAKE_PASEO_VERSION", output)
			adapter, err := NewWithConfig(RunnerConfig{
				Timeout:     time.Second,
				StdoutLimit: 64 << 10,
				StderrLimit: 64 << 10,
			})
			if err != nil {
				t.Fatalf("создать адаптер: %v", err)
			}

			_, err = adapter.CheckEnvironment(context.Background(), testDaemonOwner)
			if !errors.Is(err, ErrUnexpectedVersionOutput) {
				t.Fatalf("ожидалась ошибка формы версии, получено %v", err)
			}
			if strings.Contains(err.Error(), "секрет") {
				t.Fatalf("ошибка раскрыла непроверенный вывод: %v", err)
			}
		})
	}
}

func TestАдаптерСохраняетГраницыПроцессаБезУтечкиВывода(t *testing.T) {
	t.Run("тайм-аут", func(t *testing.T) {
		installEnvironmentTestExecutable(t)
		t.Setenv("FAKE_PASEO_SLEEP", "5")

		adapter, err := NewWithConfig(RunnerConfig{
			Timeout:     20 * time.Millisecond,
			StdoutLimit: 1024,
			StderrLimit: 1024,
		})
		if err != nil {
			t.Fatalf("создать адаптер: %v", err)
		}
		_, err = adapter.CheckEnvironment(context.Background(), testDaemonOwner)
		if !errors.Is(err, ErrCommandTimeout) {
			t.Fatalf("ожидался тайм-аут, получено %v", err)
		}
	})

	t.Run("отмена", func(t *testing.T) {
		installEnvironmentTestExecutable(t)
		adapter, err := NewWithConfig(RunnerConfig{
			Timeout:     time.Second,
			StdoutLimit: 1024,
			StderrLimit: 1024,
		})
		if err != nil {
			t.Fatalf("создать адаптер: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = adapter.CheckEnvironment(ctx, testDaemonOwner)
		if !errors.Is(err, ErrCommandCanceled) {
			t.Fatalf("ожидалась отмена, получено %v", err)
		}
	})

	t.Run("лимит stdout", func(t *testing.T) {
		installEnvironmentTestExecutable(t)
		t.Setenv("FAKE_PASEO_VERSION", strings.Repeat("x", 9))
		adapter, err := NewWithConfig(RunnerConfig{
			Timeout:     time.Second,
			StdoutLimit: 8,
			StderrLimit: 1024,
		})
		if err != nil {
			t.Fatalf("создать адаптер: %v", err)
		}

		_, err = adapter.CheckEnvironment(context.Background(), testDaemonOwner)
		if !errors.Is(err, ErrStdoutLimit) {
			t.Fatalf("ожидалось переполнение stdout, получено %v", err)
		}
	})

	t.Run("stderr не раскрывается", func(t *testing.T) {
		installEnvironmentTestExecutable(t)
		t.Setenv("FAKE_PASEO_STDERR", "секретный токен")
		t.Setenv("FAKE_PASEO_EXIT", "17")
		adapter, err := NewWithConfig(RunnerConfig{
			Timeout:     time.Second,
			StdoutLimit: 1024,
			StderrLimit: 1024,
		})
		if err != nil {
			t.Fatalf("создать адаптер: %v", err)
		}

		_, err = adapter.CheckEnvironment(context.Background(), testDaemonOwner)
		if !errors.Is(err, ErrCommandExit) {
			t.Fatalf("ожидалась ошибка процесса, получено %v", err)
		}
		if strings.Contains(err.Error(), "секретный") {
			t.Fatalf("ошибка раскрыла stderr: %v", err)
		}
	})
}

func installEnvironmentTestExecutable(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "paseo")
	script := `#!/bin/sh
if [ -n "$FAKE_PASEO_RECORD" ]; then
  printf '%s\n' "$@" >> "$FAKE_PASEO_RECORD"
fi
if [ "$1" = "--version" ]; then
  stdout="$FAKE_PASEO_VERSION"
elif [ "$1" = "status" ]; then
  stdout="$FAKE_PASEO_STATUS"
else
  stdout="$FAKE_PASEO_STDOUT"
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
}

func environmentStatusJSON(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	status := map[string]any{
		"serverId":        "srv_test123",
		"localDaemon":     "running",
		"connectedDaemon": "reachable",
		"home":            "/tmp/paseo-home",
		"listen":          "127.0.0.1:6767",
		"relay":           "disabled",
		"hostname":        "test-host",
		"pid":             42,
		"startedAt":       "2026-09-07T08:11:35.910Z",
		"owner":           testDaemonOwner,
		"logPath":         "/tmp/paseo-home/daemon.log",
		"daemonNode":      "/usr/bin/node",
		"cliNode":         "/usr/bin/node",
		"cliVersion":      ActiveContract().CLIVersion(),
		"daemonVersion":   ActiveContract().DaemonVersion(),
		"desktopManaged":  false,
		"providers": []map[string]any{
			{
				"label":   "Codex",
				"path":    "available",
				"version": nil,
				"source":  "daemon",
			},
		},
	}
	if mutate != nil {
		mutate(status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("собрать JSON status: %v", err)
	}
	return string(encoded)
}
