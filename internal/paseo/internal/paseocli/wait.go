package paseocli

import (
	"context"
	"fmt"
)

// WaitEvent — проверенный сигнал события блокирующего ожидания.
type WaitEvent uint8

const (
	WaitEventIdle WaitEvent = iota + 1
	WaitEventTimeout
	WaitEventPermission
	WaitEventAgentError
)

// Схема значимых полей JSON команды wait Paseo CLI 0.8.0-beta.1. Message является
// непрозрачным внешним текстом и отбрасывается сразу после проверки типа.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/agent/wait.ts
type waitResultJSON struct {
	AgentID requiredValue[string] `json:"agentId"`
	Status  requiredValue[string] `json:"status"`
	Message requiredValue[string] `json:"message"`
}

func (adapter *Adapter) WaitSession(ctx context.Context, expectedID string) (WaitEvent, error) {
	if !validIdentifierValue(expectedID) {
		return 0, ErrInvalidWaitSessionID
	}
	output, err := adapter.RunUntilContextDone(ctx, Invocation{
		Name:      "wait",
		Arguments: []string{"wait", expectedID, "--json"},
	})
	if err != nil {
		return 0, err
	}
	return decodeWaitEvent(output, expectedID)
}

func decodeWaitEvent(output []byte, expectedID string) (WaitEvent, error) {
	var raw waitResultJSON
	if err := decodeAdditiveJSON(output, &raw); err != nil {
		return 0, err
	}
	if !raw.AgentID.present || !raw.Status.present || !raw.Message.present {
		return 0, fmt.Errorf("%w: wait не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(raw.AgentID.value) {
		return 0, fmt.Errorf("%w: wait содержит некорректный agentId", ErrUnexpectedJSON)
	}
	if raw.AgentID.value != expectedID {
		return 0, ErrWaitSessionIdentityMismatch
	}

	switch raw.Status.value {
	case "idle":
		return WaitEventIdle, nil
	case "timeout":
		return WaitEventTimeout, nil
	case "permission":
		return WaitEventPermission, nil
	case "error":
		return WaitEventAgentError, nil
	default:
		return 0, fmt.Errorf("%w: неизвестный статус wait", ErrUnexpectedJSON)
	}
}
