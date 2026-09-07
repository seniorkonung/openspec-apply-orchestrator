package paseo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

const maxSessionSettingLength = 256

type SessionSettings struct {
	provider string
	model    string
	thinking string
	mode     string
}

func NewSessionSettings(provider, model, thinking, mode string) (SessionSettings, error) {
	if !validSessionSetting(provider) || !validSessionSetting(model) ||
		(thinking != "" && !validSessionSetting(thinking)) ||
		(mode != "" && !validSessionSetting(mode)) {
		return SessionSettings{}, ErrInvalidSessionSettings
	}
	return SessionSettings{provider: provider, model: model, thinking: thinking, mode: mode}, nil
}

func validSessionSetting(value string) bool {
	return len(value) <= maxSessionSettingLength && validIdentifierValue(value)
}

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

func (client *Client) CreateOwnSession(
	ctx context.Context,
	environment CompatibleEnvironment,
	change orchestrator.ChangeKey,
	workspace ActiveWorkspace,
	settings SessionSettings,
	prompt string,
) (orchestrator.SessionID, error) {
	if err := validateCompatibleEnvironment(environment); err != nil {
		return orchestrator.SessionID{}, err
	}
	if change.String() == "" || workspace.id.String() == "" ||
		workspace.name != managedWorkspaceName(change) || workspace.cwd == "" {
		return orchestrator.SessionID{}, ErrInvalidDirectoryQuery
	}
	if !validSessionSetting(settings.provider) || !validSessionSetting(settings.model) ||
		(settings.thinking != "" && !validSessionSetting(settings.thinking)) ||
		(settings.mode != "" && !validSessionSetting(settings.mode)) {
		return orchestrator.SessionID{}, ErrInvalidSessionSettings
	}
	if strings.TrimSpace(prompt) == "" || strings.IndexByte(prompt, 0) >= 0 {
		return orchestrator.SessionID{}, ErrInvalidInitialPrompt
	}
	canonicalCWD, err := canonicalDirectory(workspace.cwd)
	if err != nil {
		return orchestrator.SessionID{}, err
	}
	if canonicalCWD != workspace.cwd {
		return orchestrator.SessionID{}, ErrInvalidWorkingDirectory
	}

	args := []string{
		"run", "--background",
		"--workspace", workspace.id.String(),
		"--provider", settings.provider,
		"--model", settings.model,
	}
	if settings.thinking != "" {
		args = append(args, "--thinking", settings.thinking)
	}
	if settings.mode != "" {
		args = append(args, "--mode", settings.mode)
	}
	for _, label := range ownSessionLabels(change, workspace.id) {
		args = append(args, "--label", label.key+"="+label.value)
	}
	args = append(args, "--json", "--", prompt)

	output, err := client.runner.run(ctx, command{
		name: "run",
		args: args,
		// Paseo 0.7.2 выводит родителя из PASEO_AGENT_ID даже при явном --workspace.
		// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/agent/run.ts
		unsetEnv: []string{"PASEO_AGENT_ID", "PASEO_WORKSPACE_ID"},
	})
	if err != nil {
		return orchestrator.SessionID{}, unknownRunOutcome(err)
	}
	result, err := decodeCreatedSession(output)
	if err != nil {
		return orchestrator.SessionID{}, unknownRunOutcome(err)
	}
	actualCWD, err := canonicalDirectory(result.CWD.value)
	if err != nil {
		return orchestrator.SessionID{}, unknownRunOutcome(err)
	}
	if actualCWD != workspace.cwd {
		return orchestrator.SessionID{}, unknownRunOutcome(ErrSessionWorkingDirectoryMismatch)
	}
	id, err := orchestrator.NewSessionID(result.AgentID.value)
	if err != nil {
		return orchestrator.SessionID{}, unknownRunOutcome(
			fmt.Errorf("%w: созданная сессия содержит некорректный ID", ErrUnexpectedJSON),
		)
	}
	return id, nil
}

func unknownRunOutcome(cause error) error {
	return fmt.Errorf("%w: %w", ErrRunOutcomeUnknown, cause)
}

func (client *Client) ArchiveOwnSession(
	ctx context.Context,
	environment CompatibleEnvironment,
	workspace ActiveWorkspace,
	session orchestrator.ManagedSession,
) error {
	if err := validateCompatibleEnvironment(environment); err != nil {
		return err
	}
	if err := validateManagedSessionTarget(workspace, session); err != nil {
		return err
	}

	before, err := client.inspectManagedSession(ctx, workspace, session)
	if err != nil {
		return err
	}
	if before.Archived.value {
		return nil
	}
	if before.Status.value == "initializing" || before.Status.value == "running" ||
		len(before.PendingPermissions.value) > 0 {
		return ErrSessionStillRunning
	}

	output, err := client.runner.run(ctx, command{
		name: "archive",
		args: []string{"archive", session.ID().String(), "--json"},
	})
	if err != nil {
		return err
	}
	result, err := decodeArchivedSession(output)
	if err != nil {
		return err
	}
	if result.AgentID.value != session.ID().String() {
		return ErrSessionIdentityMismatch
	}

	after, err := client.inspectManagedSession(ctx, workspace, session)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArchiveNotConfirmed, err)
	}
	if !after.Archived.value {
		return ErrArchiveNotConfirmed
	}
	return nil
}

func validateManagedSessionTarget(workspace ActiveWorkspace, session orchestrator.ManagedSession) error {
	if workspace.id.String() == "" || session.ID().String() == "" ||
		session.WorkspaceID() != workspace.id || session.ChangeKey().String() == "" ||
		workspace.name != managedWorkspaceName(session.ChangeKey()) || workspace.cwd == "" {
		return ErrInvalidDirectoryQuery
	}
	canonicalCWD, err := canonicalDirectory(workspace.cwd)
	if err != nil {
		return err
	}
	if canonicalCWD != workspace.cwd {
		return ErrInvalidWorkingDirectory
	}
	return nil
}

func (client *Client) inspectManagedSession(
	ctx context.Context,
	workspace ActiveWorkspace,
	session orchestrator.ManagedSession,
) (agentInspectionJSON, error) {
	inspection, err := client.inspectAgent(ctx, session.ID())
	if err != nil {
		return agentInspectionJSON{}, err
	}
	if _, err := inspection.toUntrustedSession(
		session.ID(), session.ChangeKey(), session.WorkspaceID(), workspace.cwd,
	); err != nil {
		return agentInspectionJSON{}, err
	}
	return inspection, nil
}

func ownSessionLabels(change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID) []labelFilter {
	return []labelFilter{
		{key: orchestrator.LabelOwner, value: orchestrator.ManagedOwner},
		{key: orchestrator.LabelVersion, value: orchestrator.CurrentOwnershipVersion},
		{key: orchestrator.LabelChange, value: change.String()},
		{key: orchestrator.LabelKind, value: orchestrator.CommitPreparationKind},
		{key: orchestrator.LabelWorkspace, value: workspace.String()},
	}
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

type createdSessionJSON struct {
	AgentID  requiredValue[string] `json:"agentId"`
	Status   requiredValue[string] `json:"status"`
	Provider requiredValue[string] `json:"provider"`
	CWD      requiredValue[string] `json:"cwd"`
	Title    requiredValue[string] `json:"title"`
}

// Схема соответствует JSON команды archive Paseo CLI 0.7.2.
// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/agent/archive.ts
type archivedSessionJSON struct {
	AgentID    requiredValue[string] `json:"agentId"`
	Status     requiredValue[string] `json:"status"`
	ArchivedAt requiredValue[string] `json:"archivedAt"`
}

func decodeArchivedSession(output []byte) (archivedSessionJSON, error) {
	var session archivedSessionJSON
	if err := decodeStrictJSON(output, &session); err != nil {
		return archivedSessionJSON{}, err
	}
	if !session.AgentID.present || !session.Status.present || !session.ArchivedAt.present {
		return archivedSessionJSON{}, fmt.Errorf("%w: archive не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(session.AgentID.value) || session.Status.value != "archived" {
		return archivedSessionJSON{}, fmt.Errorf("%w: archive содержит некорректное поле", ErrUnexpectedJSON)
	}
	if _, err := time.Parse(time.RFC3339Nano, session.ArchivedAt.value); err != nil {
		return archivedSessionJSON{}, fmt.Errorf("%w: archive содержит некорректное время", ErrUnexpectedJSON)
	}
	return session, nil
}

func decodeCreatedSession(output []byte) (createdSessionJSON, error) {
	var session createdSessionJSON
	if err := decodeStrictJSON(output, &session); err != nil {
		return createdSessionJSON{}, err
	}
	if !session.AgentID.present || !session.Status.present || !session.Provider.present ||
		!session.CWD.present || !session.Title.present {
		return createdSessionJSON{}, fmt.Errorf("%w: run не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(session.AgentID.value) || !validOpaqueValue(session.Provider.value) ||
		!filepath.IsAbs(session.CWD.value) || !validOpaqueValue(session.Title.value) {
		return createdSessionJSON{}, fmt.Errorf("%w: run содержит некорректное поле", ErrUnexpectedJSON)
	}
	switch session.Status.value {
	case "created", "running":
		return session, nil
	default:
		return createdSessionJSON{}, fmt.Errorf("%w: run содержит неизвестное состояние", ErrUnexpectedJSON)
	}
}
