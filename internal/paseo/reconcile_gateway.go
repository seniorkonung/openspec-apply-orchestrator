package paseo

import (
	"context"
	"errors"
	"fmt"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

var ErrInvalidReconcileGateway = errors.New("некорректная конфигурация gateway сопровождения")

type ReconcileGateway struct {
	client      *Client
	environment CompatibleEnvironment
}

func NewReconcileGateway(
	client *Client,
	environment CompatibleEnvironment,
) (*ReconcileGateway, error) {
	gateway := &ReconcileGateway{
		client:      client,
		environment: environment,
	}
	if err := gateway.validate(); err != nil {
		return nil, err
	}
	return gateway, nil
}

func (gateway *ReconcileGateway) FindActiveWorkspace(
	ctx context.Context,
	change orchestrator.ChangeKey,
	cwd string,
) (orchestrator.ManagedWorkspaceObservation, error) {
	if err := gateway.validate(); err != nil {
		return nil, err
	}
	observation, err := gateway.client.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return nil, err
	}

	switch observed := observation.(type) {
	case NoActiveWorkspace:
		return orchestrator.NoManagedWorkspace{}, nil
	case OneActiveWorkspace:
		return orchestrator.OneManagedWorkspace{ID: observed.Workspace.ID()}, nil
	case AmbiguousActiveWorkspaces:
		ids := make([]orchestrator.WorkspaceID, 0, len(observed.Workspaces))
		for _, workspace := range observed.Workspaces {
			ids = append(ids, workspace.ID())
		}
		return orchestrator.AmbiguousManagedWorkspaces{IDs: ids}, nil
	default:
		return nil, fmt.Errorf("%w: неизвестное наблюдение workspace %T", ErrUnexpectedJSON, observation)
	}
}

func (gateway *ReconcileGateway) FindOwnSessions(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) (orchestrator.OwnSessionObservation, error) {
	if err := gateway.validate(); err != nil {
		return nil, err
	}
	return gateway.client.FindOwnSessions(ctx, change, workspace, cwd)
}

func (gateway *ReconcileGateway) ObserveOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
	session orchestrator.SessionID,
) (orchestrator.OwnSessionObservation, error) {
	if err := gateway.validate(); err != nil {
		return nil, err
	}
	return gateway.client.ObserveOwnSession(ctx, change, workspace, cwd, session)
}

func (gateway *ReconcileGateway) CreateWorkspace(
	ctx context.Context,
	change orchestrator.ChangeKey,
	cwd string,
) error {
	if err := gateway.validate(); err != nil {
		return err
	}
	workspaces, err := gateway.client.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return orchestrator.ClassifySourceReadError(ctx, orchestrator.ReadSourcePaseo, err)
	}
	if _, absent := workspaces.(NoActiveWorkspace); !absent {
		return orchestrator.ErrReconcileObservationChanged
	}
	_, err = gateway.client.CreateWorkspace(ctx, gateway.environment, change, cwd)
	return err
}

func (gateway *ReconcileGateway) CreateOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspaceID orchestrator.WorkspaceID,
	cwd string,
	settings VerifiedSessionSettings,
	prompt prompts.CommitPreparationPrompt,
) (orchestrator.SessionID, error) {
	if err := gateway.validate(); err != nil {
		return orchestrator.SessionID{}, err
	}
	if err := validateVerifiedSessionSettings(gateway.environment, settings); err != nil {
		return orchestrator.SessionID{}, ErrInvalidSessionSettings
	}
	return gateway.createOwnSession(
		ctx,
		change,
		workspaceID,
		cwd,
		settings.runSettings(),
		prompt.Text(),
	)
}

func (gateway *ReconcileGateway) createOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspaceID orchestrator.WorkspaceID,
	cwd string,
	settings runSessionSettings,
	prompt string,
) (orchestrator.SessionID, error) {
	if err := gateway.validate(); err != nil {
		return orchestrator.SessionID{}, err
	}
	workspace, err := gateway.findFreshWorkspace(ctx, change, workspaceID, cwd)
	if err != nil {
		return orchestrator.SessionID{}, err
	}
	sessions, err := gateway.client.FindOwnSessions(ctx, change, workspaceID, cwd)
	if err != nil {
		return orchestrator.SessionID{}, orchestrator.ClassifySourceReadError(
			ctx,
			orchestrator.ReadSourcePaseo,
			err,
		)
	}
	if _, absent := sessions.(orchestrator.NoActiveOwnSession); !absent {
		return orchestrator.SessionID{}, orchestrator.ErrReconcileObservationChanged
	}
	return gateway.client.createOwnSession(
		ctx,
		gateway.environment,
		change,
		workspace,
		settings,
		prompt,
	)
}

func (gateway *ReconcileGateway) WaitOwnSession(
	ctx context.Context,
	session orchestrator.SessionID,
) error {
	if err := gateway.validate(); err != nil {
		return err
	}
	if session.String() == "" {
		return ErrInvalidWaitSessionID
	}
	_, err := gateway.client.Wait(ctx, session)
	return err
}

func (gateway *ReconcileGateway) ArchiveOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspaceID orchestrator.WorkspaceID,
	cwd string,
	session orchestrator.ManagedSession,
) error {
	if err := gateway.validate(); err != nil {
		return err
	}
	if session.ID().String() == "" || session.ChangeKey() != change || session.WorkspaceID() != workspaceID {
		return ErrInvalidDirectoryQuery
	}
	workspace, err := gateway.findFreshWorkspace(ctx, change, workspaceID, cwd)
	if err != nil {
		return err
	}
	sessions, err := gateway.client.ObserveOwnSession(ctx, change, workspaceID, cwd, session.ID())
	if err != nil {
		return orchestrator.ClassifySourceReadError(ctx, orchestrator.ReadSourcePaseo, err)
	}
	switch observed := sessions.(type) {
	case orchestrator.ObservedOwnSessionClosed:
		if observed.Session.ID() == session.ID() {
			return nil
		}
		return orchestrator.ErrReconcileObservationChanged
	case orchestrator.OwnSessionAwaitingAction:
		if observed.Reason != orchestrator.SessionTurnFinished || observed.Session.ID() != session.ID() {
			return orchestrator.ErrReconcileObservationChanged
		}
	default:
		return orchestrator.ErrReconcileObservationChanged
	}
	if err := gateway.client.ArchiveOwnSession(ctx, gateway.environment, workspace, session); err != nil {
		if errors.Is(err, ErrSessionStillRunning) {
			return fmt.Errorf("%w: %w", orchestrator.ErrReconcileObservationChanged, err)
		}
		return err
	}
	return nil
}

func (gateway *ReconcileGateway) findFreshWorkspace(
	ctx context.Context,
	change orchestrator.ChangeKey,
	expected orchestrator.WorkspaceID,
	cwd string,
) (ActiveWorkspace, error) {
	if expected.String() == "" {
		return ActiveWorkspace{}, ErrInvalidDirectoryQuery
	}
	workspaces, err := gateway.client.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return ActiveWorkspace{}, orchestrator.ClassifySourceReadError(
			ctx,
			orchestrator.ReadSourcePaseo,
			err,
		)
	}
	one, ok := workspaces.(OneActiveWorkspace)
	if !ok || one.Workspace.ID() != expected {
		return ActiveWorkspace{}, orchestrator.ErrReconcileObservationChanged
	}
	return one.Workspace, nil
}

func (gateway *ReconcileGateway) validate() error {
	if gateway == nil || gateway.client == nil || gateway.client.runner == nil {
		return ErrInvalidReconcileGateway
	}
	if err := validateCompatibleEnvironment(gateway.environment); err != nil {
		return err
	}
	return nil
}
