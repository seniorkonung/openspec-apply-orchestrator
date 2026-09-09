package paseo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

func TestОжиданиеВозвращаетТипизированноеСобытиеОднойКомандой(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{status: "idle", want: "idle"},
		{status: "timeout", want: "timeout"},
		{status: "permission", want: "permission"},
		{status: "error", want: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			recordPath := filepath.Join(t.TempDir(), "команды")
			client := newClient(newFakeAdapter(t, adapterConfig{
				timeout:     20 * time.Millisecond,
				stdoutLimit: 1024,
				stderrLimit: 1024,
			}), testDaemonOwner)
			t.Setenv("FAKE_PASEO_RECORD", recordPath)
			t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, "agent-123", tt.status, "внешний текст"))

			result, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
			if err != nil {
				t.Fatalf("дождаться события сессии: %v", err)
			}
			if got := waitResultName(result); got != tt.want {
				t.Fatalf("неожиданный тип результата: получен %q, нужен %q", got, tt.want)
			}

			recorded, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatalf("прочитать журнал команд: %v", err)
			}
			if string(recorded) != "wait\nagent-123\n--json\n" {
				t.Fatalf("ожидалась одна команда wait без inspect:\n%s", recorded)
			}
		})
	}
}

func TestОжиданиеНеИспользуетКороткийТаймаутИсполнителя(t *testing.T) {
	client := newClient(newFakeAdapter(t, adapterConfig{
		timeout:     20 * time.Millisecond,
		stdoutLimit: 1024,
		stderrLimit: 1024,
	}), testDaemonOwner)
	t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, "agent-123", "idle", "готово"))
	t.Setenv("FAKE_PASEO_SLEEP", "0.1")

	result, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
	if err != nil {
		t.Fatalf("дождаться события дольше технического тайм-аута: %v", err)
	}
	if _, ok := result.(WaitIdle); !ok {
		t.Fatalf("ожидался результат WaitIdle, получено %T", result)
	}
}

func TestОжиданиеОтменяетсяКонтекстомПроцесса(t *testing.T) {
	client := newClient(newFakeAdapter(t, adapterConfig{
		timeout:     time.Second,
		stdoutLimit: 1024,
		stderrLimit: 1024,
	}), testDaemonOwner)
	t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, "agent-123", "idle", "готово"))
	t.Setenv("FAKE_PASEO_SLEEP", "5")

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(30*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()

	_, err := client.Wait(ctx, mustSessionID(t, "agent-123"))
	if !errors.Is(err, ErrCommandCanceled) {
		t.Fatalf("ожидалась отмена команды wait, получено %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("отмена wait заняла слишком долго: %s", elapsed)
	}
}

func TestОжиданиеСтрогоПроверяетJSONИПолныйID(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   error
	}{
		{
			name:   "несовпавший полный ID",
			output: marshalWaitJSON(t, "agent-other", "idle", "готово"),
			want:   ErrWaitSessionIdentityMismatch,
		},
		{
			name:   "неизвестный статус",
			output: marshalWaitJSON(t, "agent-123", "working", "ещё работаю"),
			want:   ErrUnexpectedJSON,
		},
		{
			name:   "отсутствует message",
			output: `{"agentId":"agent-123","status":"idle"}`,
			want:   ErrUnexpectedJSON,
		},
		{
			name:   "пустой вывод",
			output: "",
			want:   ErrEmptyOutput,
		},
		{
			name:   "обрезанный JSON",
			output: `{"agentId":"agent-123"`,
			want:   ErrTruncatedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(newFakeAdapter(t, adapterConfig{
				timeout:     time.Second,
				stdoutLimit: 1024,
				stderrLimit: 1024,
			}), testDaemonOwner)
			t.Setenv("FAKE_PASEO_WAIT", tt.output)

			_, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.want, err)
			}
		})
	}
}

func TestОжиданиеДопускаетНовоеНеиспользуемоеПоле(t *testing.T) {
	client := newClient(newFakeAdapter(t, adapterConfig{
		timeout:     time.Second,
		stdoutLimit: 1024,
		stderrLimit: 1024,
	}), testDaemonOwner)
	t.Setenv(
		"FAKE_PASEO_WAIT",
		`{"agentId":"agent-123","status":"idle","message":"готово","новое":{"секрет":true}}`,
	)

	result, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
	if err != nil {
		t.Fatalf("прочитать аддитивный результат wait: %v", err)
	}
	if _, ok := result.(WaitIdle); !ok {
		t.Fatalf("ожидался результат WaitIdle, получено %T", result)
	}
}

func TestОжиданиеНеСохраняетИНераскрываетMessage(t *testing.T) {
	const activity = "СЕКРЕТНАЯ НЕДАВНЯЯ АКТИВНОСТЬ\nсодержимое разговора"
	client := newClient(newFakeAdapter(t, adapterConfig{
		timeout:     time.Second,
		stdoutLimit: 1024,
		stderrLimit: 1024,
	}), testDaemonOwner)
	t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, "agent-123", "idle", activity))

	result, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
	if err != nil {
		t.Fatalf("прочитать результат wait: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), activity) {
		t.Fatalf("доменный результат сохранил message: %#v", result)
	}
}

func TestОжиданиеВозвращаетОшибкиКомандыИЛимита(t *testing.T) {
	t.Run("ненулевой код", func(t *testing.T) {
		client := newClient(newFakeAdapter(t, adapterConfig{
			timeout:     time.Second,
			stdoutLimit: 1024,
			stderrLimit: 1024,
		}), testDaemonOwner)
		t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, "agent-123", "idle", "секрет"))
		t.Setenv("FAKE_PASEO_EXIT", "9")

		_, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
		if !errors.Is(err, ErrCommandExit) {
			t.Fatalf("ожидалась ошибка команды, получено %v", err)
		}
		if strings.Contains(err.Error(), "секрет") {
			t.Fatalf("ошибка раскрыла message: %v", err)
		}
	})

	t.Run("превышение stdout", func(t *testing.T) {
		client := newClient(newFakeAdapter(t, adapterConfig{
			timeout:     time.Second,
			stdoutLimit: 32,
			stderrLimit: 1024,
		}), testDaemonOwner)
		t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, "agent-123", "idle", strings.Repeat("x", 64)))

		_, err := client.Wait(context.Background(), mustSessionID(t, "agent-123"))
		if !errors.Is(err, ErrStdoutLimit) {
			t.Fatalf("ожидалось превышение stdout, получено %v", err)
		}
	})
}

func marshalWaitJSON(t *testing.T, agentID, status, message string) string {
	t.Helper()
	output, err := json.Marshal(map[string]string{
		"agentId": agentID,
		"status":  status,
		"message": message,
	})
	if err != nil {
		t.Fatalf("подготовить JSON wait: %v", err)
	}
	return string(output)
}

func mustSessionID(t *testing.T, value string) orchestrator.SessionID {
	t.Helper()
	id, err := orchestrator.NewSessionID(value)
	if err != nil {
		t.Fatalf("создать ID сессии: %v", err)
	}
	return id
}

func waitResultName(result WaitResult) string {
	switch result.(type) {
	case WaitIdle:
		return "idle"
	case WaitTimeout:
		return "timeout"
	case WaitPermission:
		return "permission"
	case WaitAgentError:
		return "error"
	default:
		return "unknown"
	}
}
