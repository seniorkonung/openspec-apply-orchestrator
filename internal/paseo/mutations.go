package paseo

import (
	"context"
	"fmt"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

func (client *Client) CreateWorkspace(
	ctx context.Context,
	environment CompatibleEnvironment,
	change orchestrator.ChangeKey,
	cwd string,
) (ActiveWorkspace, error) {
	if err := validateCompatibleEnvironment(environment); err != nil {
		return ActiveWorkspace{}, err
	}
	if change.String() == "" {
		return ActiveWorkspace{}, fmt.Errorf("%w: пустой ключ change", ErrInvalidDirectoryQuery)
	}
	canonicalCWD, err := canonicalDirectory(cwd)
	if err != nil {
		return ActiveWorkspace{}, err
	}
	expectedName := managedWorkspaceName(change)

	output, err := client.runner.run(ctx, command{
		name: "workspace create",
		args: []string{
			"workspace", "create",
			"--isolation", "local",
			"--path", canonicalCWD,
			"--title", expectedName,
			"--json",
		},
	})
	if err != nil {
		return ActiveWorkspace{}, err
	}

	workspace, err := decodeCreatedWorkspace(output)
	if err != nil {
		return ActiveWorkspace{}, err
	}
	actualCWD, err := canonicalDirectory(workspace.CWD.value)
	if err != nil {
		return ActiveWorkspace{}, err
	}
	if workspace.Name.value != expectedName || workspace.Isolation.value != "local" || actualCWD != canonicalCWD {
		return ActiveWorkspace{}, fmt.Errorf("%w: созданный workspace не соответствует запросу", ErrUnexpectedJSON)
	}
	id, err := orchestrator.NewWorkspaceID(workspace.WorkspaceID.value)
	if err != nil {
		return ActiveWorkspace{}, fmt.Errorf("%w: созданный workspace содержит некорректный ID", ErrUnexpectedJSON)
	}
	return ActiveWorkspace{id: id, name: expectedName, cwd: canonicalCWD}, nil
}

func validateCompatibleEnvironment(environment CompatibleEnvironment) error {
	if environment.serverID.String() == "" || environment.version.String() != compatiblePaseoVersion {
		return ErrIncompatibleCLIVersion
	}
	return nil
}

func decodeCreatedWorkspace(output []byte) (rawWorkspaceJSON, error) {
	var workspace rawWorkspaceJSON
	if err := decodeStrictJSON(output, &workspace); err != nil {
		return rawWorkspaceJSON{}, err
	}
	if err := validateWorkspaceJSON(workspace); err != nil {
		return rawWorkspaceJSON{}, err
	}
	return workspace, nil
}
