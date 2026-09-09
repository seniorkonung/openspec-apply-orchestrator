package paseo

import (
	"context"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
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
	event, err := client.adapter.WaitSession(ctx, session.String())
	if err != nil {
		return nil, err
	}
	switch event {
	case paseocli.WaitEventIdle:
		return WaitIdle{}, nil
	case paseocli.WaitEventTimeout:
		return WaitTimeout{}, nil
	case paseocli.WaitEventPermission:
		return WaitPermission{}, nil
	case paseocli.WaitEventAgentError:
		return WaitAgentError{}, nil
	default:
		return nil, ErrUnexpectedJSON
	}
}
