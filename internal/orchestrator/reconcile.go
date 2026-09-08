package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	ObserveOwnSession(context.Context, ChangeKey, WorkspaceID, string, SessionID) (OwnSessionObservation, error)
	CreateWorkspace(context.Context, ChangeKey, string) error
	CreateOwnSession(context.Context, ChangeKey, WorkspaceID, string) (SessionID, error)
	WaitOwnSession(context.Context, SessionID) error
	ArchiveOwnSession(context.Context, ChangeKey, WorkspaceID, string, ManagedSession) error
}

type PhaseOneReconciler struct {
	gateway PhaseOneGateway
}

func NewPhaseOneReconciler(gateway PhaseOneGateway) (*PhaseOneReconciler, error) {
	if gateway == nil {
		return nil, ErrInvalidReconciler
	}
	return &PhaseOneReconciler{gateway: gateway}, nil
}

func (reconciler *PhaseOneReconciler) Run(ctx context.Context, change ChangeKey, cwd string) error {
	if reconciler == nil || reconciler.gateway == nil || change.String() == "" || strings.TrimSpace(cwd) == "" {
		return ErrInvalidReconciler
	}

	var knownSession *SessionID
	for {
		action, err := reconciler.nextAction(ctx, change, cwd, knownSession)
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
			created, createErr := reconciler.gateway.CreateOwnSession(ctx, change, selected.workspace, cwd)
			if createErr == nil {
				if created.String() == "" {
					return fmt.Errorf("%w: создание вернуло пустой ID сессии", ErrUnexpectedObservation)
				}
				knownSession = &created
			}
			err = createErr
		case waitOwnSessionAction:
			known := selected.session
			knownSession = &known
			err = reconciler.gateway.WaitOwnSession(ctx, known)
		case archiveOwnSessionAction:
			known := selected.session.ID()
			knownSession = &known
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
	knownSession *SessionID,
) (phaseOneAction, error) {
	workspaces, err := reconciler.gateway.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return nil, err
	}

	switch observed := workspaces.(type) {
	case NoManagedWorkspace:
		if knownSession != nil {
			return stopPhaseOneAction{err: fmt.Errorf(
				"%w: workspace известной сессии %s больше не активен",
				ErrUnexpectedObservation,
				knownSession.String(),
			)}, nil
		}
		return createWorkspaceAction{}, nil
	case OneManagedWorkspace:
		if observed.ID.String() == "" {
			return stopPhaseOneAction{err: fmt.Errorf("%w: пустой ID workspace", ErrUnexpectedObservation)}, nil
		}
		var sessions OwnSessionObservation
		if knownSession == nil {
			sessions, err = reconciler.gateway.FindOwnSessions(ctx, change, observed.ID, cwd)
		} else {
			sessions, err = reconciler.gateway.ObserveOwnSession(
				ctx,
				change,
				observed.ID,
				cwd,
				*knownSession,
			)
		}
		if err != nil {
			return nil, err
		}
		if knownSession != nil {
			if err := validateKnownSessionObservation(*knownSession, sessions); err != nil {
				return stopPhaseOneAction{err: err}, nil
			}
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
		return waitOwnSessionAction{session: observed.Session.ID()}
	case OwnSessionAwaitingAction:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopPhaseOneAction{err: err}
		}
		switch observed.Reason {
		case SessionTurnFinished:
			return archiveOwnSessionAction{workspace: workspace, session: observed.Session}
		case SessionAgentError, SessionPermissionCompatibilityViolation:
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

func validateKnownSessionObservation(known SessionID, observation OwnSessionObservation) error {
	var observed SessionID
	switch session := observation.(type) {
	case WorkingOwnSession:
		observed = session.Session.ID()
	case OwnSessionAwaitingAction:
		observed = session.Session.ID()
	case ObservedOwnSessionClosed:
		observed = session.Session.ID()
	case AmbiguousOwnSessions:
		return nil
	case NoActiveOwnSession:
		return fmt.Errorf("%w: известная сессия %s исчезла без подтверждения", ErrUnexpectedObservation, known.String())
	default:
		return fmt.Errorf("%w: неизвестное наблюдение сессии %T", ErrUnexpectedObservation, observation)
	}
	if observed != known {
		return fmt.Errorf(
			"%w: ожидалась известная сессия %s, получена %s",
			ErrUnexpectedObservation,
			known.String(),
			observed.String(),
		)
	}
	return nil
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

type waitOwnSessionAction struct {
	session SessionID
}

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
