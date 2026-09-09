package paseo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

const testDaemonOwner = "1000@test-host"

func TestСовместимаяЛокальнаяСредаСтановитсяДоверенной(t *testing.T) {
	client := newFakeClient(t)
	setCompatibleEnvironment(t, nil)

	environment, err := client.CheckCompatibility(context.Background())
	if err != nil {
		t.Fatalf("проверить совместимость: %v", err)
	}
	if environment.ServerID().String() != "srv_test123" {
		t.Fatalf("неожиданный serverId: %q", environment.ServerID().String())
	}
	if environment.Version().String() != paseocli.ActiveContract().CLIVersion() {
		t.Fatalf("неожиданная версия: %q", environment.Version().String())
	}
}

func TestВерсииCLIИDaemonПроверяютсяДоДоверия(t *testing.T) {
	tests := []struct {
		name      string
		cliOutput string
		mutate    func(map[string]any)
		expected  error
	}{
		{
			name:      "несовместимый вывод --version",
			cliOutput: "0.8.0",
			expected:  ErrIncompatibleCLIVersion,
		},
		{
			name:      "версия CLI в status противоречит --version",
			cliOutput: paseocli.ActiveContract().CLIVersion(),
			mutate: func(status map[string]any) {
				status["cliVersion"] = "0.7.1"
			},
			expected: ErrInconsistentCLIVersion,
		},
		{
			name:      "несовместимая версия daemon",
			cliOutput: paseocli.ActiveContract().CLIVersion(),
			mutate: func(status map[string]any) {
				status["daemonVersion"] = "0.8.0"
			},
			expected: ErrIncompatibleDaemonVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient(t)
			t.Setenv("FAKE_PASEO_VERSION", tt.cliOutput)
			t.Setenv("FAKE_PASEO_STATUS", statusJSON(t, tt.mutate))

			_, err := client.CheckCompatibility(context.Background())
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.expected, err)
			}
		})
	}
}

func TestЛокальностьДоступностьИВладелецDaemonПроверяются(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(map[string]any)
		expected error
	}{
		{
			name: "локальный daemon не работает",
			mutate: func(status map[string]any) {
				status["localDaemon"] = "stopped"
			},
			expected: ErrDaemonNotLocal,
		},
		{
			name: "daemon недоступен",
			mutate: func(status map[string]any) {
				status["connectedDaemon"] = "unreachable"
			},
			expected: ErrDaemonUnavailable,
		},
		{
			name: "daemon принадлежит другому пользователю",
			mutate: func(status map[string]any) {
				status["owner"] = "2000@test-host"
			},
			expected: ErrDaemonOwnerMismatch,
		},
		{
			name: "serverId отсутствует",
			mutate: func(status map[string]any) {
				status["serverId"] = nil
			},
			expected: ErrInvalidServerID,
		},
		{
			name: "serverId содержит пробел",
			mutate: func(status map[string]any) {
				status["serverId"] = "srv test"
			},
			expected: ErrInvalidServerID,
		},
		{
			name: "hostname противоречит владельцу",
			mutate: func(status map[string]any) {
				status["hostname"] = "other-host"
			},
			expected: ErrUnexpectedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient(t)
			setCompatibleEnvironment(t, tt.mutate)

			_, err := client.CheckCompatibility(context.Background())
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.expected, err)
			}
		})
	}
}

func TestОшибкиJSONСтатусаРазличаются(t *testing.T) {
	tests := []struct {
		name     string
		output   func(*testing.T) string
		expected error
	}{
		{
			name:     "пустой вывод",
			output:   func(*testing.T) string { return "" },
			expected: ErrEmptyOutput,
		},
		{
			name:     "обрезанный объект",
			output:   func(*testing.T) string { return `{"serverId":"srv_test123"` },
			expected: ErrTruncatedJSON,
		},
		{
			name: "неизвестное поле",
			output: func(t *testing.T) string {
				return statusJSON(t, func(status map[string]any) { status["секретный-токен"] = true })
			},
			expected: ErrUnexpectedJSON,
		},
		{
			name: "отсутствует обязательное поле",
			output: func(t *testing.T) string {
				return statusJSON(t, func(status map[string]any) { delete(status, "providers") })
			},
			expected: ErrUnexpectedJSON,
		},
		{
			name: "после объекта есть второй JSON",
			output: func(t *testing.T) string {
				return statusJSON(t, nil) + `{}`
			},
			expected: ErrUnexpectedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient(t)
			t.Setenv("FAKE_PASEO_STATUS", tt.output(t))

			_, err := client.Status(context.Background())
			if !errors.Is(err, tt.expected) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.expected, err)
			}
			if strings.Contains(err.Error(), "секретный") {
				t.Fatalf("ошибка раскрыла непроверенный JSON: %v", err)
			}
		})
	}
}

func TestЧтениеСтатусаМожноПовторять(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	client := newFakeClient(t)
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STATUS", statusJSON(t, nil))

	for range 2 {
		if _, err := client.Status(context.Background()); err != nil {
			t.Fatalf("прочитать status: %v", err)
		}
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	if string(recorded) != "status\n--json\nstatus\n--json\n" {
		t.Fatalf("ожидались два независимых чтения, получено %q", recorded)
	}
}

func TestВыводВерсииДолженБытьSemverОднойСтрокой(t *testing.T) {
	for _, output := range []string{"0.7.2\nсекрет", "секрет", " 0.7.2", "0.7.2-" + strings.Repeat("a", 65)} {
		t.Run(output, func(t *testing.T) {
			client := newFakeClient(t)
			t.Setenv("FAKE_PASEO_VERSION", output)

			_, err := client.Version(context.Background())
			if !errors.Is(err, ErrUnexpectedVersionOutput) {
				t.Fatalf("ожидалась ошибка формы версии, получено %v", err)
			}
			if strings.Contains(err.Error(), "секрет") {
				t.Fatalf("ошибка раскрыла непроверенный вывод: %v", err)
			}
		})
	}
}

func newFakeClient(t *testing.T) *Client {
	t.Helper()
	runner := newFakeRunner(t, runnerConfig{
		timeout:     time.Second,
		stdoutLimit: 64 << 10,
		stderrLimit: 64 << 10,
	})
	return newClient(runner, testDaemonOwner)
}

func setCompatibleEnvironment(t *testing.T, mutate func(map[string]any)) {
	t.Helper()
	t.Setenv("FAKE_PASEO_VERSION", paseocli.ActiveContract().CLIVersion())
	t.Setenv("FAKE_PASEO_STATUS", statusJSON(t, mutate))
}

func statusJSON(t *testing.T, mutate func(map[string]any)) string {
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
		"cliVersion":      paseocli.ActiveContract().CLIVersion(),
		"daemonVersion":   paseocli.ActiveContract().DaemonVersion(),
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
		t.Fatalf("собрать JSON статуса: %v", err)
	}
	return string(encoded)
}
