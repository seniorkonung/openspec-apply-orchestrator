package paseo

import (
	"context"
	"fmt"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

type labelFilter struct {
	key   string
	value string
}

type listedAgent struct {
	id    orchestrator.SessionID
	state paseocli.SessionState
}

func (client *Client) FindOwnSessions(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) (orchestrator.OwnSessionObservation, error) {
	canonicalCWD, exact, err := client.findOwnSessionCandidates(ctx, change, workspace, cwd)
	if err != nil {
		return nil, err
	}
	if len(exact) != 1 {
		return observeListedAgents(change, workspace, exact)
	}
	return client.inspectOwnSession(ctx, exact[0].id, change, workspace, canonicalCWD)
}

func (client *Client) ObserveOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
	known orchestrator.SessionID,
) (orchestrator.OwnSessionObservation, error) {
	if known.String() == "" {
		return nil, fmt.Errorf("%w: отсутствует ID известной сессии", ErrInvalidDirectoryQuery)
	}
	canonicalCWD, exact, err := client.findOwnSessionCandidates(ctx, change, workspace, cwd)
	if err != nil {
		return nil, err
	}

	switch len(exact) {
	case 0:
		observation, err := client.inspectOwnSession(ctx, known, change, workspace, canonicalCWD)
		if err != nil {
			return nil, err
		}
		if _, closed := observation.(orchestrator.ObservedOwnSessionClosed); !closed {
			return nil, fmt.Errorf(
				"%w: известная сессия %s активна в inspect, но отсутствует в фильтрах",
				ErrCorruptSessionOwnership,
				known.String(),
			)
		}
		return observation, nil
	case 1:
		if exact[0].id != known {
			return nil, fmt.Errorf(
				"%w: ожидалась %s, найдена %s",
				ErrSessionIdentityMismatch,
				known.String(),
				exact[0].id.String(),
			)
		}
		return client.inspectOwnSession(ctx, known, change, workspace, canonicalCWD)
	default:
		return observeListedAgents(change, workspace, exact)
	}
}

func (client *Client) findOwnSessionCandidates(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) (string, []listedAgent, error) {
	if change.String() == "" || workspace.String() == "" {
		return "", nil, fmt.Errorf("%w: отсутствует ключ change или workspace", ErrInvalidDirectoryQuery)
	}
	canonicalCWD, err := canonicalDirectory(cwd)
	if err != nil {
		return "", nil, err
	}

	broadFilters := []labelFilter{
		{key: orchestrator.LabelOwner, value: orchestrator.ManagedOwner},
		{key: orchestrator.LabelChange, value: change.String()},
	}
	exactFilters := append([]labelFilter{}, broadFilters...)
	exactFilters = append(exactFilters,
		labelFilter{key: orchestrator.LabelVersion, value: orchestrator.CurrentOwnershipVersion},
		labelFilter{key: orchestrator.LabelKind, value: orchestrator.CommitPreparationKind},
		labelFilter{key: orchestrator.LabelWorkspace, value: workspace.String()},
	)

	broad, err := client.listAgents(ctx, broadFilters)
	if err != nil {
		return "", nil, err
	}
	exact, err := client.listAgents(ctx, exactFilters)
	if err != nil {
		return "", nil, err
	}
	if !sameAgentSet(broad, exact) {
		return "", nil, fmt.Errorf(
			"%w: широкий набор %d, точный набор %d",
			ErrCorruptSessionOwnership,
			len(broad),
			len(exact),
		)
	}
	return canonicalCWD, exact, nil
}

func (client *Client) inspectOwnSession(
	ctx context.Context,
	expectedID orchestrator.SessionID,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	canonicalCWD string,
) (orchestrator.OwnSessionObservation, error) {
	inspection, err := client.inspectAgent(ctx, expectedID)
	if err != nil {
		return nil, err
	}
	raw, err := inspectionToUntrustedSession(inspection, expectedID, change, workspace, canonicalCWD)
	if err != nil {
		return nil, err
	}
	return orchestrator.ObserveOwnSessions(change, workspace, []orchestrator.UntrustedOwnSession{raw})
}

func (client *Client) listAgents(ctx context.Context, filters []labelFilter) ([]listedAgent, error) {
	labels := make([]paseocli.SessionLabel, 0, len(filters))
	for _, filter := range filters {
		labels = append(labels, paseocli.SessionLabel{Key: filter.key, Value: filter.value})
	}
	sessions, err := client.adapter.ListSessions(ctx, labels)
	if err != nil {
		return nil, err
	}
	agents := make([]listedAgent, 0, len(sessions))
	for index, session := range sessions {
		id, err := orchestrator.NewSessionID(session.ID())
		if err != nil {
			return nil, fmt.Errorf("%w: сессия %d содержит некорректный ID", ErrUnexpectedJSON, index+1)
		}
		agents = append(agents, listedAgent{id: id, state: session.State()})
	}
	return agents, nil
}

func (client *Client) inspectAgent(
	ctx context.Context,
	id orchestrator.SessionID,
) (paseocli.SessionInspection, error) {
	return client.adapter.InspectSession(ctx, id.String())
}

func sameAgentSet(left, right []listedAgent) bool {
	if len(left) != len(right) {
		return false
	}
	leftIDs := make(map[string]struct{}, len(left))
	for _, agent := range left {
		leftIDs[agent.id.String()] = struct{}{}
	}
	for _, agent := range right {
		if _, ok := leftIDs[agent.id.String()]; !ok {
			return false
		}
	}
	return true
}

func observeListedAgents(
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	agents []listedAgent,
) (orchestrator.OwnSessionObservation, error) {
	raw := make([]orchestrator.UntrustedOwnSession, 0, len(agents))
	for _, agent := range agents {
		raw = append(raw, untrustedOwnSession(agent.id, agent.state, false, false, change, workspace))
	}
	return orchestrator.ObserveOwnSessions(change, workspace, raw)
}

func inspectionToUntrustedSession(
	inspection paseocli.SessionInspection,
	expectedID orchestrator.SessionID,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	canonicalCWD string,
) (orchestrator.UntrustedOwnSession, error) {
	if inspection.ID() != expectedID.String() {
		return orchestrator.UntrustedOwnSession{}, ErrSessionIdentityMismatch
	}
	actualCWD, err := canonicalDirectory(inspection.CWD())
	if err != nil {
		return orchestrator.UntrustedOwnSession{}, err
	}
	if actualCWD != canonicalCWD {
		return orchestrator.UntrustedOwnSession{}, ErrSessionWorkingDirectoryMismatch
	}
	if _, hasParent := inspection.ParentID(); hasParent {
		return orchestrator.UntrustedOwnSession{}, ErrForeignSessionParent
	}
	return untrustedOwnSession(
		expectedID,
		inspection.State(),
		inspection.Archived(),
		inspection.HasPendingPermission(),
		change,
		workspace,
	), nil
}

func untrustedOwnSession(
	id orchestrator.SessionID,
	state paseocli.SessionState,
	archived bool,
	permission bool,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
) orchestrator.UntrustedOwnSession {
	status := sessionStateName(state)
	if archived {
		status = "closed"
	}
	requiresAttention := permission
	reason := ""
	switch {
	case status == "closed":
		requiresAttention = false
	case status == "error":
		requiresAttention = true
		reason = "error"
	case permission:
		reason = "permission"
	case status == "idle":
		requiresAttention = true
		reason = "finished"
	}

	return orchestrator.UntrustedOwnSession{
		ID:                id.String(),
		WorkspaceID:       workspace.String(),
		Status:            status,
		RequiresAttention: requiresAttention,
		AttentionReason:   reason,
		Labels: map[string]string{
			orchestrator.LabelOwner:     orchestrator.ManagedOwner,
			orchestrator.LabelVersion:   orchestrator.CurrentOwnershipVersion,
			orchestrator.LabelChange:    change.String(),
			orchestrator.LabelKind:      orchestrator.CommitPreparationKind,
			orchestrator.LabelWorkspace: workspace.String(),
		},
	}
}

func sessionStateName(state paseocli.SessionState) string {
	switch state {
	case paseocli.SessionInitializing:
		return "initializing"
	case paseocli.SessionIdle:
		return "idle"
	case paseocli.SessionRunning:
		return "running"
	case paseocli.SessionError:
		return "error"
	case paseocli.SessionClosed:
		return "closed"
	default:
		panic("непроверенное состояние сессии Paseo")
	}
}
