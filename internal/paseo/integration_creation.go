//go:build paseo_integration

package paseo

import (
	"context"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

func init() {
	supplementalFullAccessMode = integrationFullAccessMode
}

func integrationFullAccessMode(key compatibilityKey) (fullAccessMode, bool) {
	// Отдельное сопоставление существует только в сборке интеграционного стенда:
	// производственный контракт codex/full-access остаётся закрытым и неизменным.
	if key == (compatibilityKey{version: "0.7.2", provider: "oa-integration"}) {
		return fullAccessMode{
			id: "integration-unrestricted", approvalPolicy: "never", sandbox: "danger-full-access",
		}, true
	}
	return fullAccessMode{}, false
}

// CreateOwnSessionForIntegration сохраняет узкий стенд Phase 1 на управляемом
// ACP-провайдере, у которого Paseo 0.7.2 не публикует режимы в каталоге.
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
