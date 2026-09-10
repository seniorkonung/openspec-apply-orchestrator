package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
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
	verbose   bool
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

type configurationSnapshot interface {
	CommitPreparation() (config.UntrustedAgentSettings, error)
	InterventionChannel() (config.InterventionChannel, error)
}

type interventionDeliveryFactory func(config.InterventionChannel) (notify.Deliverer, error)

type waitClock interface {
	NewTicker(time.Duration) waitTicker
}

type waitTicker interface {
	C() <-chan time.Time
	Stop()
}

type commandDependencies struct {
	newChangeSource         func(string) (changeSource, error)
	openRepository          func(context.Context, string) (workingTreeRepository, error)
	acquireChangeLock       func(string, string) (io.Closer, error)
	openPaseo               func(context.Context) (paseoRuntime, error)
	readConfiguration       func(string) (configurationSnapshot, error)
	loadNewSessionInputs    func(context.Context, configurationSnapshot, paseoRuntime) (newSessionInputs, error)
	newInterventionDelivery interventionDeliveryFactory
	waitClock               waitClock
	interventionClock       orchestrator.InterventionClock
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
		return newCommandReporter(output, false).invalidArguments()
	}
	return runPrepareCommits(
		ctx,
		options,
		workingDirectory,
		newCommandReporter(output, options.verbose),
		dependencies,
	)
}

func parseCommand(arguments []string) (commandOptions, error) {
	if len(arguments) == 0 || arguments[0] != "prepare-commits" {
		return commandOptions{}, errors.New("неизвестная команда")
	}
	flags := flag.NewFlagSet("prepare-commits", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	changeName := flags.String("change", "", "имя активного OpenSpec change")
	storeID := flags.String("store", "", "идентификатор OpenSpec store")
	verbose := flags.Bool("verbose", false, "показывать результаты технических чтений и проверок")
	if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *changeName == "" {
		return commandOptions{}, errors.New("некорректные аргументы")
	}
	if _, err := openspec.NewSelection(*changeName, *storeID); err != nil {
		return commandOptions{}, err
	}
	return commandOptions{
		selection: changeSelection{changeName: *changeName, storeID: *storeID},
		verbose:   *verbose,
	}, nil
}

func (dependencies commandDependencies) valid() bool {
	return dependencies.newChangeSource != nil && dependencies.openRepository != nil &&
		dependencies.acquireChangeLock != nil && dependencies.openPaseo != nil &&
		dependencies.readConfiguration != nil && dependencies.loadNewSessionInputs != nil &&
		dependencies.newInterventionDelivery != nil && dependencies.waitClock != nil &&
		dependencies.interventionClock != nil
}

func runPrepareCommits(
	ctx context.Context,
	options commandOptions,
	workingDirectory string,
	reporter *commandReporter,
	dependencies commandDependencies,
) (exitCode int) {
	reporter.checkingOpenSpec(options.selection.changeName)
	changeSource, err := dependencies.newChangeSource(workingDirectory)
	if err != nil {
		return reporter.commandError(err)
	}
	change, err := changeSource.Resolve(ctx, options.selection)
	if err != nil {
		return reporter.commandError(err)
	}
	reporter.openSpecRead()

	reporter.checkingWorkingTree()
	repository, err := dependencies.openRepository(ctx, workingDirectory)
	if err != nil {
		return reporter.commandError(err)
	}
	reporter.workingTreeOpened()
	configuration, err := dependencies.readConfiguration(repository.Root())
	if err != nil {
		return reporter.commandError(err)
	}
	reporter.configurationSnapshotRead()

	reporter.checkingLocalOwnership()
	lock, err := dependencies.acquireChangeLock(repository.Root(), change.changeRoot)
	if err != nil {
		return reporter.commandError(err)
	}
	reporter.localOwnershipAcquired()
	defer func() {
		if closeErr := lock.Close(); closeErr != nil && exitCode == exitSuccess {
			reporter.changeLockReleaseFailed()
			exitCode = exitObstacle
		}
	}()

	reporter.checkingPaseo()
	paseoRuntime, err := dependencies.openPaseo(ctx)
	if err != nil {
		return reporter.commandError(err)
	}
	reporter.paseoCompatible()
	changeKey, err := orchestrator.NewCommitPreparationChangeKey(orchestrator.CommitPreparationIdentity{
		WorkingTreeRoot:  repository.Root(),
		PlanningHomeRoot: change.planningHomeRoot,
		ChangeName:       change.name,
		ServerID:         paseoRuntime.ServerID(),
	})
	if err != nil {
		return reporter.commandError(err)
	}

	delivery, err := newSnapshotInterventionDelivery(
		configuration,
		dependencies.newInterventionDelivery,
		reporter,
	)
	if err != nil {
		return reporter.commandError(err)
	}
	gateway := &commandGateway{
		selection:            options.selection,
		initialChange:        change,
		changeSource:         changeSource,
		repository:           repository,
		paseo:                paseoRuntime,
		configuration:        configuration,
		loadNewSessionInputs: dependencies.loadNewSessionInputs,
		delivery:             delivery,
		clock:                dependencies.waitClock,
		reporter:             reporter,
		reportedSessions:     make(map[string]struct{}),
	}
	reconciler, err := orchestrator.NewMonitoredCommitPreparationReconcilerWithClock(
		gateway,
		delivery,
		gateway.knownInterventionSession,
		dependencies.interventionClock,
	)
	if err != nil {
		return reporter.commandError(err)
	}

	reporter.monitoringStarted()
	outcome, err := reconciler.Run(ctx, changeKey, repository.Root())
	if err != nil {
		return reporter.commandError(err)
	}
	return reporter.outcome(paseoRuntime, outcome)
}
