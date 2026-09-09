package paseo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
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

	workspace, err := client.adapter.CreateWorkspace(ctx, paseocli.WorkspaceCreation{
		Name: expectedName,
		CWD:  canonicalCWD,
	})
	if err != nil {
		return ActiveWorkspace{}, err
	}

	actualCWD, err := canonicalDirectory(workspace.CWD())
	if err != nil {
		return ActiveWorkspace{}, err
	}
	if workspace.Name() != expectedName || actualCWD != canonicalCWD {
		return ActiveWorkspace{}, fmt.Errorf("%w: созданный workspace не соответствует запросу", ErrUnexpectedJSON)
	}
	id, err := orchestrator.NewWorkspaceID(workspace.ID())
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
	settings VerifiedSessionSettings,
	prompt prompts.CommitPreparationPrompt,
) (orchestrator.SessionID, error) {
	if err := validateCompatibleEnvironment(environment); err != nil {
		return orchestrator.SessionID{}, err
	}
	if err := validateVerifiedSessionSettings(environment, settings); err != nil {
		return orchestrator.SessionID{}, ErrInvalidSessionSettings
	}
	return client.createOwnSession(
		ctx,
		environment,
		change,
		workspace,
		settings.runSettings(),
		prompt.Text(),
	)
}

type runSessionSettings struct {
	provider     string
	model        string
	reasoning    string
	hasReasoning bool
	mode         paseocli.FullAccessMode
}

func (client *Client) createOwnSession(
	ctx context.Context,
	environment CompatibleEnvironment,
	change orchestrator.ChangeKey,
	workspace ActiveWorkspace,
	settings runSessionSettings,
	prompt string,
) (orchestrator.SessionID, error) {
	if err := validateCompatibleEnvironment(environment); err != nil {
		return orchestrator.SessionID{}, err
	}
	if change.String() == "" || workspace.id.String() == "" ||
		workspace.name != managedWorkspaceName(change) || workspace.cwd == "" {
		return orchestrator.SessionID{}, ErrInvalidDirectoryQuery
	}
	if !validCatalogIdentifier(settings.provider) || !validCatalogIdentifier(settings.model) ||
		(settings.hasReasoning && !validCatalogIdentifier(settings.reasoning)) ||
		(!settings.hasReasoning && settings.reasoning != "") ||
		!validCatalogIdentifier(settings.mode.ID()) {
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

	labels := make([]paseocli.SessionLabel, 0, 5)
	for _, label := range ownSessionLabels(change, workspace.id) {
		labels = append(labels, paseocli.SessionLabel{Key: label.key, Value: label.value})
	}
	result, err := client.adapter.CreateSession(ctx, paseocli.SessionCreation{
		WorkspaceID:  workspace.id.String(),
		Provider:     settings.provider,
		Model:        settings.model,
		Reasoning:    settings.reasoning,
		HasReasoning: settings.hasReasoning,
		Mode:         settings.mode,
		Labels:       labels,
		Prompt:       prompt,
	})
	if err != nil {
		return orchestrator.SessionID{}, unknownRunOutcome(err)
	}
	actualCWD, err := canonicalDirectory(result.CWD())
	if err != nil {
		return orchestrator.SessionID{}, unknownRunOutcome(err)
	}
	if actualCWD != workspace.cwd {
		return orchestrator.SessionID{}, unknownRunOutcome(ErrSessionWorkingDirectoryMismatch)
	}
	id, err := orchestrator.NewSessionID(result.ID())
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
		return orchestrator.ClassifySourceReadError(ctx, orchestrator.ReadSourcePaseo, err)
	}
	if before.Archived() {
		return nil
	}
	if before.State() == paseocli.SessionInitializing || before.State() == paseocli.SessionRunning ||
		before.HasPendingPermission() {
		return ErrSessionStillRunning
	}

	mutationErr := client.adapter.ArchiveSession(ctx, session.ID().String())

	after, inspectionErr := client.inspectManagedSession(ctx, workspace, session)
	inspectionErr = orchestrator.ClassifySourceReadError(
		ctx,
		orchestrator.ReadSourcePaseo,
		inspectionErr,
	)
	if inspectionErr == nil && after.Archived() {
		return nil
	}
	if mutationErr != nil {
		if inspectionErr != nil {
			return unknownArchiveOutcome(mutationErr, inspectionErr)
		}
		return unknownArchiveOutcome(mutationErr, ErrArchiveNotConfirmed)
	}
	if inspectionErr != nil {
		return fmt.Errorf("%w: %w", ErrArchiveNotConfirmed, inspectionErr)
	}
	return ErrArchiveNotConfirmed
}

func unknownArchiveOutcome(causes ...error) error {
	return fmt.Errorf("%w: %w", ErrArchiveOutcomeUnknown, errors.Join(causes...))
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
) (paseocli.SessionInspection, error) {
	inspection, err := client.inspectAgent(ctx, session.ID())
	if err != nil {
		return paseocli.SessionInspection{}, err
	}
	if _, err := inspectionToUntrustedSession(
		inspection,
		session.ID(), session.ChangeKey(), session.WorkspaceID(), workspace.cwd,
	); err != nil {
		return paseocli.SessionInspection{}, err
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
	if !environment.value.IsCompatible() {
		return ErrIncompatibleCLIVersion
	}
	return nil
}
