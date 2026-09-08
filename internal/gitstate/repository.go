package gitstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrInvalidRepository     = errors.New("некорректный адаптер рабочего Git")
	ErrNotRepository         = errors.New("рабочий каталог не принадлежит Git-репозиторию")
	ErrWorkingContextChanged = errors.New("канонический рабочий контекст Git изменился")
	ErrUnexpectedOutput      = errors.New("команда Git вернула неожиданный вывод")
)

type Repository struct {
	runner                    *runner
	requestedWorkingDirectory string
	workingDirectory          string
	root                      string
}

func Open(ctx context.Context, workingDirectory string) (*Repository, error) {
	return openWithConfig(ctx, workingDirectory, defaultRunnerConfig())
}

func openWithConfig(
	ctx context.Context,
	workingDirectory string,
	config runnerConfig,
) (*Repository, error) {
	if ctx == nil || workingDirectory == "" {
		return nil, ErrInvalidRepository
	}

	requestedWorkingDirectory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("%w: получить абсолютный рабочий путь: %v", ErrInvalidRepository, err)
	}
	canonicalWorkingDirectory, err := canonicalExistingDirectory(requestedWorkingDirectory)
	if err != nil {
		return nil, fmt.Errorf("%w: рабочий каталог: %v", ErrInvalidRepository, err)
	}
	runner, err := newRunner(config)
	if err != nil {
		return nil, err
	}
	root, err := runner.resolveRoot(ctx, canonicalWorkingDirectory)
	if err != nil {
		var exitError *CommandExitError
		if errors.As(err, &exitError) && exitError.ExitCode == 128 {
			return nil, fmt.Errorf("%w: %v", ErrNotRepository, err)
		}
		return nil, fmt.Errorf("прочитать корень рабочего Git: %w", err)
	}

	return &Repository{
		runner:                    runner,
		requestedWorkingDirectory: filepath.Clean(requestedWorkingDirectory),
		workingDirectory:          canonicalWorkingDirectory,
		root:                      root,
	}, nil
}

func (repository *Repository) Root() string {
	if repository == nil {
		return ""
	}
	return repository.root
}

func (repository *Repository) Read(ctx context.Context) (State, error) {
	if repository == nil || repository.runner == nil || repository.root == "" ||
		repository.workingDirectory == "" || repository.requestedWorkingDirectory == "" || ctx == nil {
		return nil, ErrInvalidRepository
	}

	workingDirectory, err := canonicalExistingDirectory(repository.requestedWorkingDirectory)
	if err != nil {
		return nil, fmt.Errorf("перепроверить рабочий каталог Git: %w", err)
	}
	if workingDirectory != repository.workingDirectory {
		return nil, fmt.Errorf(
			"%w: ожидался %q, получен %q",
			ErrWorkingContextChanged,
			repository.workingDirectory,
			workingDirectory,
		)
	}
	root, err := repository.runner.resolveRoot(ctx, workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("перепроверить корень рабочего Git: %w", err)
	}
	if root != repository.root {
		return nil, fmt.Errorf(
			"%w: ожидался %q, получен %q",
			ErrWorkingContextChanged,
			repository.root,
			root,
		)
	}

	result, err := repository.runner.run(ctx, command{
		name:             "status",
		workingDirectory: repository.root,
		arguments: []string{
			"status",
			"--porcelain=v2",
			"--untracked-files=normal",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("прочитать состояние рабочего Git: %w", err)
	}
	if len(result.stdout) == 0 {
		return Clean{}, nil
	}
	return Dirty{}, nil
}

func canonicalExistingDirectory(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("получить абсолютный путь: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("канонизировать путь: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("прочитать путь: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("путь не является каталогом")
	}
	return filepath.Clean(canonical), nil
}
