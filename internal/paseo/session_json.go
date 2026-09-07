package paseo

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
)

type listedAgent struct {
	id     orchestrator.SessionID
	status string
}

type agentListItemJSON struct {
	ID       requiredValue[string] `json:"id"`
	ShortID  requiredValue[string] `json:"shortId"`
	Name     requiredValue[string] `json:"name"`
	Provider requiredValue[string] `json:"provider"`
	Thinking requiredValue[string] `json:"thinking"`
	Status   requiredValue[string] `json:"status"`
	CWD      requiredValue[string] `json:"cwd"`
	Created  requiredValue[string] `json:"created"`
}

func decodeAgentList(output []byte) ([]listedAgent, error) {
	var items []agentListItemJSON
	if err := decodeStrictJSON(output, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, fmt.Errorf("%w: список сессий равен null", ErrUnexpectedJSON)
	}

	agents := make([]listedAgent, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		if !item.ID.present || !item.ShortID.present || !item.Name.present || !item.Provider.present ||
			!item.Thinking.present || !item.Status.present || !item.CWD.present || !item.Created.present {
			return nil, fmt.Errorf("%w: сессия %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
		if !validIdentifierValue(item.ID.value) || !validIdentifierValue(item.ShortID.value) ||
			!validOpaqueValue(item.Name.value) || !validOpaqueValue(item.Provider.value) ||
			!validOpaqueValue(item.Thinking.value) || !validAgentStatus(item.Status.value) ||
			!validOpaqueValue(item.CWD.value) || !validOpaqueValue(item.Created.value) {
			return nil, fmt.Errorf("%w: сессия %d содержит некорректное поле", ErrUnexpectedJSON, index+1)
		}
		if len(item.ShortID.value) > len(item.ID.value) || item.ID.value[:len(item.ShortID.value)] != item.ShortID.value {
			return nil, fmt.Errorf("%w: shortId сессии %d не является префиксом id", ErrUnexpectedJSON, index+1)
		}
		if _, duplicate := seen[item.ID.value]; duplicate {
			return nil, fmt.Errorf("%w: повторный id сессии", ErrUnexpectedJSON)
		}
		seen[item.ID.value] = struct{}{}
		id, err := orchestrator.NewSessionID(item.ID.value)
		if err != nil {
			return nil, fmt.Errorf("%w: сессия %d содержит некорректный id", ErrUnexpectedJSON, index+1)
		}
		agents = append(agents, listedAgent{id: id, status: item.Status.value})
	}
	return agents, nil
}

type agentInspectionJSON struct {
	ID                 requiredValue[string]                  `json:"Id"`
	Name               requiredValue[string]                  `json:"Name"`
	Provider           requiredValue[string]                  `json:"Provider"`
	Model              requiredValue[string]                  `json:"Model"`
	Thinking           requiredValue[string]                  `json:"Thinking"`
	Status             requiredValue[string]                  `json:"Status"`
	Archived           requiredValue[bool]                    `json:"Archived"`
	ArchivedAt         requiredNullable[string]               `json:"ArchivedAt"`
	Mode               requiredValue[string]                  `json:"Mode"`
	CWD                requiredValue[string]                  `json:"Cwd"`
	CreatedAt          requiredValue[string]                  `json:"CreatedAt"`
	UpdatedAt          requiredValue[string]                  `json:"UpdatedAt"`
	LastUsage          requiredNullable[lastUsageJSON]        `json:"LastUsage"`
	Capabilities       requiredNullable[capabilitiesJSON]     `json:"Capabilities"`
	AvailableModes     requiredNullable[[]availableModeJSON]  `json:"AvailableModes"`
	PendingPermissions requiredValue[[]pendingPermissionJSON] `json:"PendingPermissions"`
	Worktree           requiredNullable[string]               `json:"Worktree"`
	ParentAgentID      requiredNullable[string]               `json:"ParentAgentId"`
}

type lastUsageJSON struct {
	InputTokens  requiredValue[int64]   `json:"InputTokens"`
	OutputTokens requiredValue[int64]   `json:"OutputTokens"`
	CachedTokens requiredValue[int64]   `json:"CachedTokens"`
	CostUSD      requiredValue[float64] `json:"CostUsd"`
}

type capabilitiesJSON struct {
	Streaming    requiredValue[bool] `json:"Streaming"`
	Persistence  requiredValue[bool] `json:"Persistence"`
	DynamicModes requiredValue[bool] `json:"DynamicModes"`
	MCPServers   requiredValue[bool] `json:"McpServers"`
}

type availableModeJSON struct {
	ID    requiredValue[string] `json:"id"`
	Label requiredValue[string] `json:"label"`
}

type pendingPermissionJSON struct {
	ID   requiredValue[string] `json:"id"`
	Tool requiredValue[string] `json:"tool"`
}

func decodeAgentInspection(output []byte) (agentInspectionJSON, error) {
	var inspection agentInspectionJSON
	if err := decodeStrictJSON(output, &inspection); err != nil {
		return agentInspectionJSON{}, err
	}
	if err := validateAgentInspection(inspection); err != nil {
		return agentInspectionJSON{}, err
	}
	return inspection, nil
}

func validateAgentInspection(inspection agentInspectionJSON) error {
	present := inspection.ID.present && inspection.Name.present && inspection.Provider.present &&
		inspection.Model.present && inspection.Thinking.present && inspection.Status.present &&
		inspection.Archived.present && inspection.ArchivedAt.present && inspection.Mode.present &&
		inspection.CWD.present && inspection.CreatedAt.present && inspection.UpdatedAt.present &&
		inspection.LastUsage.present && inspection.Capabilities.present && inspection.AvailableModes.present &&
		inspection.PendingPermissions.present && inspection.Worktree.present && inspection.ParentAgentID.present
	if !present {
		return fmt.Errorf("%w: inspect не содержит обязательное поле", ErrUnexpectedJSON)
	}
	if !validIdentifierValue(inspection.ID.value) || !validOpaqueValue(inspection.Name.value) ||
		!validOpaqueValue(inspection.Provider.value) || !validOpaqueValue(inspection.Model.value) ||
		!validOpaqueValue(inspection.Thinking.value) || !validAgentStatus(inspection.Status.value) ||
		!validOpaqueValue(inspection.Mode.value) || !filepath.IsAbs(inspection.CWD.value) {
		return fmt.Errorf("%w: inspect содержит некорректное поле", ErrUnexpectedJSON)
	}
	if _, err := time.Parse(time.RFC3339Nano, inspection.CreatedAt.value); err != nil {
		return fmt.Errorf("%w: поле CreatedAt", ErrUnexpectedJSON)
	}
	if _, err := time.Parse(time.RFC3339Nano, inspection.UpdatedAt.value); err != nil {
		return fmt.Errorf("%w: поле UpdatedAt", ErrUnexpectedJSON)
	}
	if inspection.Archived.value != (inspection.ArchivedAt.value != nil) {
		return fmt.Errorf("%w: Archived и ArchivedAt противоречат друг другу", ErrUnexpectedJSON)
	}
	if inspection.Archived.value &&
		(inspection.Status.value == "initializing" || inspection.Status.value == "running" ||
			len(inspection.PendingPermissions.value) > 0) {
		return fmt.Errorf("%w: архивирование противоречит активному состоянию", ErrUnexpectedJSON)
	}
	if inspection.ArchivedAt.value != nil {
		if _, err := time.Parse(time.RFC3339Nano, *inspection.ArchivedAt.value); err != nil {
			return fmt.Errorf("%w: поле ArchivedAt", ErrUnexpectedJSON)
		}
	}
	if err := validateInspectionDetails(inspection); err != nil {
		return err
	}
	return nil
}

func validateInspectionDetails(inspection agentInspectionJSON) error {
	if usage := inspection.LastUsage.value; usage != nil {
		if !usage.InputTokens.present || !usage.OutputTokens.present || !usage.CachedTokens.present ||
			!usage.CostUSD.present || usage.InputTokens.value < 0 || usage.OutputTokens.value < 0 ||
			usage.CachedTokens.value < 0 || usage.CostUSD.value < 0 {
			return fmt.Errorf("%w: поле LastUsage", ErrUnexpectedJSON)
		}
	}
	if capabilities := inspection.Capabilities.value; capabilities != nil {
		if !capabilities.Streaming.present || !capabilities.Persistence.present ||
			!capabilities.DynamicModes.present || !capabilities.MCPServers.present {
			return fmt.Errorf("%w: поле Capabilities", ErrUnexpectedJSON)
		}
	}
	if modes := inspection.AvailableModes.value; modes != nil {
		for index, mode := range *modes {
			if !mode.ID.present || !mode.Label.present || !validIdentifierValue(mode.ID.value) ||
				!validOpaqueValue(mode.Label.value) {
				return fmt.Errorf("%w: AvailableModes %d", ErrUnexpectedJSON, index+1)
			}
		}
	}
	for index, permission := range inspection.PendingPermissions.value {
		if !permission.ID.present || !permission.Tool.present || !validIdentifierValue(permission.ID.value) ||
			!validOpaqueValue(permission.Tool.value) {
			return fmt.Errorf("%w: PendingPermissions %d", ErrUnexpectedJSON, index+1)
		}
	}
	if inspection.Worktree.value != nil && !validOpaqueValue(*inspection.Worktree.value) {
		return fmt.Errorf("%w: поле Worktree", ErrUnexpectedJSON)
	}
	if inspection.ParentAgentID.value != nil && !validIdentifierValue(*inspection.ParentAgentID.value) {
		return fmt.Errorf("%w: поле ParentAgentId", ErrUnexpectedJSON)
	}
	return nil
}

func validAgentStatus(status string) bool {
	switch status {
	case "initializing", "idle", "running", "error", "closed":
		return true
	default:
		return false
	}
}
