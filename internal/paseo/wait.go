package paseo

import (
	"context"
	"fmt"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

type WaitResult interface {
	isWaitResult()
}

type WaitIdle struct{}

type WaitTimeout struct{}

type WaitPermission struct{}

type WaitAgentError struct{}

func (WaitIdle) isWaitResult()       {}
func (WaitTimeout) isWaitResult()    {}
func (WaitPermission) isWaitResult() {}
func (WaitAgentError) isWaitResult() {}

func (client *Client) Wait(
	ctx context.Context,
	session orchestrator.SessionID,
) (WaitResult, error) {
	if !validIdentifierValue(session.String()) {
		return nil, ErrInvalidWaitSessionID
	}

	output, err := client.runner.runUntilContextDone(ctx, command{
		name: "wait",
		args: []string{"wait", session.String(), "--json"},
	})
	if err != nil {
		return nil, err
	}

	return decodeWaitResult(output, session)
}

// Схема соответствует JSON Paseo CLI 0.7.2. Message намеренно остаётся
// непрозрачным внешним текстом, ограниченным общим пределом stdout runner.
// Источник: https://github.com/getpaseo/paseo/blob/v0.7.2/packages/cli/src/commands/agent/wait.ts
type waitResultJSON struct {
	AgentID requiredValue[string] `json:"agentId"`
	Status  requiredValue[string] `json:"status"`
	Message requiredValue[string] `json:"message"`
}

func decodeWaitResult(
	output []byte,
	expectedSession orchestrator.SessionID,
) (WaitResult, error) {
	var raw waitResultJSON
	if err := decodeStrictJSON(output, &raw); err != nil {
		return nil, err
	}
	if !raw.AgentID.present || !raw.Status.present || !raw.Message.present {
		return nil, fmt.Errorf("%w: wait не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(raw.AgentID.value) {
		return nil, fmt.Errorf("%w: wait содержит некорректный agentId", ErrUnexpectedJSON)
	}
	if raw.AgentID.value != expectedSession.String() {
		return nil, ErrWaitSessionIdentityMismatch
	}

	switch raw.Status.value {
	case "idle":
		return WaitIdle{}, nil
	case "timeout":
		return WaitTimeout{}, nil
	case "permission":
		return WaitPermission{}, nil
	case "error":
		return WaitAgentError{}, nil
	default:
		return nil, fmt.Errorf("%w: неизвестный статус wait", ErrUnexpectedJSON)
	}
}
