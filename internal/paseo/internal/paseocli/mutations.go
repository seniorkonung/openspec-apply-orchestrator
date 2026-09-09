package paseocli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// WorkspaceCreation содержит доменные входы создания workspace без знания
// вызывающей стороной о синтаксисе Paseo CLI.
type WorkspaceCreation struct {
	Name string
	CWD  string
}

// SessionCreation содержит проверенные входы единственной мутации создания
// сессии без раскрытия аргументов активного CLI-контракта.
type SessionCreation struct {
	WorkspaceID  string
	Provider     string
	Model        string
	Reasoning    string
	HasReasoning bool
	Mode         FullAccessMode
	Labels       []SessionLabel
	Prompt       string
}

// CreatedSession — минимальная проверенная проекция созданной сессии.
type CreatedSession struct {
	id  string
	cwd string
}

func (session CreatedSession) ID() string {
	return session.id
}

func (session CreatedSession) CWD() string {
	return session.cwd
}

// Схема значимых полей JSON команды workspace create Paseo CLI 0.8.0-beta.1.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/workspace/create.ts
type createdWorkspaceJSON struct {
	WorkspaceID requiredValue[string] `json:"workspaceId"`
	Project     requiredValue[string] `json:"project"`
	Name        requiredValue[string] `json:"name"`
	Isolation   requiredValue[string] `json:"isolation"`
	CWD         requiredValue[string] `json:"cwd"`
}

// Схема значимых полей JSON команды run Paseo CLI 0.8.0-beta.1.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/agent/run.ts
type createdSessionJSON struct {
	AgentID  requiredValue[string] `json:"agentId"`
	Status   requiredValue[string] `json:"status"`
	Provider requiredValue[string] `json:"provider"`
	CWD      requiredValue[string] `json:"cwd"`
	Title    requiredValue[string] `json:"title"`
}

// Схема значимых полей JSON команды archive Paseo CLI 0.8.0-beta.1.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/agent/archive.ts
type archivedSessionJSON struct {
	AgentID    requiredValue[string] `json:"agentId"`
	Status     requiredValue[string] `json:"status"`
	ArchivedAt requiredValue[string] `json:"archivedAt"`
}

func (adapter *Adapter) CreateWorkspace(
	ctx context.Context,
	request WorkspaceCreation,
) (ActiveWorkspace, error) {
	if !validOpaqueValue(request.Name) || !filepath.IsAbs(request.CWD) {
		return ActiveWorkspace{}, ErrInvalidWorkspaceMutation
	}
	output, err := adapter.Run(ctx, Invocation{
		Name: "workspace create",
		Arguments: []string{
			"workspace", "create",
			"--isolation", "local",
			"--path", request.CWD,
			"--title", request.Name,
			"--json",
		},
	})
	if err != nil {
		return ActiveWorkspace{}, err
	}
	return decodeCreatedWorkspace(output)
}

func (adapter *Adapter) CreateSession(
	ctx context.Context,
	request SessionCreation,
) (CreatedSession, error) {
	if !validSessionCreation(request) {
		return CreatedSession{}, ErrInvalidSessionMutation
	}

	args := []string{
		"run", "--background",
		"--workspace", request.WorkspaceID,
		"--provider", request.Provider,
		"--model", request.Model,
	}
	if request.HasReasoning {
		args = append(args, "--thinking", request.Reasoning)
	}
	args = append(args, "--mode", request.Mode.ID())
	for _, label := range request.Labels {
		args = append(args, "--label", label.Key+"="+label.Value)
	}
	args = append(args, "--json", "--", request.Prompt)

	output, err := adapter.Run(ctx, Invocation{
		Name:             "run",
		Arguments:        args,
		UnsetEnvironment: []string{"PASEO_AGENT_ID", "PASEO_WORKSPACE_ID"},
	})
	if err != nil {
		return CreatedSession{}, err
	}
	return decodeCreatedSession(output)
}

func (adapter *Adapter) ArchiveSession(ctx context.Context, expectedID string) error {
	if !validIdentifierValue(expectedID) {
		return ErrInvalidArchiveSessionID
	}
	output, err := adapter.Run(ctx, Invocation{
		Name:      "archive",
		Arguments: []string{"archive", expectedID, "--json"},
	})
	if err != nil {
		return err
	}
	return decodeArchivedSession(output, expectedID)
}

func validSessionCreation(request SessionCreation) bool {
	if !validIdentifierValue(request.WorkspaceID) || !validIdentifierValue(request.Provider) ||
		!validIdentifierValue(request.Model) || !request.Mode.valid() ||
		request.Mode.provider != request.Provider ||
		(request.HasReasoning && !validIdentifierValue(request.Reasoning)) ||
		(!request.HasReasoning && request.Reasoning != "") ||
		strings.TrimSpace(request.Prompt) == "" || strings.IndexByte(request.Prompt, 0) >= 0 {
		return false
	}
	for _, label := range request.Labels {
		if !validSessionLabel(label) {
			return false
		}
	}
	return true
}

func decodeCreatedWorkspace(output []byte) (ActiveWorkspace, error) {
	var raw createdWorkspaceJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return ActiveWorkspace{}, err
	}
	if !raw.WorkspaceID.present || !raw.Project.present || !raw.Name.present ||
		!raw.Isolation.present || !raw.CWD.present {
		return ActiveWorkspace{}, fmt.Errorf("%w: созданный workspace не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(raw.WorkspaceID.value) || !validOpaqueValue(raw.Project.value) ||
		!validOpaqueValue(raw.Name.value) || !filepath.IsAbs(raw.CWD.value) {
		return ActiveWorkspace{}, fmt.Errorf("%w: созданный workspace содержит некорректное поле", ErrUnexpectedJSON)
	}
	if raw.Isolation.value != "local" {
		return ActiveWorkspace{}, fmt.Errorf("%w: созданный workspace содержит неожиданную изоляцию", ErrUnexpectedJSON)
	}
	return ActiveWorkspace{id: raw.WorkspaceID.value, name: raw.Name.value, cwd: raw.CWD.value}, nil
}

func decodeCreatedSession(output []byte) (CreatedSession, error) {
	var raw createdSessionJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return CreatedSession{}, err
	}
	if !raw.AgentID.present || !raw.Status.present || !raw.Provider.present ||
		!raw.CWD.present || !raw.Title.present {
		return CreatedSession{}, fmt.Errorf("%w: run не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(raw.AgentID.value) || !validOpaqueValue(raw.Provider.value) ||
		!filepath.IsAbs(raw.CWD.value) || !validOpaqueValue(raw.Title.value) {
		return CreatedSession{}, fmt.Errorf("%w: run содержит некорректное поле", ErrUnexpectedJSON)
	}
	if raw.Status.value != "created" && raw.Status.value != "running" {
		return CreatedSession{}, fmt.Errorf("%w: run содержит неизвестное состояние", ErrUnexpectedJSON)
	}
	return CreatedSession{id: raw.AgentID.value, cwd: raw.CWD.value}, nil
}

func decodeArchivedSession(output []byte, expectedID string) error {
	var raw archivedSessionJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return err
	}
	if !raw.AgentID.present || !raw.Status.present || !raw.ArchivedAt.present {
		return fmt.Errorf("%w: archive не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(raw.AgentID.value) || raw.Status.value != "archived" {
		return fmt.Errorf("%w: archive содержит некорректное поле", ErrUnexpectedJSON)
	}
	if raw.AgentID.value != expectedID {
		return ErrSessionIdentityMismatch
	}
	if _, err := time.Parse(time.RFC3339Nano, raw.ArchivedAt.value); err != nil {
		return fmt.Errorf("%w: archive содержит некорректное время", ErrUnexpectedJSON)
	}
	return nil
}
