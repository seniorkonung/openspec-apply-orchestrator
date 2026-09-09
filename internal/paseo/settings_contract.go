//go:build !paseo_integration

package paseo

import "github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"

func compatibleFullAccessMode(
	environment CompatibleEnvironment,
	provider string,
) (paseocli.FullAccessMode, bool) {
	return environment.contract.FullAccessMode(provider)
}
