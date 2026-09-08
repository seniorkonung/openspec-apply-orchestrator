package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

type commandGateway struct {
	selection            changeSelection
	initialChange        resolvedChange
	changeSource         changeSource
	repository           workingTreeRepository
	paseo                paseoRuntime
	loadNewSessionInputs func(context.Context, string, paseoRuntime) (newSessionInputs, error)
	clock                waitClock
	output               io.Writer
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
	return nil
}

func (gateway *commandGateway) FindActiveWorkspace(ctx context.Context, change orchestrator.ChangeKey, cwd string) (orchestrator.ManagedWorkspaceObservation, error) {
	return gateway.paseo.FindActiveWorkspace(ctx, change, cwd)
}

func (gateway *commandGateway) FindOwnSessions(ctx context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string) (orchestrator.OwnSessionObservation, error) {
	observation, err := gateway.paseo.FindOwnSessions(ctx, change, workspace, cwd)
	if err == nil {
		gateway.reportRecoveredSession(observation)
	}
	return observation, err
}

func (gateway *commandGateway) ObserveOwnSession(ctx context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string, session orchestrator.SessionID) (orchestrator.OwnSessionObservation, error) {
	observation, err := gateway.paseo.ObserveOwnSession(ctx, change, workspace, cwd, session)
	if err == nil {
		gateway.reportRecoveredSession(observation)
	}
	return observation, err
}

func (gateway *commandGateway) ReadWorkingTree(ctx context.Context, cwd string) (orchestrator.WorkingTreeObservation, error) {
	if cwd != gateway.repository.Root() {
		return nil, errors.New("канонический рабочий Git-контекст изменился")
	}
	return gateway.repository.Read(ctx)
}

func (gateway *commandGateway) PrepareNewSession(ctx context.Context, _ orchestrator.ChangeKey, _ string) (orchestrator.PreparedSessionCreation, error) {
	fmt.Fprintln(gateway.output, "Проверяю конфигурацию и каталог новой сессии.")
	inputs, err := gateway.loadNewSessionInputs(ctx, gateway.repository.Root(), gateway.paseo)
	if err != nil {
		return orchestrator.PreparedSessionCreation{}, err
	}
	return orchestrator.NewPreparedSessionCreation(func(
		ctx context.Context,
		change orchestrator.ChangeKey,
		workspace orchestrator.WorkspaceID,
		cwd string,
	) (orchestrator.SessionID, error) {
		fmt.Fprintln(gateway.output, "Создаю собственную сессию подготовки коммитов.")
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
			fmt.Fprintf(gateway.output, "Создана собственная сессия %s.\n", created.String())
		}
		return created, err
	}), nil
}

func (gateway *commandGateway) CreateWorkspace(ctx context.Context, change orchestrator.ChangeKey, cwd string) error {
	fmt.Fprintln(gateway.output, "Создаю workspace выбранного change.")
	return gateway.paseo.CreateWorkspace(ctx, change, cwd)
}

func (gateway *commandGateway) WaitOwnSession(ctx context.Context, session orchestrator.SessionID) error {
	fmt.Fprintf(gateway.output, "Ожидаю завершения хода сессии %s.\n", session.String())
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
			fmt.Fprintf(gateway.output, "Ожидание продолжается: сессия %s.\n", session.String())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (gateway *commandGateway) ArchiveOwnSession(ctx context.Context, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string, session orchestrator.ManagedSession) error {
	fmt.Fprintf(gateway.output, "Архивирую собственную сессию %s.\n", session.ID().String())
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
	fmt.Fprintf(gateway.output, "Восстановлена собственная сессия %s.\n", id.String())
}
