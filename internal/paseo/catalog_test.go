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

func TestКаталогНастроекЧитаетсяТочнымиНемутирующимиКомандами(t *testing.T) {
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_PROVIDERS", encodeDirectoryJSON(t, []any{
		providerCatalogItem("codex", "available", "Enabled"),
	}))
	t.Setenv("FAKE_PASEO_MODELS", encodeDirectoryJSON(t, []any{
		modelCatalogItem("gpt-5.6-sol", []string{"low", "high"}, "low"),
	}))

	providers, err := client.readProviderCatalog(context.Background())
	if err != nil {
		t.Fatalf("прочитать каталог провайдеров: %v", err)
	}
	provider, ok := providers["codex"]
	if !ok || provider.status != providerAvailable || !provider.enabled {
		t.Fatalf("неожиданный провайдер codex: %#v", provider)
	}

	models, err := client.readModelCatalog(context.Background(), "codex")
	if err != nil {
		t.Fatalf("прочитать каталог моделей: %v", err)
	}
	model, ok := models["gpt-5.6-sol"]
	if !ok || len(model.reasoning) != 2 || model.reasoning[1] != "high" {
		t.Fatalf("неожиданная модель gpt-5.6-sol: %#v", model)
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

func TestКаталогНастроекСтрогоОтклоняетПовреждённыйJSON(t *testing.T) {
	tests := []struct {
		name      string
		providers any
		models    any
		read      func(*Client) error
	}{
		{
			name: "неизвестное поле провайдера",
			providers: []any{func() map[string]any {
				item := providerCatalogItem("codex", "available", "Enabled")
				item["source"] = "builtin"
				return item
			}()},
			read: func(client *Client) error {
				_, err := client.readProviderCatalog(context.Background())
				return err
			},
		},
		{
			name: "неизвестное состояние провайдера",
			providers: []any{
				providerCatalogItem("codex", "ready", "Enabled"),
			},
			read: func(client *Client) error {
				_, err := client.readProviderCatalog(context.Background())
				return err
			},
		},
		{
			name: "повтор идентификатора провайдера",
			providers: []any{
				providerCatalogItem("codex", "available", "Enabled"),
				providerCatalogItem("codex", "available", "Enabled"),
			},
			read: func(client *Client) error {
				_, err := client.readProviderCatalog(context.Background())
				return err
			},
		},
		{
			name: "отсутствующее поле модели",
			models: []any{func() map[string]any {
				item := modelCatalogItem("gpt-5.6-sol", []string{"high"}, "high")
				delete(item, "defaultThinkingOptionId")
				return item
			}()},
			read: func(client *Client) error {
				_, err := client.readModelCatalog(context.Background(), "codex")
				return err
			},
		},
		{
			name: "повтор reasoning",
			models: []any{
				modelCatalogItem("gpt-5.6-sol", []string{"high", "high"}, "high"),
			},
			read: func(client *Client) error {
				_, err := client.readModelCatalog(context.Background(), "codex")
				return err
			},
		},
		{
			name: "несогласованное представление reasoning",
			models: []any{func() map[string]any {
				item := modelCatalogItem("gpt-5.6-sol", []string{"low", "high"}, "low")
				item["thinkingOptions"] = "high, low"
				return item
			}()},
			read: func(client *Client) error {
				_, err := client.readModelCatalog(context.Background(), "codex")
				return err
			},
		},
		{
			name: "default reasoning отсутствует в списке",
			models: []any{
				modelCatalogItem("gpt-5.6-sol", []string{"low"}, "high"),
			},
			read: func(client *Client) error {
				_, err := client.readModelCatalog(context.Background(), "codex")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient(t)
			if tt.providers != nil {
				t.Setenv("FAKE_PASEO_PROVIDERS", encodeDirectoryJSON(t, tt.providers))
			}
			if tt.models != nil {
				t.Setenv("FAKE_PASEO_MODELS", encodeDirectoryJSON(t, tt.models))
			}

			if err := tt.read(client); !errors.Is(err, ErrUnexpectedJSON) {
				t.Fatalf("ожидалась ошибка строгой схемы, получено %v", err)
			}
		})
	}
}

func TestКаталогНастроекОтклоняетNullВместоСписка(t *testing.T) {
	t.Run("provider ls", func(t *testing.T) {
		client := newFakeClient(t)
		t.Setenv("FAKE_PASEO_PROVIDERS", "null")

		_, err := client.readProviderCatalog(context.Background())
		if !errors.Is(err, ErrUnexpectedJSON) {
			t.Fatalf("ожидалась ошибка null-каталога провайдеров, получено %v", err)
		}
	})

	t.Run("provider models", func(t *testing.T) {
		client := newFakeClient(t)
		t.Setenv("FAKE_PASEO_MODELS", "null")

		_, err := client.readModelCatalog(context.Background(), "codex")
		if !errors.Is(err, ErrUnexpectedJSON) {
			t.Fatalf("ожидалась ошибка null-каталога моделей, получено %v", err)
		}
	})
}

func TestКаталогНастроекСохраняетОшибкуИсточника(t *testing.T) {
	t.Run("тайм-аут", func(t *testing.T) {
		client := newClient(newFakeRunner(t, runnerConfig{
			timeout:     20 * time.Millisecond,
			stdoutLimit: 1024,
			stderrLimit: 1024,
		}), testDaemonOwner)
		t.Setenv("FAKE_PASEO_SLEEP", "5")

		_, err := client.readProviderCatalog(context.Background())
		if !errors.Is(err, ErrCommandTimeout) {
			t.Fatalf("ожидался тайм-аут источника, получено %v", err)
		}
	})

	t.Run("превышение размера", func(t *testing.T) {
		client := newClient(newFakeRunner(t, runnerConfig{
			timeout:     time.Second,
			stdoutLimit: 8,
			stderrLimit: 1024,
		}), testDaemonOwner)
		t.Setenv("FAKE_PASEO_PROVIDERS", `[123456789]`)

		_, err := client.readProviderCatalog(context.Background())
		if !errors.Is(err, ErrStdoutLimit) {
			t.Fatalf("ожидалось превышение размера источника, получено %v", err)
		}
	})
}

func providerCatalogItem(provider, status, enabled string) map[string]any {
	return map[string]any{
		"provider":    provider,
		"label":       strings.ToUpper(provider),
		"status":      status,
		"enabled":     enabled,
		"defaultMode": "auto-review",
		"modes":       "Default Permissions, Auto-review, Full Access",
	}
}

func modelCatalogItem(id string, reasoning []string, defaultReasoning any) map[string]any {
	presentation := "none"
	if len(reasoning) > 0 {
		presentation = strings.Join(reasoning, ", ")
	}
	return map[string]any{
		"model":                   strings.ToUpper(id),
		"id":                      id,
		"description":             "Тестовая модель",
		"thinkingOptionIds":       reasoning,
		"defaultThinkingOptionId": defaultReasoning,
		"thinkingOptions":         presentation,
	}
}
