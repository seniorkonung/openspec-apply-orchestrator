package paseo

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

const managedWorkspaceNamePrefix = "oa-v1-"

type ActiveWorkspace struct {
	id   orchestrator.WorkspaceID
	name string
	cwd  string
}

func (workspace ActiveWorkspace) ID() orchestrator.WorkspaceID {
	return workspace.id
}

func (workspace ActiveWorkspace) Name() string {
	return workspace.name
}

func (workspace ActiveWorkspace) CWD() string {
	return workspace.cwd
}

type ActiveWorkspaceObservation interface {
	isActiveWorkspaceObservation()
}

type NoActiveWorkspace struct{}

func (NoActiveWorkspace) isActiveWorkspaceObservation() {}

type OneActiveWorkspace struct {
	Workspace ActiveWorkspace
}

func (OneActiveWorkspace) isActiveWorkspaceObservation() {}

type AmbiguousActiveWorkspaces struct {
	Workspaces []ActiveWorkspace
}

func (AmbiguousActiveWorkspaces) isActiveWorkspaceObservation() {}

func managedWorkspaceName(change orchestrator.ChangeKey) string {
	digest := sha256.Sum256([]byte(change.String()))
	return fmt.Sprintf("%s%x", managedWorkspaceNamePrefix, digest)
}

func (client *Client) FindActiveWorkspace(
	ctx context.Context,
	change orchestrator.ChangeKey,
	cwd string,
) (ActiveWorkspaceObservation, error) {
	if change.String() == "" {
		return nil, fmt.Errorf("%w: пустой ключ change", ErrInvalidDirectoryQuery)
	}
	canonicalCWD, err := canonicalDirectory(cwd)
	if err != nil {
		return nil, err
	}

	workspaces, err := client.adapter.ListActiveWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	expectedName := managedWorkspaceName(change)
	matches := make([]ActiveWorkspace, 0, 1)
	for index, workspace := range workspaces {
		if workspace.Name() != expectedName {
			continue
		}
		workspaceCWD, err := canonicalDirectory(workspace.CWD())
		if err != nil {
			return nil, fmt.Errorf("workspace %d: %w", index+1, err)
		}
		if workspaceCWD != canonicalCWD {
			continue
		}
		id, err := orchestrator.NewWorkspaceID(workspace.ID())
		if err != nil {
			return nil, fmt.Errorf("%w: workspace %d содержит некорректный ID", ErrUnexpectedJSON, index+1)
		}
		matches = append(matches, ActiveWorkspace{id: id, name: expectedName, cwd: canonicalCWD})
	}

	switch len(matches) {
	case 0:
		return NoActiveWorkspace{}, nil
	case 1:
		return OneActiveWorkspace{Workspace: matches[0]}, nil
	default:
		return AmbiguousActiveWorkspaces{Workspaces: matches}, nil
	}
}

func canonicalDirectory(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: пустой путь", ErrInvalidWorkingDirectory)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: абсолютный путь: %v", ErrInvalidWorkingDirectory, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: разрешить символические ссылки: %v", ErrInvalidWorkingDirectory, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: прочитать каталог: %v", ErrInvalidWorkingDirectory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: путь не является каталогом", ErrInvalidWorkingDirectory)
	}
	return filepath.Clean(canonical), nil
}
