package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/openspec"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

const (
	exitSuccess              = 0
	exitObstacle             = 1
	exitUsageOrConfiguration = 2
	waitProgressInterval     = 30 * time.Second
)

type changeSelection struct {
	changeName string
	storeID    string
}

type commandOptions struct {
	selection changeSelection
}

type resolvedChange struct {
	name             string
	planningHomeRoot string
	changeRoot       string
}

type changeSource interface {
	Resolve(context.Context, changeSelection) (resolvedChange, error)
}

type workingTreeRepository interface {
	Root() string
	Read(context.Context) (orchestrator.WorkingTreeObservation, error)
}

type paseoRuntime interface {
	ServerID() string
	SessionLink(orchestrator.SessionID) string
	FindActiveWorkspace(context.Context, orchestrator.ChangeKey, string) (orchestrator.ManagedWorkspaceObservation, error)
	FindOwnSessions(context.Context, orchestrator.ChangeKey, orchestrator.WorkspaceID, string) (orchestrator.OwnSessionObservation, error)
	ObserveOwnSession(context.Context, orchestrator.ChangeKey, orchestrator.WorkspaceID, string, orchestrator.SessionID) (orchestrator.OwnSessionObservation, error)
	CreateWorkspace(context.Context, orchestrator.ChangeKey, string) error
	CreateOwnSession(context.Context, orchestrator.ChangeKey, orchestrator.WorkspaceID, string, paseo.VerifiedSessionSettings, prompts.CommitPreparationPrompt) (orchestrator.SessionID, error)
	WaitOwnSession(context.Context, orchestrator.SessionID) error
	ArchiveOwnSession(context.Context, orchestrator.ChangeKey, orchestrator.WorkspaceID, string, orchestrator.ManagedSession) error
	VerifySessionSettings(context.Context, paseo.UntrustedSessionSettings) (paseo.VerifiedSessionSettings, error)
}

type newSessionInputs struct {
	settings paseo.VerifiedSessionSettings
	prompt   prompts.CommitPreparationPrompt
}

type waitClock interface {
	NewTicker(time.Duration) waitTicker
}

type waitTicker interface {
	C() <-chan time.Time
	Stop()
}

type commandDependencies struct {
	newChangeSource      func(string) (changeSource, error)
	openRepository       func(context.Context, string) (workingTreeRepository, error)
	acquireChangeLock    func(string, string) (io.Closer, error)
	openPaseo            func(context.Context) (paseoRuntime, error)
	loadNewSessionInputs func(context.Context, string, paseoRuntime) (newSessionInputs, error)
	clock                waitClock
}

func runCommand(
	ctx context.Context,
	arguments []string,
	workingDirectory string,
	output io.Writer,
	dependencies commandDependencies,
) int {
	if ctx == nil || output == nil || !dependencies.valid() {
		return exitObstacle
	}
	options, err := parseCommand(arguments)
	if err != nil {
		fmt.Fprintln(output, "Ошибка аргументов: используйте prepare-commits --change <name> [--store <id>].")
		return exitUsageOrConfiguration
	}
	return runPrepareCommits(ctx, options, workingDirectory, output, dependencies)
}

func parseCommand(arguments []string) (commandOptions, error) {
	if len(arguments) == 0 || arguments[0] != "prepare-commits" {
		return commandOptions{}, errors.New("неизвестная команда")
	}
	flags := flag.NewFlagSet("prepare-commits", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	changeName := flags.String("change", "", "имя активного OpenSpec change")
	storeID := flags.String("store", "", "идентификатор OpenSpec store")
	if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *changeName == "" {
		return commandOptions{}, errors.New("некорректные аргументы")
	}
	if _, err := openspec.NewSelection(*changeName, *storeID); err != nil {
		return commandOptions{}, err
	}
	return commandOptions{selection: changeSelection{changeName: *changeName, storeID: *storeID}}, nil
}

func (dependencies commandDependencies) valid() bool {
	return dependencies.newChangeSource != nil && dependencies.openRepository != nil &&
		dependencies.acquireChangeLock != nil && dependencies.openPaseo != nil &&
		dependencies.loadNewSessionInputs != nil && dependencies.clock != nil
}

func runPrepareCommits(
	ctx context.Context,
	options commandOptions,
	workingDirectory string,
	output io.Writer,
	dependencies commandDependencies,
) (exitCode int) {
	fmt.Fprintf(output, "Проверяю OpenSpec change %s.\n", options.selection.changeName)
	changeSource, err := dependencies.newChangeSource(workingDirectory)
	if err != nil {
		return reportCommandError(output, err)
	}
	change, err := changeSource.Resolve(ctx, options.selection)
	if err != nil {
		return reportCommandError(output, err)
	}

	fmt.Fprintln(output, "Проверяю рабочий Git.")
	repository, err := dependencies.openRepository(ctx, workingDirectory)
	if err != nil {
		return reportCommandError(output, err)
	}

	fmt.Fprintln(output, "Проверяю локальную среду и владение change.")
	lock, err := dependencies.acquireChangeLock(repository.Root(), change.changeRoot)
	if err != nil {
		return reportCommandError(output, err)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil && exitCode == exitSuccess {
			fmt.Fprintln(output, "Ошибка: не удалось освободить локальное владение change.")
			exitCode = exitObstacle
		}
	}()

	fmt.Fprintln(output, "Проверяю совместимость локального Paseo.")
	paseoRuntime, err := dependencies.openPaseo(ctx)
	if err != nil {
		return reportCommandError(output, err)
	}
	changeKey, err := orchestrator.NewCommitPreparationChangeKey(orchestrator.CommitPreparationIdentity{
		WorkingTreeRoot:  repository.Root(),
		PlanningHomeRoot: change.planningHomeRoot,
		ChangeName:       change.name,
		ServerID:         paseoRuntime.ServerID(),
	})
	if err != nil {
		return reportCommandError(output, err)
	}

	gateway := &commandGateway{
		selection:            options.selection,
		initialChange:        change,
		changeSource:         changeSource,
		repository:           repository,
		paseo:                paseoRuntime,
		loadNewSessionInputs: dependencies.loadNewSessionInputs,
		clock:                dependencies.clock,
		output:               output,
		reportedSessions:     make(map[string]struct{}),
	}
	reconciler, err := orchestrator.NewCommitPreparationReconciler(gateway)
	if err != nil {
		return reportCommandError(output, err)
	}

	fmt.Fprintln(output, "Сопровождение подготовки коммитов запущено.")
	outcome, err := reconciler.Run(ctx, changeKey, repository.Root())
	if err != nil {
		return reportCommandError(output, err)
	}
	return reportOutcome(output, paseoRuntime, outcome)
}
