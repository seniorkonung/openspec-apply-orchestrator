package paseo

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestJSONСпискаСессийПроверяетсяДоСозданияТипов(t *testing.T) {
	output := marshalSessionJSON(t, []map[string]any{
		jsonAgentListItem("agent-123", "running"),
	})

	agents, err := decodeAgentList(output)
	if err != nil {
		t.Fatalf("прочитать список сессий: %v", err)
	}
	if len(agents) != 1 || agents[0].id.String() != "agent-123" || agents[0].status != "running" {
		t.Fatalf("неожиданный проверенный список: %+v", agents)
	}
}

func TestНекорректныйJSONСпискаСессийНеОзначаетПустойСписок(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "null вместо списка", value: nil},
		{name: "нет обязательного поля", value: []map[string]any{{"id": "agent-123"}}},
		{name: "неизвестное поле", value: []map[string]any{withSessionJSONField(jsonAgentListItem("agent-123", "running"), "token", true)}},
		{name: "shortId не является префиксом", value: []map[string]any{withSessionJSONField(jsonAgentListItem("agent-123", "running"), "shortId", "other")}},
		{name: "неизвестное состояние", value: []map[string]any{jsonAgentListItem("agent-123", "sleeping")}},
		{name: "повторный полный ID", value: []map[string]any{jsonAgentListItem("agent-123", "running"), jsonAgentListItem("agent-123", "idle")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeAgentList(marshalSessionJSON(t, tt.value))
			if !errors.Is(err, ErrUnexpectedJSON) {
				t.Fatalf("ожидалась ошибка JSON списка, получено %v", err)
			}
		})
	}
}

func TestJSONInspectПроверяетВложенныеПоля(t *testing.T) {
	inspection := jsonAgentInspection("idle")
	inspection["LastUsage"] = map[string]any{
		"InputTokens": 10, "OutputTokens": 4, "CachedTokens": 2, "CostUsd": 0.01,
	}
	inspection["Capabilities"] = map[string]any{
		"Streaming": true, "Persistence": true, "DynamicModes": true, "McpServers": false,
	}
	inspection["AvailableModes"] = []map[string]any{{"id": "plan", "label": "План"}}
	inspection["PendingPermissions"] = []map[string]any{{"id": "permission-1", "tool": "Bash"}}

	got, err := decodeAgentInspection(marshalSessionJSON(t, inspection))
	if err != nil {
		t.Fatalf("прочитать inspect: %v", err)
	}
	if got.ID.value != "agent-123" || got.Status.value != "idle" || len(got.PendingPermissions.value) != 1 {
		t.Fatalf("неожиданный проверенный inspect: id=%q status=%q", got.ID.value, got.Status.value)
	}
}

func TestПротиворечивыйИлиНеполныйJSONInspectОтклоняется(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "нет обязательного поля", mutate: func(value map[string]any) { delete(value, "Cwd") }},
		{name: "неизвестное поле", mutate: func(value map[string]any) { value["Token"] = "секрет" }},
		{name: "неверная дата", mutate: func(value map[string]any) { value["UpdatedAt"] = "вчера" }},
		{
			name: "архивирование без даты",
			mutate: func(value map[string]any) {
				value["Status"] = "idle"
				value["Archived"] = true
			},
		},
		{
			name: "архивированная сессия работает",
			mutate: func(value map[string]any) {
				value["Archived"] = true
				value["ArchivedAt"] = "2026-09-07T09:20:00Z"
			},
		},
		{
			name: "архивированная сессия ждёт разрешение",
			mutate: func(value map[string]any) {
				value["Status"] = "idle"
				value["Archived"] = true
				value["ArchivedAt"] = "2026-09-07T09:20:00Z"
				value["PendingPermissions"] = []map[string]any{{"id": "permission-1", "tool": "Bash"}}
			},
		},
		{
			name: "неполное вложенное поле",
			mutate: func(value map[string]any) {
				value["Capabilities"] = map[string]any{"Streaming": true}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := jsonAgentInspection("running")
			tt.mutate(value)

			_, err := decodeAgentInspection(marshalSessionJSON(t, value))
			if !errors.Is(err, ErrUnexpectedJSON) {
				t.Fatalf("ожидалась ошибка JSON inspect, получено %v", err)
			}
		})
	}
}

func jsonAgentListItem(id, status string) map[string]any {
	return map[string]any{
		"id": id, "shortId": id[:7], "name": "агент", "provider": "codex/gpt-5",
		"thinking": "high", "status": status, "cwd": "/repo", "created": "just now",
	}
}

func jsonAgentInspection(status string) map[string]any {
	return map[string]any{
		"Id": "agent-123", "Name": "агент", "Provider": "codex", "Model": "gpt-5",
		"Thinking": "high", "Status": status, "Archived": false, "ArchivedAt": nil,
		"Mode": "default", "Cwd": "/repo", "CreatedAt": "2026-09-07T09:00:00Z",
		"UpdatedAt": "2026-09-07T09:10:00Z", "LastUsage": nil, "Capabilities": nil,
		"AvailableModes": nil, "PendingPermissions": []map[string]any{}, "Worktree": nil,
		"ParentAgentId": nil,
	}
}

func withSessionJSONField(value map[string]any, key string, field any) map[string]any {
	value[key] = field
	return value
}

func marshalSessionJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("собрать JSON сессии: %v", err)
	}
	return encoded
}
