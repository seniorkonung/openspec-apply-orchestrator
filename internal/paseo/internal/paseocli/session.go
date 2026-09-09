package paseocli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SessionLabel задаёт проверяемый фильтр метки без знания вызывающей стороной
// о синтаксисе аргументов Paseo CLI.
type SessionLabel struct {
	Key   string
	Value string
}

// SessionState — проверенное состояние сессии активного контракта Paseo.
type SessionState uint8

const (
	SessionInitializing SessionState = iota + 1
	SessionIdle
	SessionRunning
	SessionError
	SessionClosed
)

// ListedSession — минимальная проверенная проекция активной сессии.
type ListedSession struct {
	id    string
	state SessionState
}

func (session ListedSession) ID() string {
	return session.id
}

func (session ListedSession) State() SessionState {
	return session.state
}

// SessionInspection содержит только проверенные сведения, необходимые
// доменному слою для наблюдения собственной сессии.
type SessionInspection struct {
	id                   string
	state                SessionState
	archived             bool
	cwd                  string
	hasPendingPermission bool
	parentID             string
	hasParent            bool
}

func (inspection SessionInspection) ID() string {
	return inspection.id
}

func (inspection SessionInspection) State() SessionState {
	return inspection.state
}

func (inspection SessionInspection) Archived() bool {
	return inspection.archived
}

func (inspection SessionInspection) CWD() string {
	return inspection.cwd
}

func (inspection SessionInspection) HasPendingPermission() bool {
	return inspection.hasPendingPermission
}

func (inspection SessionInspection) ParentID() (string, bool) {
	return inspection.parentID, inspection.hasParent
}

// Схема значимых полей JSON команды ls Paseo CLI 0.8.0-beta.1.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/agent/ls.ts
type listedSessionJSON struct {
	ID     requiredValue[string] `json:"id"`
	Status requiredValue[string] `json:"status"`
}

// Схема значимых полей JSON команды inspect Paseo CLI 0.8.0-beta.1.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/agent/inspect.ts
type inspectedSessionJSON struct {
	ID                 requiredValue[string]                  `json:"Id"`
	Status             requiredValue[string]                  `json:"Status"`
	Archived           requiredValue[bool]                    `json:"Archived"`
	ArchivedAt         requiredNullable[string]               `json:"ArchivedAt"`
	CWD                requiredValue[string]                  `json:"Cwd"`
	PendingPermissions requiredValue[[]pendingPermissionJSON] `json:"PendingPermissions"`
	ParentAgentID      requiredNullable[string]               `json:"ParentAgentId"`
}

type pendingPermissionJSON struct {
	ID   requiredValue[string] `json:"id"`
	Tool requiredValue[string] `json:"tool"`
}

func (adapter *Adapter) ListSessions(
	ctx context.Context,
	labels []SessionLabel,
) ([]ListedSession, error) {
	args := []string{"ls", "--global"}
	for _, label := range labels {
		if !validSessionLabel(label) {
			return nil, ErrInvalidSessionQuery
		}
		args = append(args, "--label", label.Key+"="+label.Value)
	}
	args = append(args, "--json")

	output, err := adapter.Run(ctx, Invocation{Name: "ls", Arguments: args})
	if err != nil {
		return nil, err
	}
	return decodeListedSessions(output)
}

func (adapter *Adapter) InspectSession(
	ctx context.Context,
	expectedID string,
) (SessionInspection, error) {
	if !validIdentifierValue(expectedID) {
		return SessionInspection{}, ErrInvalidSessionQuery
	}
	output, err := adapter.Run(ctx, Invocation{
		Name:      "inspect",
		Arguments: []string{"inspect", expectedID, "--json"},
	})
	if err != nil {
		return SessionInspection{}, err
	}
	return decodeSessionInspection(output, expectedID)
}

func decodeListedSessions(output []byte) ([]ListedSession, error) {
	var raw []listedSessionJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: список сессий равен null", ErrUnexpectedJSON)
	}

	sessions := make([]ListedSession, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, item := range raw {
		if !item.ID.present || !item.Status.present {
			return nil, fmt.Errorf("%w: сессия %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validIdentifierValue(item.ID.value) {
			return nil, fmt.Errorf("%w: сессия %d содержит некорректный ID", ErrUnexpectedJSON, index+1)
		}
		state, err := parseSessionState(item.Status.value)
		if err != nil {
			return nil, fmt.Errorf("%w: сессия %d содержит неизвестное состояние", ErrUnexpectedJSON, index+1)
		}
		if _, exists := seen[item.ID.value]; exists {
			return nil, fmt.Errorf("%w: повторный id сессии", ErrUnexpectedJSON)
		}
		seen[item.ID.value] = struct{}{}
		sessions = append(sessions, ListedSession{id: item.ID.value, state: state})
	}
	return sessions, nil
}

func decodeSessionInspection(output []byte, expectedID string) (SessionInspection, error) {
	var raw inspectedSessionJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return SessionInspection{}, err
	}
	if err := validateSessionInspection(raw, expectedID); err != nil {
		return SessionInspection{}, err
	}

	state, _ := parseSessionState(raw.Status.value)
	inspection := SessionInspection{
		id:                   raw.ID.value,
		state:                state,
		archived:             raw.Archived.value,
		cwd:                  raw.CWD.value,
		hasPendingPermission: len(raw.PendingPermissions.value) > 0,
	}
	if raw.ParentAgentID.value != nil {
		inspection.parentID = *raw.ParentAgentID.value
		inspection.hasParent = true
	}
	return inspection, nil
}

func validateSessionInspection(raw inspectedSessionJSON, expectedID string) error {
	present := raw.ID.present && raw.Status.present && raw.Archived.present &&
		raw.ArchivedAt.present && raw.CWD.present && raw.PendingPermissions.present &&
		raw.ParentAgentID.present
	if !present {
		return fmt.Errorf("%w: inspect не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(raw.ID.value) || !filepath.IsAbs(raw.CWD.value) {
		return fmt.Errorf("%w: inspect содержит некорректное поле", ErrUnexpectedJSON)
	}
	if raw.ID.value != expectedID {
		return ErrSessionIdentityMismatch
	}
	state, err := parseSessionState(raw.Status.value)
	if err != nil {
		return fmt.Errorf("%w: inspect содержит неизвестное состояние", ErrUnexpectedJSON)
	}
	if raw.Archived.value != (raw.ArchivedAt.value != nil) {
		return fmt.Errorf("%w: Archived и ArchivedAt противоречат друг другу", ErrUnexpectedJSON)
	}
	if raw.ArchivedAt.value != nil {
		if _, err := time.Parse(time.RFC3339Nano, *raw.ArchivedAt.value); err != nil {
			return fmt.Errorf("%w: поле ArchivedAt", ErrUnexpectedJSON)
		}
	}
	if raw.Archived.value &&
		(state == SessionInitializing || state == SessionRunning || len(raw.PendingPermissions.value) > 0) {
		return fmt.Errorf("%w: архивирование противоречит активному состоянию", ErrUnexpectedJSON)
	}
	for index, permission := range raw.PendingPermissions.value {
		if !permission.ID.present || !permission.Tool.present ||
			!validIdentifierValue(permission.ID.value) || !validOpaqueValue(permission.Tool.value) {
			return fmt.Errorf("%w: PendingPermissions %d", ErrUnexpectedJSON, index+1)
		}
	}
	if raw.ParentAgentID.value != nil && !validIdentifierValue(*raw.ParentAgentID.value) {
		return fmt.Errorf("%w: поле ParentAgentId", ErrUnexpectedJSON)
	}
	return nil
}

func parseSessionState(status string) (SessionState, error) {
	switch status {
	case "initializing":
		return SessionInitializing, nil
	case "idle":
		return SessionIdle, nil
	case "running":
		return SessionRunning, nil
	case "error":
		return SessionError, nil
	case "closed":
		return SessionClosed, nil
	default:
		return 0, ErrUnexpectedJSON
	}
}

func validSessionLabel(label SessionLabel) bool {
	return validIdentifierValue(label.Key) && !strings.Contains(label.Key, "=") &&
		validOpaqueValue(label.Value)
}
