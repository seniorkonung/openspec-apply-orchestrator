package paseo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
)

var _ UntrustedSessionSettings = config.UntrustedAgentSettings{}

func TestНастройкиПроверяютсяПоКаталогуИЗакрытойТаблицеПолногоДоступа(t *testing.T) {
	client := newFakeClient(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_PROVIDERS", encodeDirectoryJSON(t, []any{
		providerCatalogItem("codex", "available", "Enabled"),
	}))
	t.Setenv("FAKE_PASEO_MODELS", encodeDirectoryJSON(t, []any{
		modelCatalogItem("gpt-5.6-sol", []string{"low", "high"}, "low"),
	}))
	raw := untrustedSettings{provider: "codex", model: "gpt-5.6-sol", reasoning: "high", hasReasoning: true}

	settings, err := client.VerifySessionSettings(context.Background(), compatibleTestEnvironment(), raw)
	if err != nil {
		t.Fatalf("проверить настройки: %v", err)
	}
	reasoning, hasReasoning := settings.Reasoning()
	if settings.Provider() != "codex" || settings.Model() != "gpt-5.6-sol" ||
		!hasReasoning || reasoning != "high" || settings.Mode() != "full-access" {
		t.Fatalf("неожиданные проверенные настройки: %#v", settings)
	}
	if settings.mode.approvalPolicy != "never" || settings.mode.sandbox != "danger-full-access" {
		t.Fatalf("неверная семантика полного доступа: %#v", settings.mode)
	}

	assertOnlyCatalogReads(t, recordPath)
}

func TestНеобязательныйReasoningНеПодменяетсяЗначениемПоУмолчанию(t *testing.T) {
	client := newFakeClient(t)
	t.Setenv("FAKE_PASEO_PROVIDERS", encodeDirectoryJSON(t, []any{
		providerCatalogItem("codex", "available", "Enabled"),
	}))
	t.Setenv("FAKE_PASEO_MODELS", encodeDirectoryJSON(t, []any{
		modelCatalogItem("gpt-5.6-sol", []string{"low", "high"}, "low"),
	}))

	settings, err := client.VerifySessionSettings(
		context.Background(),
		compatibleTestEnvironment(),
		untrustedSettings{provider: "codex", model: "gpt-5.6-sol"},
	)
	if err != nil {
		t.Fatalf("проверить настройки без reasoning: %v", err)
	}
	if reasoning, present := settings.Reasoning(); present || reasoning != "" {
		t.Fatalf("reasoning неожиданно получил значение: %q, present=%t", reasoning, present)
	}
}

func TestТаблицаПолногоДоступаЗащищенаТочнымиКонтрактнымиФикстурами(t *testing.T) {
	want := map[compatibilityKey]fullAccessMode{
		{version: "0.7.2", provider: "codex"}: {
			id:             "full-access",
			approvalPolicy: "never",
			sandbox:        "danger-full-access",
		},
	}
	if !reflect.DeepEqual(fullAccessCompatibility, want) {
		t.Fatalf("таблица полного доступа не соответствует контрактным фикстурам:\nполучено: %#v\nожидалось: %#v", fullAccessCompatibility, want)
	}
}

func TestНедопустимыеНастройкиОтклоняютсяДоМутаций(t *testing.T) {
	tests := []struct {
		name        string
		environment CompatibleEnvironment
		raw         untrustedSettings
		providers   []any
		models      []any
		want        error
		wantValue   string
	}{
		{
			name:        "несовместимая версия",
			environment: CompatibleEnvironment{serverID: ServerID{value: "server-1"}, version: Version{value: "0.7.3"}},
			raw:         untrustedSettings{provider: "codex", model: "gpt-5.6-sol"},
			want:        ErrIncompatibleCLIVersion,
		},
		{
			name:        "значение похоже на флаг CLI",
			environment: compatibleTestEnvironment(),
			raw:         untrustedSettings{provider: "--host", model: "gpt-5.6-sol"},
			want:        ErrInvalidSessionSettings,
		},
		{
			name:        "provider отсутствует",
			environment: compatibleTestEnvironment(),
			raw:         untrustedSettings{provider: "codex", model: "gpt-5.6-sol"},
			providers:   []any{},
			want:        ErrProviderNotFound,
			wantValue:   "codex",
		},
		{
			name:        "пользовательский профиль не поддерживается",
			environment: compatibleTestEnvironment(),
			raw:         untrustedSettings{provider: "codex-work", model: "gpt-5.6-sol"},
			providers: []any{
				providerCatalogItem("codex-work", "available", "Enabled"),
			},
			want:      ErrUnsupportedProvider,
			wantValue: "codex-work",
		},
		{
			name:        "встроенный provider без полного режима не поддерживается",
			environment: compatibleTestEnvironment(),
			raw:         untrustedSettings{provider: "claude", model: "claude-opus"},
			providers: []any{
				providerCatalogItem("claude", "available", "Enabled"),
			},
			want:      ErrUnsupportedProvider,
			wantValue: "claude",
		},
		{
			name:        "provider недоступен",
			environment: compatibleTestEnvironment(),
			raw:         untrustedSettings{provider: "codex", model: "gpt-5.6-sol"},
			providers: []any{
				providerCatalogItem("codex", "unavailable", "Disabled"),
			},
			want:      ErrProviderUnavailable,
			wantValue: "codex",
		},
		{
			name:        "model отсутствует",
			environment: compatibleTestEnvironment(),
			raw:         untrustedSettings{provider: "codex", model: "gpt-5.6-sol"},
			providers: []any{
				providerCatalogItem("codex", "available", "Enabled"),
			},
			models:    []any{},
			want:      ErrModelNotFound,
			wantValue: "gpt-5.6-sol",
		},
		{
			name:        "reasoning отсутствует",
			environment: compatibleTestEnvironment(),
			raw: untrustedSettings{
				provider: "codex", model: "gpt-5.6-sol", reasoning: "ultra", hasReasoning: true,
			},
			providers: []any{
				providerCatalogItem("codex", "available", "Enabled"),
			},
			models: []any{
				modelCatalogItem("gpt-5.6-sol", []string{"low", "high"}, "low"),
			},
			want:      ErrReasoningNotFound,
			wantValue: "ultra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeClient(t)
			recordPath := filepath.Join(t.TempDir(), "вызовы")
			t.Setenv("FAKE_PASEO_RECORD", recordPath)
			if tt.providers != nil {
				t.Setenv("FAKE_PASEO_PROVIDERS", encodeDirectoryJSON(t, tt.providers))
			}
			if tt.models != nil {
				t.Setenv("FAKE_PASEO_MODELS", encodeDirectoryJSON(t, tt.models))
			}

			_, err := client.VerifySessionSettings(context.Background(), tt.environment, tt.raw)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.want, err)
			}
			if tt.wantValue != "" && !strings.Contains(err.Error(), tt.wantValue) {
				t.Fatalf("ошибка не указывает проблемное значение %q: %v", tt.wantValue, err)
			}
			assertOnlyCatalogReads(t, recordPath)
		})
	}
}

type untrustedSettings struct {
	provider     string
	model        string
	reasoning    string
	hasReasoning bool
}

func (settings untrustedSettings) Provider() string {
	return settings.provider
}

func (settings untrustedSettings) Model() string {
	return settings.model
}

func (settings untrustedSettings) Reasoning() (string, bool) {
	return settings.reasoning, settings.hasReasoning
}

func assertOnlyCatalogReads(t *testing.T, recordPath string) {
	t.Helper()
	recorded, err := os.ReadFile(recordPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	for _, argument := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		if argument == "workspace" || argument == "run" || argument == "archive" {
			t.Fatalf("обнаружена изменяющая команда %q:\n%s", argument, recorded)
		}
	}
}
