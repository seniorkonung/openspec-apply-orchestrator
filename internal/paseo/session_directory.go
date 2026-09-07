package paseo

import (
	"context"
	"fmt"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

type labelFilter struct {
	key   string
	value string
}

func (client *Client) FindOwnSessions(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) (orchestrator.OwnSessionObservation, error) {
	if change.String() == "" || workspace.String() == "" {
		return nil, fmt.Errorf("%w: отсутствует ключ change или workspace", ErrInvalidDirectoryQuery)
	}
	canonicalCWD, err := canonicalDirectory(cwd)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	exact, err := client.listAgents(ctx, exactFilters)
	if err != nil {
		return nil, err
	}
	if !sameAgentSet(broad, exact) {
		return nil, fmt.Errorf(
			"%w: широкий набор %d, точный набор %d",
			ErrCorruptSessionOwnership,
			len(broad),
			len(exact),
		)
	}

	if len(exact) != 1 {
		return observeListedAgents(change, workspace, exact)
	}

	inspection, err := client.inspectAgent(ctx, exact[0].id)
	if err != nil {
		return nil, err
	}
	raw, err := inspection.toUntrustedSession(exact[0].id, change, workspace, canonicalCWD)
	if err != nil {
		return nil, err
	}
	return orchestrator.ObserveOwnSessions(change, workspace, []orchestrator.UntrustedOwnSession{raw})
}

func (client *Client) listAgents(ctx context.Context, filters []labelFilter) ([]listedAgent, error) {
	args := []string{"ls", "--global"}
	for _, filter := range filters {
		args = append(args, "--label", filter.key+"="+filter.value)
	}
	args = append(args, "--json")

	output, err := client.runner.run(ctx, command{name: "ls", args: args})
	if err != nil {
		return nil, err
	}
	return decodeAgentList(output)
}

func (client *Client) inspectAgent(ctx context.Context, id orchestrator.SessionID) (agentInspectionJSON, error) {
	output, err := client.runner.run(ctx, command{
		name: "inspect",
		args: []string{"inspect", id.String(), "--json"},
	})
	if err != nil {
		return agentInspectionJSON{}, err
	}
	return decodeAgentInspection(output)
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
		raw = append(raw, untrustedOwnSession(agent.id, agent.status, false, change, workspace))
	}
	return orchestrator.ObserveOwnSessions(change, workspace, raw)
}

func (inspection agentInspectionJSON) toUntrustedSession(
	expectedID orchestrator.SessionID,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	canonicalCWD string,
) (orchestrator.UntrustedOwnSession, error) {
	if inspection.ID.value != expectedID.String() {
		return orchestrator.UntrustedOwnSession{}, ErrSessionIdentityMismatch
	}
	actualCWD, err := canonicalDirectory(inspection.CWD.value)
	if err != nil {
		return orchestrator.UntrustedOwnSession{}, err
	}
	if actualCWD != canonicalCWD {
		return orchestrator.UntrustedOwnSession{}, ErrSessionWorkingDirectoryMismatch
	}
	if inspection.ParentAgentID.value != nil {
		return orchestrator.UntrustedOwnSession{}, ErrForeignSessionParent
	}

	status := inspection.Status.value
	hasPendingPermission := false
	if inspection.Archived.value {
		status = "closed"
	} else if len(inspection.PendingPermissions.value) > 0 {
		hasPendingPermission = true
	}
	return untrustedOwnSession(expectedID, status, hasPendingPermission, change, workspace), nil
}

func untrustedOwnSession(
	id orchestrator.SessionID,
	status string,
	permission bool,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
) orchestrator.UntrustedOwnSession {
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
