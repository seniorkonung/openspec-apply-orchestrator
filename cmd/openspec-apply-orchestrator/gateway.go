package main

import (
	"context"
	"errors"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

type commandGateway struct {
	selection            changeSelection
	initialChange        resolvedChange
	changeSource         changeSource
	repository           workingTreeRepository
	paseo                paseoRuntime
	configuration        configurationSnapshot
	loadNewSessionInputs func(context.Context, configurationSnapshot, paseoRuntime) (newSessionInputs, error)
	delivery             *snapshotInterventionDelivery
	clock                waitClock
	reporter             *commandReporter
	reportedSessions     map[string]struct{}
}

func (gateway *commandGateway) RefreshActiveChange(ctx context.Context, expected orchestrator.ChangeKey, cwd string) error {
	current, err := gateway.changeSource.Resolve(ctx, gateway.selection)
	if err != nil {
		return err
	}
	currentKey, err := orchestrator.NewCommitPreparationChangeKey(orchestrator.CommitPreparationIdentity{
		WorkingTreeRoot:  gateway.repository.Root(),
		PlanningHomeRoot: current.planningHomeRoot,
		ChangeName:       current.name,
		ServerID:         gateway.paseo.ServerID(),
	})
	if err != nil {
		return err
	}
	if cwd != gateway.repository.Root() || currentKey != expected || current.changeRoot != gateway.initialChange.changeRoot {
		return errors.New("контекст выбранного OpenSpec change изменился")
	}
	gateway.reporter.openSpecRead()
	return nil
}

func (gateway *commandGateway) FindActiveWorkspace(ctx context.Context, change orchestrator.ChangeKey, cwd string) (orchestrator.ManagedWorkspaceObservation, error) {
	observation, err := gateway.paseo.FindActiveWorkspace(ctx, change, cwd)
	if err == nil {
		gateway.reporter.workspaceRead()
	}
	return observation, err
}

func (gateway *commandGateway) FindOwnSessions(ctx context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string) (orchestrator.OwnSessionObservation, error) {
	observation, err := gateway.paseo.FindOwnSessions(ctx, change, workspace, cwd)
	if err == nil {
		gateway.reporter.ownSessionsRead()
		gateway.delivery.observeSessionProgress(observation)
		gateway.reportRecoveredSession(observation)
	}
	return observation, err
}

func (gateway *commandGateway) ObserveOwnSession(ctx context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string, session orchestrator.SessionID) (orchestrator.OwnSessionObservation, error) {
	observation, err := gateway.paseo.ObserveOwnSession(ctx, change, workspace, cwd, session)
	if err == nil {
		gateway.reporter.ownSessionRead()
		gateway.delivery.observeSessionProgress(observation)
		gateway.reportRecoveredSession(observation)
	}
	return observation, err
}

func (gateway *commandGateway) ReadWorkingTree(ctx context.Context, cwd string) (orchestrator.WorkingTreeObservation, error) {
	if cwd != gateway.repository.Root() {
		return nil, errors.New("канонический рабочий Git-контекст изменился")
	}
	observation, err := gateway.repository.Read(ctx)
	if err == nil {
		gateway.reporter.workingTreeRead()
	}
	return observation, err
}

func (gateway *commandGateway) PrepareNewSession(ctx context.Context, _ orchestrator.ChangeKey, _ string) (orchestrator.PreparedSessionCreation, error) {
	gateway.reporter.checkingNewSessionInputs()
	if err := gateway.delivery.Resolve(); err != nil {
		return orchestrator.PreparedSessionCreation{}, err
	}
	inputs, err := gateway.loadNewSessionInputs(ctx, gateway.configuration, gateway.paseo)
	if err != nil {
		return orchestrator.PreparedSessionCreation{}, err
	}
	gateway.reporter.newSessionInputsChecked()
	return orchestrator.NewPreparedSessionCreation(func(
		ctx context.Context,
		change orchestrator.ChangeKey,
		workspace orchestrator.WorkspaceID,
		cwd string,
	) (orchestrator.SessionID, error) {
		gateway.reporter.creatingSession()
		created, err := gateway.paseo.CreateOwnSession(
			ctx,
			change,
			workspace,
			cwd,
			inputs.settings,
			inputs.prompt,
		)
		if err == nil {
			gateway.reportedSessions[created.String()] = struct{}{}
			gateway.reporter.sessionCreated(created)
		}
		return created, err
	}), nil
}

func (gateway *commandGateway) knownInterventionSession(session orchestrator.SessionID) (notify.KnownSession, error) {
	return notify.NewKnownSession(session.String(), gateway.paseo.SessionLink(session))
}

func (gateway *commandGateway) CreateWorkspace(ctx context.Context, change orchestrator.ChangeKey, cwd string) error {
	gateway.reporter.creatingWorkspace()
	err := gateway.paseo.CreateWorkspace(ctx, change, cwd)
	if err == nil {
		gateway.reporter.workspaceCreated()
	}
	return err
}

func (gateway *commandGateway) WaitOwnSession(ctx context.Context, session orchestrator.SessionID) error {
	gateway.reporter.waitingForTurn(session)
	finished := make(chan error, 1)
	go func() {
		finished <- gateway.paseo.WaitOwnSession(ctx, session)
	}()
	ticker := gateway.clock.NewTicker(waitProgressInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-finished:
			return err
		case <-ticker.C():
			gateway.reporter.waitHeartbeat(session)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (gateway *commandGateway) ArchiveOwnSession(ctx context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string, session orchestrator.ManagedSession) error {
	gateway.reporter.archivingSession(session.ID())
	return gateway.paseo.ArchiveOwnSession(ctx, change, workspace, cwd, session)
}

func (gateway *commandGateway) reportRecoveredSession(observation orchestrator.OwnSessionObservation) {
	var id orchestrator.SessionID
	switch observed := observation.(type) {
	case orchestrator.WorkingOwnSession:
		id = observed.Session.ID()
	case orchestrator.OwnSessionAwaitingAction:
		id = observed.Session.ID()
	case orchestrator.ObservedOwnSessionClosed:
		id = observed.Session.ID()
	default:
		return
	}
	if _, reported := gateway.reportedSessions[id.String()]; reported {
		return
	}
	gateway.reportedSessions[id.String()] = struct{}{}
	gateway.reporter.sessionRecovered(id)
}

type snapshotInterventionDelivery struct {
	snapshot configurationSnapshot
	build    interventionDeliveryFactory
	reporter *commandReporter
	resolved bool
	delivery notify.Deliverer
	err      error
	episode  notify.EpisodeKey
	hasEvent bool
	retrying bool
}

func newSnapshotInterventionDelivery(
	snapshot configurationSnapshot,
	build interventionDeliveryFactory,
	reporter *commandReporter,
) (*snapshotInterventionDelivery, error) {
	if snapshot == nil || build == nil || reporter == nil {
		return nil, errors.New("некорректная конфигурация доставки уведомлений")
	}
	return &snapshotInterventionDelivery{snapshot: snapshot, build: build, reporter: reporter}, nil
}

func (delivery *snapshotInterventionDelivery) Resolve() error {
	if delivery.resolved {
		return delivery.err
	}
	delivery.resolved = true
	channel, err := delivery.snapshot.InterventionChannel()
	if err == nil {
		delivery.delivery, err = delivery.build(channel)
		if err == nil && delivery.delivery == nil {
			err = errors.New("адаптер уведомлений не создан")
		}
	}
	delivery.err = err
	return delivery.err
}

func (delivery *snapshotInterventionDelivery) Deliver(
	ctx context.Context,
	event notify.Intervention,
) *notify.DeliveryError {
	if delivery == nil || ctx == nil || event == nil {
		return notify.NewDeliveryError()
	}
	key := event.EpisodeKey()
	if delivery.hasEvent && delivery.episode == key && delivery.retrying {
		delivery.reporter.retryingInterventionDelivery()
	} else {
		delivery.reporter.interventionRequired(event)
	}
	delivery.episode = key
	delivery.hasEvent = true
	if err := delivery.Resolve(); err != nil {
		delivery.retrying = true
		delivery.reporter.interventionConfigurationFailed(err)
		return notify.NewDeliveryError()
	}
	if err := delivery.delivery.Deliver(ctx, event); err != nil {
		delivery.retrying = true
		delivery.reporter.interventionDeliveryFailed()
		return err
	}
	delivery.retrying = false
	delivery.reporter.interventionDelivered()
	return nil
}

func (delivery *snapshotInterventionDelivery) observeSessionProgress(observation orchestrator.OwnSessionObservation) {
	if _, working := observation.(orchestrator.WorkingOwnSession); !working {
		return
	}
	delivery.hasEvent = false
	delivery.retrying = false
}

var _ notify.Deliverer = (*snapshotInterventionDelivery)(nil)
