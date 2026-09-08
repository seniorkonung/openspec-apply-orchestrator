package paseo

import (
	"context"
	"fmt"
	"strings"
)

type providerStatus uint8

const (
	providerAvailable providerStatus = iota + 1
	providerLoading
	providerError
	providerUnavailable
)

type providerCatalogEntry struct {
	status  providerStatus
	enabled bool
}

type modelCatalogEntry struct {
	reasoning []string
}

// Схема соответствует JSON команды provider ls Paseo CLI 0.7.2.
// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/provider/ls.ts
type providerCatalogItemJSON struct {
	Provider    requiredValue[string] `json:"provider"`
	Label       requiredValue[string] `json:"label"`
	Status      requiredValue[string] `json:"status"`
	Enabled     requiredValue[string] `json:"enabled"`
	DefaultMode requiredValue[string] `json:"defaultMode"`
	Modes       requiredValue[string] `json:"modes"`
}

// Схема соответствует JSON команды provider models --thinking Paseo CLI 0.7.2.
// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/provider/models.ts
type modelCatalogItemJSON struct {
	Model                   requiredValue[string]    `json:"model"`
	ID                      requiredValue[string]    `json:"id"`
	Description             requiredValue[string]    `json:"description"`
	ThinkingOptionIDs       requiredValue[[]string]  `json:"thinkingOptionIds"`
	DefaultThinkingOptionID requiredNullable[string] `json:"defaultThinkingOptionId"`
	ThinkingOptions         requiredValue[string]    `json:"thinkingOptions"`
}

func (client *Client) readProviderCatalog(ctx context.Context) (map[string]providerCatalogEntry, error) {
	output, err := client.runner.run(ctx, command{
		name: "provider ls",
		args: []string{"provider", "ls", "--json"},
	})
	if err != nil {
		return nil, err
	}
	return decodeProviderCatalog(output)
}

func (client *Client) readModelCatalog(
	ctx context.Context,
	provider string,
) (map[string]modelCatalogEntry, error) {
	if !validCatalogIdentifier(provider) {
		return nil, ErrInvalidSessionSettings
	}
	output, err := client.runner.run(ctx, command{
		name: "provider models",
		args: []string{"provider", "models", provider, "--thinking", "--json"},
	})
	if err != nil {
		return nil, err
	}
	return decodeModelCatalog(output)
}

func decodeProviderCatalog(output []byte) (map[string]providerCatalogEntry, error) {
	var raw []providerCatalogItemJSON
	if err := decodeStrictJSON(output, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrUnexpectedJSON
	}

	providers := make(map[string]providerCatalogEntry, len(raw))
	for index, item := range raw {
		if !item.Provider.present || !item.Label.present || !item.Status.present ||
			!item.Enabled.present || !item.DefaultMode.present || !item.Modes.present {
			return nil, fmt.Errorf("%w: provider %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validCatalogIdentifier(item.Provider.value) || !validOpaqueValue(item.Label.value) ||
			!validOpaqueValue(item.DefaultMode.value) || !validOptionalOpaqueValue(item.Modes.value) {
			return nil, fmt.Errorf("%w: provider %d содержит некорректное поле", ErrUnexpectedJSON, index+1)
		}
		status, err := parseProviderStatus(item.Status.value)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %d содержит некорректный status", ErrUnexpectedJSON, index+1)
		}
		enabled, err := parseProviderEnabled(item.Enabled.value)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %d содержит некорректный enabled", ErrUnexpectedJSON, index+1)
		}
		if _, exists := providers[item.Provider.value]; exists {
			return nil, fmt.Errorf("%w: provider %q повторяется", ErrUnexpectedJSON, item.Provider.value)
		}
		providers[item.Provider.value] = providerCatalogEntry{status: status, enabled: enabled}
	}
	return providers, nil
}

func decodeModelCatalog(output []byte) (map[string]modelCatalogEntry, error) {
	var raw []modelCatalogItemJSON
	if err := decodeStrictJSON(output, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrUnexpectedJSON
	}

	models := make(map[string]modelCatalogEntry, len(raw))
	for index, item := range raw {
		if !item.Model.present || !item.ID.present || !item.Description.present ||
			!item.ThinkingOptionIDs.present || !item.DefaultThinkingOptionID.present ||
			!item.ThinkingOptions.present {
			return nil, fmt.Errorf("%w: model %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validOpaqueValue(item.Model.value) || !validCatalogIdentifier(item.ID.value) ||
			!validOptionalOpaqueValue(item.Description.value) {
			return nil, fmt.Errorf("%w: model %d содержит некорректное поле", ErrUnexpectedJSON, index+1)
		}
		reasoning, err := validateReasoningOptions(item)
		if err != nil {
			return nil, fmt.Errorf("%w: model %d содержит некорректный reasoning", ErrUnexpectedJSON, index+1)
		}
		if _, exists := models[item.ID.value]; exists {
			return nil, fmt.Errorf("%w: model %q повторяется", ErrUnexpectedJSON, item.ID.value)
		}
		models[item.ID.value] = modelCatalogEntry{reasoning: reasoning}
	}
	return models, nil
}

func validateReasoningOptions(item modelCatalogItemJSON) ([]string, error) {
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

	presentation := "none"
	if len(reasoning) > 0 {
		presentation = strings.Join(reasoning, ", ")
	}
	if item.ThinkingOptions.value != presentation {
		return nil, ErrUnexpectedJSON
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

func parseProviderStatus(value string) (providerStatus, error) {
	switch value {
	case "available":
		return providerAvailable, nil
	case "loading":
		return providerLoading, nil
	case "error":
		return providerError, nil
	case "unavailable":
		return providerUnavailable, nil
	default:
		return 0, ErrUnexpectedJSON
	}
}

func parseProviderEnabled(value string) (bool, error) {
	switch value {
	case "Enabled":
		return true, nil
	case "Disabled":
		return false, nil
	default:
		return false, ErrUnexpectedJSON
	}
}

func validCatalogIdentifier(value string) bool {
	return validSessionSetting(value) && !strings.HasPrefix(value, "-")
}

func validOptionalOpaqueValue(value string) bool {
	return value == "" || validOpaqueValue(value)
}
