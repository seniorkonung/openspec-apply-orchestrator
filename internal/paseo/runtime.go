package paseo

import (
	"context"
	"net/url"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

// Runtime предоставляет потребителю проверенные операции Paseo, не раскрывая
// сборку клиента, совместимой среды и gateway сопровождения.
type Runtime struct {
	client      *Client
	environment CompatibleEnvironment
	gateway     *ReconcileGateway
}

func NewRuntime(ctx context.Context) (*Runtime, error) {
	client, err := NewClient()
	if err != nil {
		return nil, err
	}
	environment, err := client.CheckCompatibility(ctx)
	if err != nil {
		return nil, err
	}
	gateway, err := NewReconcileGateway(client, environment)
	if err != nil {
		return nil, err
	}
	return &Runtime{client: client, environment: environment, gateway: gateway}, nil
}

func (runtime *Runtime) ServerID() string {
	return runtime.environment.ServerID().String()
}

func (runtime *Runtime) SessionLink(session orchestrator.SessionID) string {
	return "paseo://h/" + url.PathEscape(runtime.ServerID()) + "/agent/" + url.PathEscape(session.String())
}

func (runtime *Runtime) FindActiveWorkspace(
	ctx context.Context,
	change orchestrator.ChangeKey,
	cwd string,
) (orchestrator.ManagedWorkspaceObservation, error) {
	return runtime.gateway.FindActiveWorkspace(ctx, change, cwd)
}

func (runtime *Runtime) FindOwnSessions(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) (orchestrator.OwnSessionObservation, error) {
	return runtime.gateway.FindOwnSessions(ctx, change, workspace, cwd)
}

func (runtime *Runtime) ObserveOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
	session orchestrator.SessionID,
) (orchestrator.OwnSessionObservation, error) {
	return runtime.gateway.ObserveOwnSession(ctx, change, workspace, cwd, session)
}

func (runtime *Runtime) CreateWorkspace(
	ctx context.Context,
	change orchestrator.ChangeKey,
	cwd string,
) error {
	return runtime.gateway.CreateWorkspace(ctx, change, cwd)
}

func (runtime *Runtime) CreateOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
	settings VerifiedSessionSettings,
	prompt prompts.CommitPreparationPrompt,
) (orchestrator.SessionID, error) {
	return runtime.gateway.CreateOwnSession(ctx, change, workspace, cwd, settings, prompt)
}

func (runtime *Runtime) WaitOwnSession(
	ctx context.Context,
	session orchestrator.SessionID,
) error {
	return runtime.gateway.WaitOwnSession(ctx, session)
}

func (runtime *Runtime) ArchiveOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
	session orchestrator.ManagedSession,
) error {
	return runtime.gateway.ArchiveOwnSession(ctx, change, workspace, cwd, session)
}

func (runtime *Runtime) VerifySessionSettings(
	ctx context.Context,
	settings UntrustedSessionSettings,
) (VerifiedSessionSettings, error) {
	return runtime.client.VerifySessionSettings(ctx, runtime.environment, settings)
}
