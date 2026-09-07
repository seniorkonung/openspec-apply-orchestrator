package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestЯдроПеречитываетСостояниеМеждуВоздействиями(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:          NoManagedWorkspace{},
		sessions:           NoActiveOwnSession{},
		workspaceID:        workspace,
		sessionAfterCreate: ownSessionObservation(t, change, workspace, "session-1", "running", ""),
	}
	clock := &fakeReconcileClock{wait: func() {
		gateway.sessions = ownSessionObservation(t, change, workspace, "session-1", "idle", "finished")
	}}
	clock.events = &gateway.events
	reconciler, err := NewPhaseOneReconciler(gateway, clock, time.Second)
	if err != nil {
		t.Fatalf("создать ядро сопровождения: %v", err)
	}

	if err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("сопроводить сессию: %v", err)
	}

	want := []string{
		"workspace:read", "workspace:create",
		"workspace:read", "sessions:read", "session:create",
		"workspace:read", "sessions:read", "clock:wait",
		"workspace:read", "sessions:read", "session:archive",
	}
	if !reflect.DeepEqual(gateway.events, want) {
		t.Fatalf("неожиданная последовательность сопровождения:\nполучено: %v\nожидалось: %v", gateway.events, want)
	}
	if gateway.createdSessions != 1 || gateway.archivedSessions != 1 {
		t.Fatalf("неожиданные мутации: create=%d archive=%d", gateway.createdSessions, gateway.archivedSessions)
	}
}

func TestНовоеЯдроПродолжаетВидимуюСессиюБезПовторногоПоручения(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:   OneManagedWorkspace{ID: workspace},
		sessions:    ownSessionObservation(t, change, workspace, "session-visible", "running", ""),
		workspaceID: workspace,
	}

	for attempt := 1; attempt <= 2; attempt++ {
		clock := &fakeReconcileClock{err: context.Canceled}
		reconciler, err := NewPhaseOneReconciler(gateway, clock, time.Second)
		if err != nil {
			t.Fatalf("создать ядро %d: %v", attempt, err)
		}
		err = reconciler.Run(context.Background(), change, "/repo")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена ядра %d, получено %v", attempt, err)
		}
	}

	if gateway.createdSessions != 0 || gateway.archivedSessions != 0 {
		t.Fatalf("видимая сессия получила лишнее воздействие: create=%d archive=%d", gateway.createdSessions, gateway.archivedSessions)
	}
	if got := countEvent(gateway.events, "sessions:read"); got != 2 {
		t.Fatalf("каждое новое ядро должно найти сессию заново, чтений: %d", got)
	}
}

func TestЗакрытиеИНуждаВЧеловекеНеВызываютНовыхМутаций(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name     string
		sessions OwnSessionObservation
		wantErr  error
	}{
		{
			name:     "сессия закрылась в текущем наблюдении",
			sessions: ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		{
			name:     "агент запросил разрешение",
			sessions: ownSessionObservation(t, change, workspace, "session-1", "idle", "permission"),
			wantErr:  ErrSessionNeedsAction,
		},
		{
			name:     "агент завершился с ошибкой",
			sessions: ownSessionObservation(t, change, workspace, "session-1", "error", "error"),
			wantErr:  ErrSessionNeedsAction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &fakePhaseOneGateway{
				workspace:   OneManagedWorkspace{ID: workspace},
				sessions:    tt.sessions,
				workspaceID: workspace,
			}
			reconciler := mustPhaseOneReconciler(t, gateway, &fakeReconcileClock{})

			err := reconciler.Run(context.Background(), change, "/repo")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.wantErr, err)
			}
			if gateway.createdSessions != 0 || gateway.archivedSessions != 0 {
				t.Fatalf("выполнено лишнее воздействие: create=%d archive=%d", gateway.createdSessions, gateway.archivedSessions)
			}
		})
	}
}

func TestНеоднозначностьОстанавливаетЯдроБезВыбора(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	otherWorkspace := mustWorkspaceID(t, "workspace-2")

	tests := []struct {
		name      string
		workspace ManagedWorkspaceObservation
		sessions  OwnSessionObservation
		wantErr   error
	}{
		{
			name:      "два workspace",
			workspace: AmbiguousManagedWorkspaces{IDs: []WorkspaceID{workspace, otherWorkspace}},
			wantErr:   ErrAmbiguousManagedWorkspaces,
		},
		{
			name:      "две собственные сессии",
			workspace: OneManagedWorkspace{ID: workspace},
			sessions:  twoOwnSessionsObservation(t, change, workspace),
			wantErr:   ErrAmbiguousOwnSessions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &fakePhaseOneGateway{
				workspace:   tt.workspace,
				sessions:    tt.sessions,
				workspaceID: workspace,
			}
			reconciler := mustPhaseOneReconciler(t, gateway, &fakeReconcileClock{})

			err := reconciler.Run(context.Background(), change, "/repo")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.wantErr, err)
			}
			if gateway.createdWorkspaces != 0 || gateway.createdSessions != 0 || gateway.archivedSessions != 0 {
				t.Fatalf("неоднозначность привела к воздействию: %v", gateway.events)
			}
		})
	}
}

func TestИзменившеесяОснованиеВоздействияПеречитывается(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:          OneManagedWorkspace{ID: workspace},
		sessions:           NoActiveOwnSession{},
		workspaceID:        workspace,
		sessionAfterCreate: ownSessionObservation(t, change, workspace, "session-1", "running", ""),
		createSessionErr:   ErrReconcileObservationChanged,
	}
	clock := &fakeReconcileClock{err: context.Canceled}
	reconciler := mustPhaseOneReconciler(t, gateway, clock)

	err := reconciler.Run(context.Background(), change, "/repo")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась отмена после перечитывания, получено %v", err)
	}
	if gateway.createdSessions != 1 {
		t.Fatalf("ожидалась одна попытка создания, получено %d", gateway.createdSessions)
	}
	if got := countEvent(gateway.events, "sessions:read"); got != 2 {
		t.Fatalf("после смены основания ожидалось повторное чтение, получено %d", got)
	}
}

type fakePhaseOneGateway struct {
	workspace          ManagedWorkspaceObservation
	sessions           OwnSessionObservation
	workspaceID        WorkspaceID
	sessionAfterCreate OwnSessionObservation
	events             []string
	createdWorkspaces  int
	createdSessions    int
	archivedSessions   int
	createSessionErr   error
}

func (gateway *fakePhaseOneGateway) FindActiveWorkspace(
	context.Context,
	ChangeKey,
	string,
) (ManagedWorkspaceObservation, error) {
	gateway.events = append(gateway.events, "workspace:read")
	return gateway.workspace, nil
}

func (gateway *fakePhaseOneGateway) FindOwnSessions(
	context.Context,
	ChangeKey,
	WorkspaceID,
	string,
) (OwnSessionObservation, error) {
	gateway.events = append(gateway.events, "sessions:read")
	return gateway.sessions, nil
}

func (gateway *fakePhaseOneGateway) CreateWorkspace(context.Context, ChangeKey, string) error {
	gateway.events = append(gateway.events, "workspace:create")
	gateway.createdWorkspaces++
	gateway.workspace = OneManagedWorkspace{ID: gateway.workspaceID}
	return nil
}

func (gateway *fakePhaseOneGateway) CreateOwnSession(context.Context, ChangeKey, WorkspaceID, string) error {
	gateway.events = append(gateway.events, "session:create")
	gateway.createdSessions++
	gateway.sessions = gateway.sessionAfterCreate
	err := gateway.createSessionErr
	gateway.createSessionErr = nil
	return err
}

func (gateway *fakePhaseOneGateway) ArchiveOwnSession(
	context.Context,
	ChangeKey,
	WorkspaceID,
	string,
	ManagedSession,
) error {
	gateway.events = append(gateway.events, "session:archive")
	gateway.archivedSessions++
	return nil
}

type fakeReconcileClock struct {
	events *[]string
	wait   func()
	err    error
}

func (clock *fakeReconcileClock) Wait(context.Context, time.Duration) error {
	if clock.events != nil {
		*clock.events = append(*clock.events, "clock:wait")
	}
	if clock.wait != nil {
		clock.wait()
	}
	return clock.err
}

func mustPhaseOneReconciler(t *testing.T, gateway *fakePhaseOneGateway, clock *fakeReconcileClock) *PhaseOneReconciler {
	t.Helper()
	clock.events = &gateway.events
	reconciler, err := NewPhaseOneReconciler(gateway, clock, time.Second)
	if err != nil {
		t.Fatalf("создать ядро сопровождения: %v", err)
	}
	return reconciler
}

func ownSessionObservation(
	t interface {
		Helper()
		Fatalf(string, ...any)
	},
	change ChangeKey,
	workspace WorkspaceID,
	id, status, reason string,
) OwnSessionObservation {
	t.Helper()
	raw := validSession(id, status)
	raw.WorkspaceID = workspace.String()
	raw.Labels[LabelChange] = change.String()
	raw.Labels[LabelWorkspace] = workspace.String()
	if reason != "" {
		raw.RequiresAttention = true
		raw.AttentionReason = reason
	}
	observation, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{raw})
	if err != nil {
		t.Fatalf("построить наблюдение собственной сессии: %v", err)
	}
	return observation
}

func twoOwnSessionsObservation(t *testing.T, change ChangeKey, workspace WorkspaceID) OwnSessionObservation {
	t.Helper()
	first := validSession("session-1", "running")
	second := withAttention(validSession("session-2", "idle"), "finished")
	for _, raw := range []*UntrustedOwnSession{&first, &second} {
		raw.WorkspaceID = workspace.String()
		raw.Labels[LabelChange] = change.String()
		raw.Labels[LabelWorkspace] = workspace.String()
	}
	observation, err := ObserveOwnSessions(change, workspace, []UntrustedOwnSession{first, second})
	if err != nil {
		t.Fatalf("построить неоднозначное наблюдение: %v", err)
	}
	return observation
}

func countEvent(events []string, target string) int {
	count := 0
	for _, event := range events {
		if event == target {
			count++
		}
	}
	return count
}
