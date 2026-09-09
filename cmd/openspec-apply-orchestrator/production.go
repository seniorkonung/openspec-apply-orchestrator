package main

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/gitstate"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/openspec"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/ownership"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

func productionCommandDependencies() commandDependencies {
	return commandDependencies{
		newChangeSource:      newProductionChangeSource,
		openRepository:       openProductionRepository,
		acquireChangeLock:    acquireProductionChangeLock,
		openPaseo:            openProductionPaseo,
		loadNewSessionInputs: loadProductionNewSessionInputs,
		clock:                systemWaitClock{},
	}
}

type productionChangeSource struct {
	client *openspec.Client
}

func newProductionChangeSource(workingDirectory string) (changeSource, error) {
	client, err := openspec.NewClient(workingDirectory)
	if err != nil {
		return nil, err
	}
	return &productionChangeSource{client: client}, nil
}

func (source *productionChangeSource) Resolve(ctx context.Context, selection changeSelection) (resolvedChange, error) {
	selected, err := openspec.NewSelection(selection.changeName, selection.storeID)
	if err != nil {
		return resolvedChange{}, err
	}
	resolved, err := source.client.ResolveChange(ctx, selected)
	if err != nil {
		return resolvedChange{}, err
	}
	return resolvedChange{
		name:             resolved.Name(),
		planningHomeRoot: resolved.PlanningHomeRoot(),
		changeRoot:       resolved.ChangeRoot(),
	}, nil
}

type productionRepository struct {
	repository *gitstate.Repository
}

func openProductionRepository(ctx context.Context, workingDirectory string) (workingTreeRepository, error) {
	repository, err := gitstate.Open(ctx, workingDirectory)
	if err != nil {
		return nil, err
	}
	return &productionRepository{repository: repository}, nil
}

func (repository *productionRepository) Root() string {
	return repository.repository.Root()
}

func (repository *productionRepository) Read(ctx context.Context) (orchestrator.WorkingTreeObservation, error) {
	state, err := repository.repository.Read(ctx)
	if err != nil {
		return nil, err
	}
	switch state.(type) {
	case gitstate.Clean:
		return orchestrator.CleanWorkingTree{}, nil
	case gitstate.Dirty:
		return orchestrator.DirtyWorkingTree{}, nil
	default:
		return nil, errors.New("Git вернул неизвестное состояние")
	}
}

func acquireProductionChangeLock(workingRoot, changeRoot string) (io.Closer, error) {
	environment, err := ownership.CheckLocalEnvironment(workingRoot, changeRoot)
	if err != nil {
		return nil, err
	}
	return environment.AcquireChangeLock()
}

func openProductionPaseo(ctx context.Context) (paseoRuntime, error) {
	return paseo.NewRuntime(ctx)
}

func loadProductionNewSessionInputs(ctx context.Context, root string, runtime paseoRuntime) (newSessionInputs, error) {
	configurationRoot, err := config.NewRepositoryRoot(root)
	if err != nil {
		return newSessionInputs{}, err
	}
	configuration, err := config.Read(configurationRoot)
	if err != nil {
		return newSessionInputs{}, err
	}
	channel := configuration.InterventionChannel()
	if channel.Type() == "" || channel.URL() == "" {
		return newSessionInputs{}, &config.FieldError{Path: "notifications.intervention", Kind: config.ErrMissingField}
	}
	settings, err := runtime.VerifySessionSettings(ctx, configuration.CommitPreparation())
	if err != nil {
		return newSessionInputs{}, err
	}
	return newSessionInputs{settings: settings, prompt: prompts.CommitPreparation()}, nil
}

type systemWaitClock struct{}

func (systemWaitClock) NewTicker(interval time.Duration) waitTicker {
	return systemWaitTicker{ticker: time.NewTicker(interval)}
}

type systemWaitTicker struct {
	ticker *time.Ticker
}

func (ticker systemWaitTicker) C() <-chan time.Time {
	return ticker.ticker.C
}

func (ticker systemWaitTicker) Stop() {
	ticker.ticker.Stop()
}
