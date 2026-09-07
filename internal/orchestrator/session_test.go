package orchestrator

import (
	"errors"
	"testing"
)

func TestИдентификаторыИмеютРазныеДоменныеТипы(t *testing.T) {
	change, err := NewChangeKey("orchestrate-commit-preparation")
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	workspace, err := NewWorkspaceID("workspace-1")
	if err != nil {
		t.Fatalf("создать идентификатор workspace: %v", err)
	}
	session, err := NewSessionID("session-1")
	if err != nil {
		t.Fatalf("создать идентификатор сессии: %v", err)
	}

	if change.String() != "orchestrate-commit-preparation" {
		t.Fatalf("неожиданный ключ change: %q", change.String())
	}
	if workspace.String() != "workspace-1" {
		t.Fatalf("неожиданный идентификатор workspace: %q", workspace.String())
	}
	if session.String() != "session-1" {
		t.Fatalf("неожиданный идентификатор сессии: %q", session.String())
	}
}

func TestПустойИдентификаторНеСтановитсяДоверенным(t *testing.T) {
	checks := []struct {
		name string
		make func() error
	}{
		{name: "change", make: func() error { _, err := NewChangeKey(" \t"); return err }},
		{name: "workspace", make: func() error { _, err := NewWorkspaceID(""); return err }},
		{name: "сессия", make: func() error { _, err := NewSessionID("\n"); return err }},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.make(); err == nil {
				t.Fatal("ожидалась ошибка проверки идентификатора")
			}
		})
	}
}

func TestНаблюдениеСессииИмеетВзаимоисключающиеВарианты(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name     string
		sessions []UntrustedOwnSession
		assert   func(*testing.T, OwnSessionObservation)
	}{
		{
			name: "активная сессия отсутствует",
			assert: func(t *testing.T, got OwnSessionObservation) {
				if _, ok := got.(NoActiveOwnSession); !ok {
					t.Fatalf("ожидалось отсутствие сессии, получено %T", got)
				}
			},
		},
		{
			name:     "сессия работает",
			sessions: []UntrustedOwnSession{validSession("session-1", "running")},
			assert: func(t *testing.T, got OwnSessionObservation) {
				working, ok := got.(WorkingOwnSession)
				if !ok {
					t.Fatalf("ожидалась работающая сессия, получено %T", got)
				}
				if working.Session.ID().String() != "session-1" {
					t.Fatalf("неожиданная сессия: %q", working.Session.ID().String())
				}
			},
		},
		{
			name: "сессия ожидает действия",
			sessions: []UntrustedOwnSession{
				withAttention(validSession("session-1", "idle"), "finished"),
			},
			assert: func(t *testing.T, got OwnSessionObservation) {
				waiting, ok := got.(OwnSessionAwaitingAction)
				if !ok {
					t.Fatalf("ожидалась сессия, ожидающая действия, получено %T", got)
				}
				if waiting.Reason != SessionTurnFinished {
					t.Fatalf("неожиданная причина ожидания: %v", waiting.Reason)
				}
			},
		},
		{
			name:     "наблюдаемая сессия закрыта",
			sessions: []UntrustedOwnSession{validSession("session-1", "closed")},
			assert: func(t *testing.T, got OwnSessionObservation) {
				if _, ok := got.(ObservedOwnSessionClosed); !ok {
					t.Fatalf("ожидалось закрытие сессии, получено %T", got)
				}
			},
		},
		{
			name: "несколько собственных сессий неоднозначны",
			sessions: []UntrustedOwnSession{
				validSession("session-1", "running"),
				withAttention(validSession("session-2", "idle"), "permission"),
			},
			assert: func(t *testing.T, got OwnSessionObservation) {
				ambiguous, ok := got.(AmbiguousOwnSessions)
				if !ok {
					t.Fatalf("ожидалась неоднозначность, получено %T", got)
				}
				if len(ambiguous.Sessions) != 2 {
					t.Fatalf("ожидались две сессии, получено %d", len(ambiguous.Sessions))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ObserveOwnSessions(change, workspace, tt.sessions)
			if err != nil {
				t.Fatalf("построить наблюдение: %v", err)
			}
			tt.assert(t, got)
		})
	}
}

func TestПричинаОжиданияИмеетЗакрытыеТипизированныеВарианты(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name   string
		status string
		reason string
		want   SessionAttentionReason
	}{
		{name: "ход завершён", status: "idle", reason: "finished", want: SessionTurnFinished},
		{name: "агент завершился с ошибкой", status: "error", reason: "error", want: SessionAgentError},
		{name: "агент запросил разрешение", status: "idle", reason: "permission", want: SessionPermissionRequested},
		{name: "работающий агент запросил разрешение", status: "running", reason: "permission", want: SessionPermissionRequested},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{
				withAttention(validSession("session-1", tt.status), tt.reason),
			})
			if err != nil {
				t.Fatalf("построить наблюдение: %v", err)
			}
			waiting, ok := observation.(OwnSessionAwaitingAction)
			if !ok {
				t.Fatalf("ожидалось ожидание действия, получено %T", observation)
			}
			if waiting.Reason != tt.want {
				t.Fatalf("ожидалась причина %v, получено %v", tt.want, waiting.Reason)
			}
		})
	}
}

func TestНекорректныеМеткиНеСоздаютДоверенноеНаблюдение(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "нет владельца", mutate: func(labels map[string]string) { delete(labels, LabelOwner) }},
		{name: "чужой владелец", mutate: func(labels map[string]string) { labels[LabelOwner] = "manual" }},
		{name: "нет версии", mutate: func(labels map[string]string) { delete(labels, LabelVersion) }},
		{name: "другой change", mutate: func(labels map[string]string) { labels[LabelChange] = "other-change" }},
		{name: "другой тип сессии", mutate: func(labels map[string]string) { labels[LabelKind] = "apply" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validSession("session-1", "running")
			tt.mutate(raw.Labels)

			_, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{raw})
			if !errors.Is(err, ErrInvalidOwnership) {
				t.Fatalf("ожидалась ошибка принадлежности, получено %v", err)
			}
		})
	}
}

func TestНеизвестнаяВерсияПринадлежностиОтклоняется(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	raw := validSession("session-1", "running")
	raw.Labels[LabelVersion] = "2"

	_, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{raw})
	if !errors.Is(err, ErrUnsupportedOwnershipVersion) {
		t.Fatalf("ожидалась ошибка неизвестной версии, получено %v", err)
	}
}

func TestПротиворечивоеСостояниеОтклоняется(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name string
		raw  UntrustedOwnSession
	}{
		{
			name: "работающая сессия отмечена как завершившая ход",
			raw:  withAttention(validSession("session-1", "running"), "finished"),
		},
		{
			name: "закрытая сессия одновременно требует действия",
			raw:  withAttention(validSession("session-1", "closed"), "error"),
		},
		{
			name: "неизвестное состояние",
			raw:  validSession("session-1", "sleeping"),
		},
		{
			name: "завершённый ход без причины ожидания",
			raw:  validSession("session-1", "idle"),
		},
		{
			name: "ошибка без причины ожидания",
			raw:  validSession("session-1", "error"),
		},
		{
			name: "ошибка отмечена как завершённый ход",
			raw:  withAttention(validSession("session-1", "error"), "finished"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{tt.raw})
			if !errors.Is(err, ErrContradictorySessionState) {
				t.Fatalf("ожидалась ошибка состояния, получено %v", err)
			}
		})
	}
}

func TestСобственнаяСессияСвязанаСВыбраннымWorkspace(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name   string
		mutate func(*UntrustedOwnSession)
	}{
		{
			name: "метка workspace отсутствует",
			mutate: func(raw *UntrustedOwnSession) {
				delete(raw.Labels, LabelWorkspace)
			},
		},
		{
			name: "метка указывает на другой workspace",
			mutate: func(raw *UntrustedOwnSession) {
				raw.Labels[LabelWorkspace] = "workspace-2"
			},
		},
		{
			name: "проверенная сессия относится к другому workspace",
			mutate: func(raw *UntrustedOwnSession) {
				raw.WorkspaceID = "workspace-2"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validSession("session-1", "running")
			tt.mutate(&raw)

			_, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{raw})
			if !errors.Is(err, ErrInvalidOwnership) {
				t.Fatalf("ожидалась ошибка связи с workspace, получено %v", err)
			}
		})
	}
}

func validSession(id, status string) UntrustedOwnSession {
	return UntrustedOwnSession{
		ID:          id,
		WorkspaceID: "workspace-1",
		Status:      status,
		Labels: map[string]string{
			LabelOwner:     ManagedOwner,
			LabelVersion:   CurrentOwnershipVersion,
			LabelChange:    "orchestrate-commit-preparation",
			LabelKind:      CommitPreparationKind,
			LabelWorkspace: "workspace-1",
		},
	}
}

func mustWorkspaceID(t *testing.T, value string) WorkspaceID {
	t.Helper()
	id, err := NewWorkspaceID(value)
	if err != nil {
		t.Fatalf("создать идентификатор workspace: %v", err)
	}
	return id
}

func withAttention(session UntrustedOwnSession, reason string) UntrustedOwnSession {
	session.RequiresAttention = true
	session.AttentionReason = reason
	return session
}

func mustChangeKey(t *testing.T, value string) ChangeKey {
	t.Helper()
	key, err := NewChangeKey(value)
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	return key
}
