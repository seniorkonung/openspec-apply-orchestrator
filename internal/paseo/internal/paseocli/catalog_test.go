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

func TestАдаптерЧитаетМинимальныйКаталогТочнымиКомандами(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	adapter := newCatalogTestAdapter(t)
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{
		{
			"provider": "codex",
			"status":   "available",
			"enabled":  "Enabled",
			"новое":    true,
		},
	}))

	providers, err := adapter.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("прочитать каталог провайдеров: %v", err)
	}
	if len(providers) != 1 || providers[0].ID() != "codex" || !providers[0].Available() {
		t.Fatalf("неожиданный каталог провайдеров: %#v", providers)
	}

	t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, []map[string]any{
		{
			"id":                      "gpt-5.6-sol",
			"thinkingOptionIds":       []string{"low", "high"},
			"defaultThinkingOptionId": "low",
			"новое":                   map[string]any{"поле": true},
		},
	}))
	models, err := adapter.ListModels(context.Background(), "codex")
	if err != nil {
		t.Fatalf("прочитать каталог моделей: %v", err)
	}
	if len(models) != 1 || models[0].ID() != "gpt-5.6-sol" {
		t.Fatalf("неожиданный каталог моделей: %#v", models)
	}
	reasoning := models[0].Reasoning()
	if len(reasoning) != 2 || reasoning[1] != "high" {
		t.Fatalf("неожиданные reasoning: %#v", reasoning)
	}

	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	want := strings.Join([]string{
		"provider", "ls", "--json",
		"provider", "models", "codex", "--thinking", "--json",
	}, "\n") + "\n"
	if string(recorded) != want {
		t.Fatalf("неожиданные команды каталога:\n%s", recorded)
	}
}

func TestАдаптерОтклоняетНедостоверныйКаталог(t *testing.T) {
	tests := []struct {
		name   string
		output any
		read   func(context.Context, *Adapter) error
	}{
		{
			name:   "provider без обязательного status",
			output: []map[string]any{{"provider": "codex", "enabled": "Enabled"}},
			read: func(ctx context.Context, adapter *Adapter) error {
				_, err := adapter.ListProviders(ctx)
				return err
			},
		},
		{
			name:   "provider с неизвестным status",
			output: []map[string]any{{"provider": "codex", "status": "ready", "enabled": "Enabled"}},
			read: func(ctx context.Context, adapter *Adapter) error {
				_, err := adapter.ListProviders(ctx)
				return err
			},
		},
		{
			name: "повтор provider",
			output: []map[string]any{
				{"provider": "codex", "status": "available", "enabled": "Enabled"},
				{"provider": "codex", "status": "available", "enabled": "Enabled"},
			},
			read: func(ctx context.Context, adapter *Adapter) error {
				_, err := adapter.ListProviders(ctx)
				return err
			},
		},
		{
			name: "повтор reasoning",
			output: []map[string]any{{
				"id":                      "gpt-5.6-sol",
				"thinkingOptionIds":       []string{"high", "high"},
				"defaultThinkingOptionId": "high",
			}},
			read: func(ctx context.Context, adapter *Adapter) error {
				_, err := adapter.ListModels(ctx, "codex")
				return err
			},
		},
		{
			name: "default reasoning отсутствует в списке",
			output: []map[string]any{{
				"id":                      "gpt-5.6-sol",
				"thinkingOptionIds":       []string{"low"},
				"defaultThinkingOptionId": "high",
			}},
			read: func(ctx context.Context, adapter *Adapter) error {
				_, err := adapter.ListModels(ctx, "codex")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := newCatalogTestAdapter(t)
			t.Setenv("FAKE_PASEO_STDOUT", catalogJSON(t, tt.output))
			if err := tt.read(context.Background(), adapter); !errors.Is(err, ErrUnexpectedJSON) {
				t.Fatalf("ожидалась ошибка значимого JSON-контракта, получено %v", err)
			}
		})
	}
}

func newCatalogTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	installEnvironmentTestExecutable(t)
	adapter, err := NewWithConfig(RunnerConfig{
		Timeout:     time.Second,
		StdoutLimit: 64 << 10,
		StderrLimit: 64 << 10,
	})
	if err != nil {
		t.Fatalf("создать адаптер: %v", err)
	}
	return adapter
}

func catalogJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("собрать JSON каталога: %v", err)
	}
	return string(encoded)
}
