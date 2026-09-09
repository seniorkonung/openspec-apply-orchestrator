//go:build paseo_integration

package paseo

import (
	"context"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

func compatibleFullAccessMode(
	environment CompatibleEnvironment,
	provider string,
) (paseocli.FullAccessMode, bool) {
	if mode, supported := environment.contract.FullAccessMode(provider); supported {
		return mode, true
	}
	return paseocli.IntegrationFullAccessMode(provider)
}

// CreateOwnSessionForIntegration сохраняет узкий стенд Phase 1 на управляемом
// ACP-провайдере, у которого Paseo не публикует режимы в каталоге.
// Производственная сборка этого обхода не содержит.
func (gateway *ReconcileGateway) CreateOwnSessionForIntegration(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspaceID orchestrator.WorkspaceID,
	cwd string,
	provider string,
	model string,
	prompt string,
) (orchestrator.SessionID, error) {
	return gateway.createOwnSession(
		ctx,
		change,
		workspaceID,
		cwd,
		runSessionSettings{provider: provider, model: model},
		prompt,
	)
}
