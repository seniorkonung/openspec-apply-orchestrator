//go:build paseo_integration

package paseocli

// IntegrationFullAccessMode возвращает тестовую подмену режима только для
// процессного стенда и не изменяет activeContract production-сборки.
func IntegrationFullAccessMode(provider string) (FullAccessMode, bool) {
	if provider != "oa-integration" {
		return FullAccessMode{}, false
	}
	return FullAccessMode{
		provider:       provider,
		id:             "integration-unrestricted",
		approvalPolicy: "never",
		sandbox:        "danger-full-access",
	}, true
}
