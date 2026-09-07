package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidReconciler           = errors.New("некорректная конфигурация ядра сопровождения")
	ErrUnexpectedObservation       = errors.New("некорректное наблюдение сопровождения")
	ErrAmbiguousManagedWorkspaces  = errors.New("обнаружено несколько workspace выбранного change")
	ErrAmbiguousOwnSessions        = errors.New("обнаружено несколько активных собственных сессий")
	ErrSessionNeedsAction          = errors.New("собственная сессия требует участия человека")
	ErrReconcileObservationChanged = errors.New("основание воздействия изменилось")
)

type ManagedWorkspaceObservation interface {
	isManagedWorkspaceObservation()
}

type NoManagedWorkspace struct{}

func (NoManagedWorkspace) isManagedWorkspaceObservation() {}

type OneManagedWorkspace struct {
	ID WorkspaceID
}

func (OneManagedWorkspace) isManagedWorkspaceObservation() {}

type AmbiguousManagedWorkspaces struct {
	IDs []WorkspaceID
}

func (AmbiguousManagedWorkspaces) isManagedWorkspaceObservation() {}

type PhaseOneGateway interface {
	FindActiveWorkspace(context.Context, ChangeKey, string) (ManagedWorkspaceObservation, error)
	FindOwnSessions(context.Context, ChangeKey, WorkspaceID, string) (OwnSessionObservation, error)
	CreateWorkspace(context.Context, ChangeKey, string) error
	CreateOwnSession(context.Context, ChangeKey, WorkspaceID, string) error
	ArchiveOwnSession(context.Context, ChangeKey, WorkspaceID, string, ManagedSession) error
}

type ReconcileClock interface {
	Wait(context.Context, time.Duration) error
}

type SystemClock struct{}

func (SystemClock) Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type PhaseOneReconciler struct {
	gateway      PhaseOneGateway
	clock        ReconcileClock
	pollInterval time.Duration
}

func NewPhaseOneReconciler(
	gateway PhaseOneGateway,
	clock ReconcileClock,
	pollInterval time.Duration,
) (*PhaseOneReconciler, error) {
	if gateway == nil || clock == nil || pollInterval <= 0 {
		return nil, ErrInvalidReconciler
	}
	return &PhaseOneReconciler{gateway: gateway, clock: clock, pollInterval: pollInterval}, nil
}

func (reconciler *PhaseOneReconciler) Run(ctx context.Context, change ChangeKey, cwd string) error {
	if reconciler == nil || reconciler.gateway == nil || reconciler.clock == nil ||
		reconciler.pollInterval <= 0 || change.String() == "" || strings.TrimSpace(cwd) == "" {
		return ErrInvalidReconciler
	}

	for {
		action, err := reconciler.nextAction(ctx, change, cwd)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		switch selected := action.(type) {
		case createWorkspaceAction:
			err = reconciler.gateway.CreateWorkspace(ctx, change, cwd)
		case createOwnSessionAction:
			err = reconciler.gateway.CreateOwnSession(ctx, change, selected.workspace, cwd)
		case waitOwnSessionAction:
			err = reconciler.clock.Wait(ctx, reconciler.pollInterval)
		case archiveOwnSessionAction:
			err = reconciler.gateway.ArchiveOwnSession(ctx, change, selected.workspace, cwd, selected.session)
			if err == nil {
				return nil
			}
		case completePhaseOneAction:
			return nil
		case stopPhaseOneAction:
			return selected.err
		default:
			return fmt.Errorf("%w: неизвестное действие %T", ErrUnexpectedObservation, action)
		}
		if errors.Is(err, ErrReconcileObservationChanged) {
			continue
		}
		if err != nil {
			return err
		}
	}
}

func (reconciler *PhaseOneReconciler) nextAction(
	ctx context.Context,
	change ChangeKey,
	cwd string,
) (phaseOneAction, error) {
	workspaces, err := reconciler.gateway.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return nil, err
	}

	switch observed := workspaces.(type) {
	case NoManagedWorkspace:
		return createWorkspaceAction{}, nil
	case OneManagedWorkspace:
		if observed.ID.String() == "" {
			return stopPhaseOneAction{err: fmt.Errorf("%w: пустой ID workspace", ErrUnexpectedObservation)}, nil
		}
		sessions, err := reconciler.gateway.FindOwnSessions(ctx, change, observed.ID, cwd)
		if err != nil {
			return nil, err
		}
		return selectSessionAction(change, observed.ID, sessions), nil
	case AmbiguousManagedWorkspaces:
		if len(observed.IDs) < 2 {
			return stopPhaseOneAction{err: fmt.Errorf("%w: неоднозначность без нескольких workspace", ErrUnexpectedObservation)}, nil
		}
		ids, err := joinWorkspaceIDs(observed.IDs)
		if err != nil {
			return stopPhaseOneAction{err: err}, nil
		}
		return stopPhaseOneAction{err: fmt.Errorf("%w: %s", ErrAmbiguousManagedWorkspaces, ids)}, nil
	default:
		return stopPhaseOneAction{err: fmt.Errorf("%w: неизвестное наблюдение workspace %T", ErrUnexpectedObservation, workspaces)}, nil
	}
}

func selectSessionAction(
	change ChangeKey,
	workspace WorkspaceID,
	observation OwnSessionObservation,
) phaseOneAction {
	switch observed := observation.(type) {
	case NoActiveOwnSession:
		return createOwnSessionAction{workspace: workspace}
	case WorkingOwnSession:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopPhaseOneAction{err: err}
		}
		return waitOwnSessionAction{}
	case OwnSessionAwaitingAction:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopPhaseOneAction{err: err}
		}
		switch observed.Reason {
		case SessionTurnFinished:
			return archiveOwnSessionAction{workspace: workspace, session: observed.Session}
		case SessionAgentError, SessionPermissionRequested:
			return stopPhaseOneAction{err: fmt.Errorf("%w: %s", ErrSessionNeedsAction, observed.Session.ID().String())}
		default:
			return stopPhaseOneAction{err: fmt.Errorf("%w: неизвестная причина ожидания", ErrUnexpectedObservation)}
		}
	case ObservedOwnSessionClosed:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopPhaseOneAction{err: err}
		}
		return completePhaseOneAction{}
	case AmbiguousOwnSessions:
		if len(observed.Sessions) < 2 {
			return stopPhaseOneAction{err: fmt.Errorf("%w: неоднозначность без нескольких сессий", ErrUnexpectedObservation)}
		}
		ids := make([]string, 0, len(observed.Sessions))
		for _, session := range observed.Sessions {
			if err := validateReconcileSession(change, workspace, session); err != nil {
				return stopPhaseOneAction{err: err}
			}
			ids = append(ids, session.ID().String())
		}
		return stopPhaseOneAction{err: fmt.Errorf("%w: %s", ErrAmbiguousOwnSessions, strings.Join(ids, ", "))}
	default:
		return stopPhaseOneAction{err: fmt.Errorf("%w: неизвестное наблюдение сессии %T", ErrUnexpectedObservation, observation)}
	}
}

func validateReconcileSession(change ChangeKey, workspace WorkspaceID, session ManagedSession) error {
	if session.ID().String() == "" || session.ChangeKey() != change || session.WorkspaceID() != workspace {
		return fmt.Errorf("%w: собственная сессия не соответствует workspace или change", ErrUnexpectedObservation)
	}
	return nil
}

func joinWorkspaceIDs(ids []WorkspaceID) (string, error) {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		if id.String() == "" {
			return "", fmt.Errorf("%w: пустой ID workspace", ErrUnexpectedObservation)
		}
		values = append(values, id.String())
	}
	return strings.Join(values, ", "), nil
}

type phaseOneAction interface {
	isPhaseOneAction()
}

type createWorkspaceAction struct{}

func (createWorkspaceAction) isPhaseOneAction() {}

type createOwnSessionAction struct {
	workspace WorkspaceID
}

func (createOwnSessionAction) isPhaseOneAction() {}

type waitOwnSessionAction struct{}

func (waitOwnSessionAction) isPhaseOneAction() {}

type archiveOwnSessionAction struct {
	workspace WorkspaceID
	session   ManagedSession
}

func (archiveOwnSessionAction) isPhaseOneAction() {}

type completePhaseOneAction struct{}

func (completePhaseOneAction) isPhaseOneAction() {}

type stopPhaseOneAction struct {
	err error
}

func (stopPhaseOneAction) isPhaseOneAction() {}
