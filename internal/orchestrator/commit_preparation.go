package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type WorkingTreeObservation interface {
	isWorkingTreeObservation()
}

type CleanWorkingTree struct{}

func (CleanWorkingTree) isWorkingTreeObservation() {}

type DirtyWorkingTree struct{}

func (DirtyWorkingTree) isWorkingTreeObservation() {}

type CommitPreparationOutcome interface {
	isCommitPreparationOutcome()
}

type NoCommitPreparationNeeded struct{}

func (NoCommitPreparationNeeded) isCommitPreparationOutcome() {}

type CommitPreparationCompleted struct {
	session SessionID
}

func (CommitPreparationCompleted) isCommitPreparationOutcome() {}

func (outcome CommitPreparationCompleted) SessionID() SessionID {
	return outcome.session
}

type HumanInterventionRequired struct {
	session SessionID
	reason  SessionAttentionReason
}

func (HumanInterventionRequired) isCommitPreparationOutcome() {}

func (outcome HumanInterventionRequired) SessionID() SessionID {
	return outcome.session
}

func (outcome HumanInterventionRequired) Reason() SessionAttentionReason {
	return outcome.reason
}

type ClosedSessionWithChanges struct {
	session SessionID
}

func (ClosedSessionWithChanges) isCommitPreparationOutcome() {}

func (outcome ClosedSessionWithChanges) SessionID() SessionID {
	return outcome.session
}

type SessionCreationFunc func(
	context.Context,
	ChangeKey,
	WorkspaceID,
	string,
) (SessionID, error)

type PreparedSessionCreation struct {
	create SessionCreationFunc
}

func NewPreparedSessionCreation(create SessionCreationFunc) PreparedSessionCreation {
	return PreparedSessionCreation{create: create}
}

func (prepared PreparedSessionCreation) valid() bool {
	return prepared.create != nil
}

type CommitPreparationGateway interface {
	RefreshActiveChange(context.Context, ChangeKey, string) error
	FindActiveWorkspace(context.Context, ChangeKey, string) (ManagedWorkspaceObservation, error)
	FindOwnSessions(context.Context, ChangeKey, WorkspaceID, string) (OwnSessionObservation, error)
	ObserveOwnSession(context.Context, ChangeKey, WorkspaceID, string, SessionID) (OwnSessionObservation, error)
	ReadWorkingTree(context.Context, string) (WorkingTreeObservation, error)
	PrepareNewSession(context.Context, ChangeKey, string) (PreparedSessionCreation, error)
	CreateWorkspace(context.Context, ChangeKey, string) error
	WaitOwnSession(context.Context, SessionID) error
	ArchiveOwnSession(context.Context, ChangeKey, WorkspaceID, string, ManagedSession) error
}

type CommitPreparationReconciler struct {
	gateway CommitPreparationGateway
}

func NewCommitPreparationReconciler(
	gateway CommitPreparationGateway,
) (*CommitPreparationReconciler, error) {
	if gateway == nil {
		return nil, ErrInvalidReconciler
	}
	return &CommitPreparationReconciler{gateway: gateway}, nil
}

func (reconciler *CommitPreparationReconciler) Run(
	ctx context.Context,
	change ChangeKey,
	cwd string,
) (CommitPreparationOutcome, error) {
	if reconciler == nil || reconciler.gateway == nil || ctx == nil ||
		change.String() == "" || strings.TrimSpace(cwd) == "" {
		return nil, ErrInvalidReconciler
	}

	var knownSession *SessionID
	var prepared PreparedSessionCreation
	for {
		action, err := reconciler.nextAction(ctx, change, cwd, knownSession, prepared.valid())
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		switch selected := action.(type) {
		case prepareSessionCreationAction:
			prepared, err = reconciler.gateway.PrepareNewSession(ctx, change, cwd)
			if err == nil && !prepared.valid() {
				return nil, fmt.Errorf("%w: подготовка вернула пустые входы создания", ErrUnexpectedObservation)
			}
		case createCommitWorkspaceAction:
			err = reconciler.gateway.CreateWorkspace(ctx, change, cwd)
		case createCommitSessionAction:
			if !prepared.valid() {
				return nil, fmt.Errorf("%w: отсутствуют подготовленные входы создания", ErrUnexpectedObservation)
			}
			created, createErr := prepared.create(ctx, change, selected.workspace, cwd)
			if createErr == nil {
				if created.String() == "" {
					return nil, fmt.Errorf("%w: создание вернуло пустой ID сессии", ErrUnexpectedObservation)
				}
				known := created
				knownSession = &known
				prepared = PreparedSessionCreation{}
			}
			err = createErr
		case waitCommitSessionAction:
			known := selected.session
			knownSession = &known
			prepared = PreparedSessionCreation{}
			err = ClassifySourceReadError(
				ctx,
				ReadSourcePaseo,
				reconciler.gateway.WaitOwnSession(ctx, known),
			)
		case archiveCommitSessionAction:
			known := selected.session.ID()
			knownSession = &known
			prepared = PreparedSessionCreation{}
			err = reconciler.gateway.ArchiveOwnSession(
				ctx,
				change,
				selected.workspace,
				cwd,
				selected.session,
			)
			if err == nil {
				return CommitPreparationCompleted{session: known}, nil
			}
		case completeCommitPreparationAction:
			return selected.outcome, nil
		case stopCommitPreparationAction:
			return nil, selected.err
		default:
			return nil, fmt.Errorf("%w: неизвестное действие %T", ErrUnexpectedObservation, action)
		}

		if errors.Is(err, ErrReconcileObservationChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
}

func (reconciler *CommitPreparationReconciler) nextAction(
	ctx context.Context,
	change ChangeKey,
	cwd string,
	knownSession *SessionID,
	hasPreparedCreation bool,
) (commitPreparationAction, error) {
	if err := reconciler.gateway.RefreshActiveChange(ctx, change, cwd); err != nil {
		return nil, ClassifySourceReadError(ctx, ReadSourceOpenSpec, err)
	}
	workspaces, err := reconciler.gateway.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return nil, ClassifySourceReadError(ctx, ReadSourcePaseo, err)
	}

	switch observed := workspaces.(type) {
	case NoManagedWorkspace:
		if knownSession != nil {
			return stopCommitPreparationAction{err: fmt.Errorf(
				"%w: workspace известной сессии %s больше не активен",
				ErrUnexpectedObservation,
				knownSession.String(),
			)}, nil
		}
		return reconciler.selectAbsentSessionAction(ctx, cwd, WorkspaceID{}, hasPreparedCreation)
	case OneManagedWorkspace:
		if observed.ID.String() == "" {
			return stopCommitPreparationAction{err: fmt.Errorf(
				"%w: пустой ID workspace",
				ErrUnexpectedObservation,
			)}, nil
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
			return nil, ClassifySourceReadError(ctx, ReadSourcePaseo, err)
		}
		if knownSession != nil {
			if err := validateKnownSessionObservation(*knownSession, sessions); err != nil {
				return stopCommitPreparationAction{err: err}, nil
			}
		}
		return reconciler.selectSessionAction(
			ctx,
			change,
			observed.ID,
			cwd,
			sessions,
			hasPreparedCreation,
		)
	case AmbiguousManagedWorkspaces:
		if len(observed.IDs) < 2 {
			return stopCommitPreparationAction{err: fmt.Errorf(
				"%w: неоднозначность без нескольких workspace",
				ErrUnexpectedObservation,
			)}, nil
		}
		ids, err := joinWorkspaceIDs(observed.IDs)
		if err != nil {
			return stopCommitPreparationAction{err: err}, nil
		}
		return stopCommitPreparationAction{err: fmt.Errorf(
			"%w: %s",
			ErrAmbiguousManagedWorkspaces,
			ids,
		)}, nil
	default:
		return stopCommitPreparationAction{err: fmt.Errorf(
			"%w: неизвестное наблюдение workspace %T",
			ErrUnexpectedObservation,
			workspaces,
		)}, nil
	}
}

func (reconciler *CommitPreparationReconciler) selectSessionAction(
	ctx context.Context,
	change ChangeKey,
	workspace WorkspaceID,
	cwd string,
	observation OwnSessionObservation,
	hasPreparedCreation bool,
) (commitPreparationAction, error) {
	switch observed := observation.(type) {
	case NoActiveOwnSession:
		return reconciler.selectAbsentSessionAction(ctx, cwd, workspace, hasPreparedCreation)
	case WorkingOwnSession:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopCommitPreparationAction{err: err}, nil
		}
		return waitCommitSessionAction{session: observed.Session.ID()}, nil
	case OwnSessionAwaitingAction:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopCommitPreparationAction{err: err}, nil
		}
		switch observed.Reason {
		case SessionTurnFinished:
			state, err := reconciler.readWorkingTree(ctx, cwd)
			if err != nil {
				return nil, err
			}
			switch state.(type) {
			case CleanWorkingTree:
				return archiveCommitSessionAction{workspace: workspace, session: observed.Session}, nil
			case DirtyWorkingTree:
				return completeCommitPreparationAction{outcome: HumanInterventionRequired{
					session: observed.Session.ID(),
					reason:  observed.Reason,
				}}, nil
			}
		case SessionAgentError, SessionPermissionCompatibilityViolation:
			return completeCommitPreparationAction{outcome: HumanInterventionRequired{
				session: observed.Session.ID(),
				reason:  observed.Reason,
			}}, nil
		default:
			return stopCommitPreparationAction{err: fmt.Errorf(
				"%w: неизвестная причина ожидания",
				ErrUnexpectedObservation,
			)}, nil
		}
	case ObservedOwnSessionClosed:
		if err := validateReconcileSession(change, workspace, observed.Session); err != nil {
			return stopCommitPreparationAction{err: err}, nil
		}
		state, err := reconciler.readWorkingTree(ctx, cwd)
		if err != nil {
			return nil, err
		}
		switch state.(type) {
		case CleanWorkingTree:
			return completeCommitPreparationAction{outcome: CommitPreparationCompleted{
				session: observed.Session.ID(),
			}}, nil
		case DirtyWorkingTree:
			return completeCommitPreparationAction{outcome: ClosedSessionWithChanges{
				session: observed.Session.ID(),
			}}, nil
		}
	case AmbiguousOwnSessions:
		if len(observed.Sessions) < 2 {
			return stopCommitPreparationAction{err: fmt.Errorf(
				"%w: неоднозначность без нескольких сессий",
				ErrUnexpectedObservation,
			)}, nil
		}
		ids := make([]string, 0, len(observed.Sessions))
		for _, session := range observed.Sessions {
			if err := validateReconcileSession(change, workspace, session); err != nil {
				return stopCommitPreparationAction{err: err}, nil
			}
			ids = append(ids, session.ID().String())
		}
		return stopCommitPreparationAction{err: fmt.Errorf(
			"%w: %s",
			ErrAmbiguousOwnSessions,
			strings.Join(ids, ", "),
		)}, nil
	default:
		return stopCommitPreparationAction{err: fmt.Errorf(
			"%w: неизвестное наблюдение сессии %T",
			ErrUnexpectedObservation,
			observation,
		)}, nil
	}

	return stopCommitPreparationAction{err: fmt.Errorf(
		"%w: неизвестное состояние рабочего Git",
		ErrUnexpectedObservation,
	)}, nil
}

func (reconciler *CommitPreparationReconciler) selectAbsentSessionAction(
	ctx context.Context,
	cwd string,
	workspace WorkspaceID,
	hasPreparedCreation bool,
) (commitPreparationAction, error) {
	state, err := reconciler.readWorkingTree(ctx, cwd)
	if err != nil {
		return nil, err
	}
	switch state.(type) {
	case CleanWorkingTree:
		return completeCommitPreparationAction{outcome: NoCommitPreparationNeeded{}}, nil
	case DirtyWorkingTree:
		if !hasPreparedCreation {
			return prepareSessionCreationAction{}, nil
		}
		if workspace.String() == "" {
			return createCommitWorkspaceAction{}, nil
		}
		return createCommitSessionAction{workspace: workspace}, nil
	default:
		return stopCommitPreparationAction{err: fmt.Errorf(
			"%w: неизвестное состояние рабочего Git %T",
			ErrUnexpectedObservation,
			state,
		)}, nil
	}
}

func (reconciler *CommitPreparationReconciler) readWorkingTree(
	ctx context.Context,
	cwd string,
) (WorkingTreeObservation, error) {
	state, err := reconciler.gateway.ReadWorkingTree(ctx, cwd)
	if err != nil {
		return nil, ClassifySourceReadError(ctx, ReadSourceGit, err)
	}
	switch state.(type) {
	case CleanWorkingTree, DirtyWorkingTree:
		return state, nil
	default:
		return nil, fmt.Errorf(
			"%w: неизвестное состояние рабочего Git %T",
			ErrUnexpectedObservation,
			state,
		)
	}
}

type commitPreparationAction interface {
	isCommitPreparationAction()
}

type prepareSessionCreationAction struct{}

func (prepareSessionCreationAction) isCommitPreparationAction() {}

type createCommitWorkspaceAction struct{}

func (createCommitWorkspaceAction) isCommitPreparationAction() {}

type createCommitSessionAction struct {
	workspace WorkspaceID
}

func (createCommitSessionAction) isCommitPreparationAction() {}

type waitCommitSessionAction struct {
	session SessionID
}

func (waitCommitSessionAction) isCommitPreparationAction() {}

type archiveCommitSessionAction struct {
	workspace WorkspaceID
	session   ManagedSession
}

func (archiveCommitSessionAction) isCommitPreparationAction() {}

type completeCommitPreparationAction struct {
	outcome CommitPreparationOutcome
}

func (completeCommitPreparationAction) isCommitPreparationAction() {}

type stopCommitPreparationAction struct {
	err error
}

func (stopCommitPreparationAction) isCommitPreparationAction() {}
