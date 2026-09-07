package paseo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

var ErrInvalidReconcileGateway = errors.New("некорректная конфигурация gateway сопровождения")

type ReconcileGateway struct {
	client      *Client
	environment CompatibleEnvironment
	settings    SessionSettings
	prompt      string
}

var _ orchestrator.PhaseOneGateway = (*ReconcileGateway)(nil)

func NewReconcileGateway(
	client *Client,
	environment CompatibleEnvironment,
	settings SessionSettings,
	prompt string,
) (*ReconcileGateway, error) {
	gateway := &ReconcileGateway{
		client:      client,
		environment: environment,
		settings:    settings,
		prompt:      prompt,
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
		return err
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
) error {
	if err := gateway.validate(); err != nil {
		return err
	}
	workspace, err := gateway.findFreshWorkspace(ctx, change, workspaceID, cwd)
	if err != nil {
		return err
	}
	sessions, err := gateway.client.FindOwnSessions(ctx, change, workspaceID, cwd)
	if err != nil {
		return err
	}
	if _, absent := sessions.(orchestrator.NoActiveOwnSession); !absent {
		return orchestrator.ErrReconcileObservationChanged
	}
	_, err = gateway.client.CreateOwnSession(
		ctx,
		gateway.environment,
		change,
		workspace,
		gateway.settings,
		gateway.prompt,
	)
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
	sessions, err := gateway.client.FindOwnSessions(ctx, change, workspaceID, cwd)
	if err != nil {
		return err
	}
	switch observed := sessions.(type) {
	case orchestrator.ObservedOwnSessionClosed:
		if observed.Session.ID() == session.ID() {
			return nil
		}
		return orchestrator.ErrReconcileObservationChanged
	case orchestrator.NoActiveOwnSession:
		inspection, err := gateway.client.inspectManagedSession(ctx, workspace, session)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrArchiveNotConfirmed, err)
		}
		if inspection.Archived.value {
			return nil
		}
		return ErrArchiveNotConfirmed
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
		return ActiveWorkspace{}, err
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
	if !validSessionSetting(gateway.settings.provider) || !validSessionSetting(gateway.settings.model) ||
		(gateway.settings.thinking != "" && !validSessionSetting(gateway.settings.thinking)) ||
		(gateway.settings.mode != "" && !validSessionSetting(gateway.settings.mode)) {
		return ErrInvalidSessionSettings
	}
	if strings.TrimSpace(gateway.prompt) == "" || strings.IndexByte(gateway.prompt, 0) >= 0 {
		return ErrInvalidInitialPrompt
	}
	return nil
}
