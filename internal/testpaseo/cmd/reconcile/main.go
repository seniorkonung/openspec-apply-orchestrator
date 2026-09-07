//go:build paseo_integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/ownership"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/testpaseo"
)

type input struct {
	operation   testpaseo.DriverOperation
	change      string
	prompt      string
	workingRoot string
	changeRoot  string
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	request := input{
		change:      os.Getenv("OA_TESTPASEO_CHANGE"),
		prompt:      os.Getenv("OA_TESTPASEO_PROMPT"),
		workingRoot: os.Getenv("OA_TESTPASEO_WORKING_ROOT"),
		changeRoot:  os.Getenv("OA_TESTPASEO_CHANGE_ROOT"),
	}
	if len(os.Args) == 2 {
		request.operation = testpaseo.DriverOperation(os.Args[1])
	}
	result := execute(ctx, request)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, request input) testpaseo.DriverResult {
	result := testpaseo.DriverResult{
		Operation: request.operation, Observation: testpaseo.ObservationNotApplicable,
	}
	if err := validateInput(request); err != nil {
		return failed(result, err)
	}

	localEnvironment, err := ownership.CheckLocalEnvironment(request.workingRoot, request.changeRoot)
	if err != nil {
		return failed(result, err)
	}
	lock, err := localEnvironment.AcquireChangeLock()
	if err != nil {
		return failed(result, err)
	}
	defer lock.Close()

	client, err := paseo.NewClient()
	if err != nil {
		return failed(result, err)
	}
	environment, err := client.CheckCompatibility(ctx)
	if err != nil {
		return failed(result, err)
	}
	result.Version = environment.Version().String()
	result.ServerID = environment.ServerID().String()
	change, err := orchestrator.NewChangeKey(request.change)
	if err != nil {
		return failed(result, err)
	}
	settings, err := paseo.NewSessionSettings(testpaseo.ProviderID, testpaseo.ModelID, "", "")
	if err != nil {
		return failed(result, err)
	}
	gateway, err := paseo.NewReconcileGateway(client, environment, settings, request.prompt)
	if err != nil {
		return failed(result, err)
	}

	switch request.operation {
	case testpaseo.DriverStart:
		if err := startOne(ctx, gateway, change, request.workingRoot); err != nil {
			return failed(result, err)
		}
	case testpaseo.DriverObserve:
	case testpaseo.DriverReconcile:
		reconciler, err := orchestrator.NewPhaseOneReconciler(gateway, orchestrator.SystemClock{}, 100*time.Millisecond)
		if err != nil {
			return failed(result, err)
		}
		if err := reconciler.Run(ctx, change, request.workingRoot); err != nil {
			result = failed(result, err)
			if !errors.Is(err, context.Canceled) {
				return result
			}
		}
	default:
		return failed(result, errors.New("неизвестная операция процесса сопровождения"))
	}

	observeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return observe(observeCtx, gateway, change, request.workingRoot, result)
}

func validateInput(request input) error {
	if strings.TrimSpace(request.change) == "" || strings.TrimSpace(request.prompt) == "" ||
		strings.TrimSpace(request.workingRoot) == "" || strings.TrimSpace(request.changeRoot) == "" {
		return errors.New("неполные входные данные процесса сопровождения")
	}
	switch request.operation {
	case testpaseo.DriverStart, testpaseo.DriverObserve, testpaseo.DriverReconcile:
		return nil
	default:
		return errors.New("неизвестная операция процесса сопровождения")
	}
}

func startOne(
	ctx context.Context,
	gateway *paseo.ReconcileGateway,
	change orchestrator.ChangeKey,
	cwd string,
) error {
	workspace, err := oneWorkspace(ctx, gateway, change, cwd)
	if err != nil {
		return err
	}
	sessions, err := gateway.FindOwnSessions(ctx, change, workspace, cwd)
	if err != nil {
		return err
	}
	if _, absent := sessions.(orchestrator.NoActiveOwnSession); !absent {
		return nil
	}
	return gateway.CreateOwnSession(ctx, change, workspace, cwd)
}

func oneWorkspace(
	ctx context.Context,
	gateway *paseo.ReconcileGateway,
	change orchestrator.ChangeKey,
	cwd string,
) (orchestrator.WorkspaceID, error) {
	workspaces, err := gateway.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return orchestrator.WorkspaceID{}, err
	}
	if _, absent := workspaces.(orchestrator.NoManagedWorkspace); absent {
		if err := gateway.CreateWorkspace(ctx, change, cwd); err != nil {
			return orchestrator.WorkspaceID{}, err
		}
		workspaces, err = gateway.FindActiveWorkspace(ctx, change, cwd)
		if err != nil {
			return orchestrator.WorkspaceID{}, err
		}
	}
	one, ok := workspaces.(orchestrator.OneManagedWorkspace)
	if !ok {
		return orchestrator.WorkspaceID{}, errors.New("не найден один workspace")
	}
	return one.ID, nil
}

func observe(
	ctx context.Context,
	gateway *paseo.ReconcileGateway,
	change orchestrator.ChangeKey,
	cwd string,
	result testpaseo.DriverResult,
) testpaseo.DriverResult {
	workspaces, err := gateway.FindActiveWorkspace(ctx, change, cwd)
	if err != nil {
		return failed(result, err)
	}
	switch workspace := workspaces.(type) {
	case orchestrator.NoManagedWorkspace:
		result.Observation = testpaseo.ObservationNoWorkspace
		return result
	case orchestrator.AmbiguousManagedWorkspaces:
		result.Observation = testpaseo.ObservationAmbiguous
		return result
	case orchestrator.OneManagedWorkspace:
		result.WorkspaceID = workspace.ID.String()
		sessions, err := gateway.FindOwnSessions(ctx, change, workspace.ID, cwd)
		if err != nil {
			return failed(result, err)
		}
		return observeSession(result, sessions)
	default:
		return failed(result, errors.New("неизвестное наблюдение workspace"))
	}
}

func observeSession(
	result testpaseo.DriverResult,
	sessions orchestrator.OwnSessionObservation,
) testpaseo.DriverResult {
	switch session := sessions.(type) {
	case orchestrator.NoActiveOwnSession:
		result.Observation = testpaseo.ObservationNoSession
	case orchestrator.WorkingOwnSession:
		result.Observation = testpaseo.ObservationWorking
		result.SessionID = session.Session.ID().String()
	case orchestrator.OwnSessionAwaitingAction:
		result.SessionID = session.Session.ID().String()
		switch session.Reason {
		case orchestrator.SessionTurnFinished:
			result.Observation = testpaseo.ObservationTurnFinished
		case orchestrator.SessionPermissionRequested:
			result.Observation = testpaseo.ObservationPermission
		case orchestrator.SessionAgentError:
			result.Observation = testpaseo.ObservationAgentError
		}
	case orchestrator.ObservedOwnSessionClosed:
		result.Observation = testpaseo.ObservationClosed
		result.SessionID = session.Session.ID().String()
	case orchestrator.AmbiguousOwnSessions:
		result.Observation = testpaseo.ObservationAmbiguous
	default:
		return failed(result, errors.New("неизвестное наблюдение сессии"))
	}
	return result
}

func failed(result testpaseo.DriverResult, err error) testpaseo.DriverResult {
	result.Error = err.Error()
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, paseo.ErrCommandCanceled):
		result.ErrorKind = testpaseo.DriverCanceled
	case errors.Is(err, ownership.ErrUnsupportedFilesystem):
		result.ErrorKind = testpaseo.DriverUnsupportedFilesystem
	case errors.Is(err, paseo.ErrRunOutcomeUnknown):
		result.ErrorKind = testpaseo.DriverRunOutcomeUnknown
	default:
		result.ErrorKind = testpaseo.DriverOtherError
	}
	return result
}
