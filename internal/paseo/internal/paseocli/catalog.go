package paseocli

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const maxCatalogIdentifierLength = 256

// CatalogProvider — минимальная проверенная проекция provider для доменного слоя.
type CatalogProvider struct {
	id        string
	available bool
}

func (provider CatalogProvider) ID() string {
	return provider.id
}

func (provider CatalogProvider) Available() bool {
	return provider.available
}

// CatalogModel — минимальная проверенная проекция model для доменного слоя.
type CatalogModel struct {
	id        string
	reasoning []string
}

func (model CatalogModel) ID() string {
	return model.id
}

func (model CatalogModel) Reasoning() []string {
	return slices.Clone(model.reasoning)
}

// Схема значимых полей JSON команды provider ls Paseo CLI 0.7.2.
// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/provider/ls.ts
type providerCatalogJSON struct {
	Provider requiredValue[string] `json:"provider"`
	Status   requiredValue[string] `json:"status"`
	Enabled  requiredValue[string] `json:"enabled"`
}

// Схема значимых полей JSON команды provider models --thinking Paseo CLI 0.7.2.
// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/provider/models.ts
type modelCatalogJSON struct {
	ID                      requiredValue[string]    `json:"id"`
	ThinkingOptionIDs       requiredValue[[]string]  `json:"thinkingOptionIds"`
	DefaultThinkingOptionID requiredNullable[string] `json:"defaultThinkingOptionId"`
}

func (adapter *Adapter) ListProviders(ctx context.Context) ([]CatalogProvider, error) {
	output, err := adapter.Run(ctx, Invocation{
		Name:      "provider ls",
		Arguments: []string{"provider", "ls", "--json"},
	})
	if err != nil {
		return nil, err
	}
	return decodeProviderCatalog(output)
}

func (adapter *Adapter) ListModels(ctx context.Context, provider string) ([]CatalogModel, error) {
	if !validCatalogIdentifier(provider) {
		return nil, ErrUnexpectedJSON
	}
	output, err := adapter.Run(ctx, Invocation{
		Name:      "provider models",
		Arguments: []string{"provider", "models", provider, "--thinking", "--json"},
	})
	if err != nil {
		return nil, err
	}
	return decodeModelCatalog(output)
}

func decodeProviderCatalog(output []byte) ([]CatalogProvider, error) {
	var raw []providerCatalogJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrUnexpectedJSON
	}

	providers := make([]CatalogProvider, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, item := range raw {
		if !item.Provider.present || !item.Status.present || !item.Enabled.present {
			return nil, fmt.Errorf("%w: provider %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validCatalogIdentifier(item.Provider.value) {
			return nil, fmt.Errorf("%w: provider %d содержит некорректный ID", ErrUnexpectedJSON, index+1)
		}
		available, err := parseProviderAvailability(item.Status.value, item.Enabled.value)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %d содержит некорректное состояние", ErrUnexpectedJSON, index+1)
		}
		if _, exists := seen[item.Provider.value]; exists {
			return nil, fmt.Errorf("%w: provider %q повторяется", ErrUnexpectedJSON, item.Provider.value)
		}
		seen[item.Provider.value] = struct{}{}
		providers = append(providers, CatalogProvider{id: item.Provider.value, available: available})
	}
	return providers, nil
}

func decodeModelCatalog(output []byte) ([]CatalogModel, error) {
	var raw []modelCatalogJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrUnexpectedJSON
	}

	models := make([]CatalogModel, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, item := range raw {
		if !item.ID.present || !item.ThinkingOptionIDs.present || !item.DefaultThinkingOptionID.present {
			return nil, fmt.Errorf("%w: model %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validCatalogIdentifier(item.ID.value) {
			return nil, fmt.Errorf("%w: model %d содержит некорректный ID", ErrUnexpectedJSON, index+1)
		}
		reasoning, err := validateReasoningOptions(item)
		if err != nil {
			return nil, fmt.Errorf("%w: model %d содержит некорректный reasoning", ErrUnexpectedJSON, index+1)
		}
		if _, exists := seen[item.ID.value]; exists {
			return nil, fmt.Errorf("%w: model %q повторяется", ErrUnexpectedJSON, item.ID.value)
		}
		seen[item.ID.value] = struct{}{}
		models = append(models, CatalogModel{id: item.ID.value, reasoning: reasoning})
	}
	return models, nil
}

func validateReasoningOptions(item modelCatalogJSON) ([]string, error) {
	seen := make(map[string]struct{}, len(item.ThinkingOptionIDs.value))
	reasoning := make([]string, 0, len(item.ThinkingOptionIDs.value))
	for _, option := range item.ThinkingOptionIDs.value {
		if !validCatalogIdentifier(option) {
			return nil, ErrUnexpectedJSON
		}
		if _, exists := seen[option]; exists {
			return nil, ErrUnexpectedJSON
		}
		seen[option] = struct{}{}
		reasoning = append(reasoning, option)
	}

	if item.DefaultThinkingOptionID.value != nil {
		if !validCatalogIdentifier(*item.DefaultThinkingOptionID.value) {
			return nil, ErrUnexpectedJSON
		}
		if _, exists := seen[*item.DefaultThinkingOptionID.value]; !exists {
			return nil, ErrUnexpectedJSON
		}
	}
	return reasoning, nil
}

func parseProviderAvailability(status, enabled string) (bool, error) {
	switch status {
	case "available", "loading", "error", "unavailable":
	default:
		return false, ErrUnexpectedJSON
	}
	switch enabled {
	case "Enabled", "Disabled":
	default:
		return false, ErrUnexpectedJSON
	}
	return status == "available" && enabled == "Enabled", nil
}

func validCatalogIdentifier(value string) bool {
	return len(value) <= maxCatalogIdentifierLength &&
		validIdentifierValue(value) && !strings.HasPrefix(value, "-")
}
