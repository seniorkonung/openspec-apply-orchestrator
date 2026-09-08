package paseo

import (
	"context"
	"fmt"
)

type UntrustedSessionSettings interface {
	Provider() string
	Model() string
	Reasoning() (string, bool)
}

type fullAccessMode struct {
	id             string
	approvalPolicy string
	sandbox        string
}

type compatibilityKey struct {
	version  string
	provider string
}

// Таблица содержит только режимы, чья unrestricted-семантика проверена для
// точной версии встроенного провайдера. Названия режимов из provider ls не
// являются доказательством этого контракта.
// Источники:
// https://github.com/getpaseo/paseo/blob/v0.7.2/packages/protocol/src/provider-manifest.ts
// https://github.com/getpaseo/paseo/blob/v0.7.2/packages/server/src/server/agent/providers/codex-app-server-agent.ts
var fullAccessCompatibility = map[compatibilityKey]fullAccessMode{
	{version: "0.7.2", provider: "codex"}: {
		id:             "full-access",
		approvalPolicy: "never",
		sandbox:        "danger-full-access",
	},
}

type VerifiedSessionSettings struct {
	provider     string
	model        string
	reasoning    string
	hasReasoning bool
	mode         fullAccessMode
}

func (settings VerifiedSessionSettings) runSettings() runSessionSettings {
	return runSessionSettings{
		provider:     settings.provider,
		model:        settings.model,
		reasoning:    settings.reasoning,
		hasReasoning: settings.hasReasoning,
		mode:         settings.mode.id,
	}
}

func (settings VerifiedSessionSettings) Provider() string {
	return settings.provider
}

func (settings VerifiedSessionSettings) Model() string {
	return settings.model
}

func (settings VerifiedSessionSettings) Reasoning() (string, bool) {
	return settings.reasoning, settings.hasReasoning
}

func (settings VerifiedSessionSettings) Mode() string {
	return settings.mode.id
}

func validateVerifiedSessionSettings(
	environment CompatibleEnvironment,
	settings VerifiedSessionSettings,
) error {
	if !validCatalogIdentifier(settings.provider) || !validCatalogIdentifier(settings.model) ||
		(settings.hasReasoning && !validCatalogIdentifier(settings.reasoning)) ||
		(!settings.hasReasoning && settings.reasoning != "") {
		return ErrInvalidSessionSettings
	}
	expectedMode, supported := fullAccessCompatibility[compatibilityKey{
		version:  environment.Version().String(),
		provider: settings.provider,
	}]
	if !supported || settings.mode != expectedMode {
		return ErrInvalidSessionSettings
	}
	return nil
}

func (client *Client) VerifySessionSettings(
	ctx context.Context,
	environment CompatibleEnvironment,
	untrusted UntrustedSessionSettings,
) (VerifiedSessionSettings, error) {
	if err := validateCompatibleEnvironment(environment); err != nil {
		return VerifiedSessionSettings{}, err
	}
	if untrusted == nil {
		return VerifiedSessionSettings{}, ErrInvalidSessionSettings
	}

	provider := untrusted.Provider()
	model := untrusted.Model()
	reasoning, hasReasoning := untrusted.Reasoning()
	if !validCatalogIdentifier(provider) || !validCatalogIdentifier(model) ||
		(hasReasoning && !validCatalogIdentifier(reasoning)) || (!hasReasoning && reasoning != "") {
		return VerifiedSessionSettings{}, ErrInvalidSessionSettings
	}

	providers, err := client.readProviderCatalog(ctx)
	if err != nil {
		return VerifiedSessionSettings{}, err
	}
	providerEntry, exists := providers[provider]
	if !exists {
		return VerifiedSessionSettings{}, fmt.Errorf("%w: %q", ErrProviderNotFound, provider)
	}
	mode, supported := fullAccessCompatibility[compatibilityKey{
		version:  environment.Version().String(),
		provider: provider,
	}]
	if !supported {
		return VerifiedSessionSettings{}, fmt.Errorf("%w: %q", ErrUnsupportedProvider, provider)
	}
	if providerEntry.status != providerAvailable || !providerEntry.enabled {
		return VerifiedSessionSettings{}, fmt.Errorf("%w: %q", ErrProviderUnavailable, provider)
	}

	models, err := client.readModelCatalog(ctx, provider)
	if err != nil {
		return VerifiedSessionSettings{}, err
	}
	modelEntry, exists := models[model]
	if !exists {
		return VerifiedSessionSettings{}, fmt.Errorf("%w: %q", ErrModelNotFound, model)
	}
	if hasReasoning && !containsCatalogValue(modelEntry.reasoning, reasoning) {
		return VerifiedSessionSettings{}, fmt.Errorf("%w: %q", ErrReasoningNotFound, reasoning)
	}

	return VerifiedSessionSettings{
		provider:     provider,
		model:        model,
		reasoning:    reasoning,
		hasReasoning: hasReasoning,
		mode:         mode,
	}, nil
}

func containsCatalogValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
