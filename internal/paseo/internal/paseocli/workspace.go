package paseocli

import (
	"context"
	"fmt"
	"path/filepath"
)

// ActiveWorkspace — минимальная проверенная проекция активного workspace для доменного слоя.
type ActiveWorkspace struct {
	id   string
	name string
	cwd  string
}

func (workspace ActiveWorkspace) ID() string {
	return workspace.id
}

func (workspace ActiveWorkspace) Name() string {
	return workspace.name
}

func (workspace ActiveWorkspace) CWD() string {
	return workspace.cwd
}

// Схема значимых полей JSON команды workspace ls Paseo CLI 0.8.0-beta.1.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/workspace/shared.ts
type activeWorkspaceJSON struct {
	WorkspaceID requiredValue[string] `json:"workspaceId"`
	Name        requiredValue[string] `json:"name"`
	Isolation   requiredValue[string] `json:"isolation"`
	CWD         requiredValue[string] `json:"cwd"`
}

func (adapter *Adapter) ListActiveWorkspaces(ctx context.Context) ([]ActiveWorkspace, error) {
	output, err := adapter.Run(ctx, Invocation{
		Name:      "workspace ls",
		Arguments: []string{"workspace", "ls", "--json"},
	})
	if err != nil {
		return nil, err
	}
	return decodeActiveWorkspaces(output)
}

func decodeActiveWorkspaces(output []byte) ([]ActiveWorkspace, error) {
	var raw []activeWorkspaceJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: список workspace равен null", ErrUnexpectedJSON)
	}

	workspaces := make([]ActiveWorkspace, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, workspace := range raw {
		if !workspace.WorkspaceID.present || !workspace.Name.present ||
			!workspace.Isolation.present || !workspace.CWD.present {
			return nil, fmt.Errorf("%w: workspace %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validIdentifierValue(workspace.WorkspaceID.value) ||
			!validOpaqueValue(workspace.Name.value) || !filepath.IsAbs(workspace.CWD.value) {
			return nil, fmt.Errorf("%w: workspace %d содержит некорректное поле", ErrUnexpectedJSON, index+1)
		}
		switch workspace.Isolation.value {
		case "local", "worktree":
		default:
			return nil, fmt.Errorf("%w: workspace %d содержит неизвестную изоляцию", ErrUnexpectedJSON, index+1)
		}
		if _, exists := seen[workspace.WorkspaceID.value]; exists {
			return nil, fmt.Errorf("%w: workspace %q повторяется", ErrUnexpectedJSON, workspace.WorkspaceID.value)
		}
		seen[workspace.WorkspaceID.value] = struct{}{}
		workspaces = append(workspaces, ActiveWorkspace{
			id:   workspace.WorkspaceID.value,
			name: workspace.Name.value,
			cwd:  workspace.CWD.value,
		})
	}
	return workspaces, nil
}
