package paseo

import (
	"context"
	"strings"
)

type providerCatalogEntry struct {
	available bool
}

type modelCatalogEntry struct {
	reasoning []string
}

func (client *Client) readProviderCatalog(ctx context.Context) (map[string]providerCatalogEntry, error) {
	entries, err := client.adapter.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	providers := make(map[string]providerCatalogEntry, len(entries))
	for _, entry := range entries {
		providers[entry.ID()] = providerCatalogEntry{available: entry.Available()}
	}
	return providers, nil
}

func (client *Client) readModelCatalog(
	ctx context.Context,
	provider string,
) (map[string]modelCatalogEntry, error) {
	if !validCatalogIdentifier(provider) {
		return nil, ErrInvalidSessionSettings
	}
	entries, err := client.adapter.ListModels(ctx, provider)
	if err != nil {
		return nil, err
	}
	models := make(map[string]modelCatalogEntry, len(entries))
	for _, entry := range entries {
		models[entry.ID()] = modelCatalogEntry{reasoning: entry.Reasoning()}
	}
	return models, nil
}

func validCatalogIdentifier(value string) bool {
	return validSessionSetting(value) && !strings.HasPrefix(value, "-")
}
