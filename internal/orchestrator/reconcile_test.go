package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestЯдроПеречитываетСостояниеМеждуВоздействиями(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:        NoManagedWorkspace{},
		sessions:         NoActiveOwnSession{},
		workspaceID:      workspace,
		createdSessionID: mustReconcileSessionID(t, "session-1"),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "running", ""),
			ownSessionObservation(t, change, workspace, "session-1", "idle", "finished"),
		},
	}
	reconciler, err := NewPhaseOneReconciler(gateway)
	if err != nil {
		t.Fatalf("создать ядро сопровождения: %v", err)
	}

	if err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("сопроводить сессию: %v", err)
	}

	want := []string{
		"workspace:read", "workspace:create",
		"workspace:read", "sessions:read", "session:create",
		"workspace:read", "session:observe:session-1", "session:wait:session-1",
		"workspace:read", "session:observe:session-1", "session:archive",
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
		waitErr:     context.Canceled,
	}

	for attempt := 1; attempt <= 2; attempt++ {
		reconciler, err := NewPhaseOneReconciler(gateway)
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
			reconciler := mustPhaseOneReconciler(t, gateway)

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
			reconciler := mustPhaseOneReconciler(t, gateway)

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
		createdSessionID:   mustReconcileSessionID(t, "session-1"),
		createSessionErr:   ErrReconcileObservationChanged,
		sessionAfterCreate: ownSessionObservation(t, change, workspace, "session-1", "running", ""),
		waitErr:            context.Canceled,
	}
	reconciler := mustPhaseOneReconciler(t, gateway)

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

func TestПослеWaitЯдроСвежоНаблюдаетТотЖеID(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:   OneManagedWorkspace{ID: workspace},
		sessions:    ownSessionObservation(t, change, workspace, "session-known", "running", ""),
		workspaceID: workspace,
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-known", "closed", ""),
		},
	}
	reconciler := mustPhaseOneReconciler(t, gateway)

	if err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("сопроводить известную сессию после wait: %v", err)
	}

	want := []string{
		"workspace:read", "sessions:read", "session:wait:session-known",
		"workspace:read", "session:observe:session-known",
	}
	if !reflect.DeepEqual(gateway.events, want) {
		t.Fatalf("ядро не сохранило цель между wait и наблюдением:\nполучено: %v\nожидалось: %v", gateway.events, want)
	}
}

func TestОшибкаWaitНеСчитаетсяЗакрытиемСессии(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	waitErr := errors.New("источник wait недоступен")
	gateway := &fakePhaseOneGateway{
		workspace:   OneManagedWorkspace{ID: workspace},
		sessions:    ownSessionObservation(t, change, workspace, "session-known", "running", ""),
		workspaceID: workspace,
		waitErr:     waitErr,
	}
	reconciler := mustPhaseOneReconciler(t, gateway)

	err := reconciler.Run(context.Background(), change, "/repo")
	if !errors.Is(err, waitErr) {
		t.Fatalf("ожидалась ошибка wait, получено %v", err)
	}
	if got := countEventPrefix(gateway.events, "session:observe:"); got != 0 {
		t.Fatalf("ошибка wait была принята за событие закрытия: %v", gateway.events)
	}
}

func TestЯдроНеПереключаетсяНаДругуюСессиюПослеWait(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:   OneManagedWorkspace{ID: workspace},
		sessions:    ownSessionObservation(t, change, workspace, "session-known", "running", ""),
		workspaceID: workspace,
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-other", "running", ""),
		},
	}
	reconciler := mustPhaseOneReconciler(t, gateway)

	err := reconciler.Run(context.Background(), change, "/repo")
	if !errors.Is(err, ErrUnexpectedObservation) {
		t.Fatalf("ожидалась ошибка смены известной сессии, получено %v", err)
	}
	if countEventPrefix(gateway.events, "session:wait:") != 1 {
		t.Fatalf("ядро переключилось на другую цель: %v", gateway.events)
	}
}

func TestПовторПослеИзмененияОснованияArchiveСохраняетИзвестныйID(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakePhaseOneGateway{
		workspace:   OneManagedWorkspace{ID: workspace},
		sessions:    ownSessionObservation(t, change, workspace, "session-known", "idle", "finished"),
		workspaceID: workspace,
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-known", "closed", ""),
		},
		archiveSessionErr: ErrReconcileObservationChanged,
	}
	reconciler := mustPhaseOneReconciler(t, gateway)

	if err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("повторить наблюдение после изменения основания archive: %v", err)
	}

	want := []string{
		"workspace:read", "sessions:read", "session:archive",
		"workspace:read", "session:observe:session-known",
	}
	if !reflect.DeepEqual(gateway.events, want) {
		t.Fatalf("повтор потерял известный ID:\nполучено: %v\nожидалось: %v", gateway.events, want)
	}
}

func TestИзвестнаяСессияНеРазрешаетСоздатьИсчезнувшийWorkspace(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	known := mustReconcileSessionID(t, "session-known")
	gateway := &fakePhaseOneGateway{workspace: NoManagedWorkspace{}}
	reconciler := mustPhaseOneReconciler(t, gateway)

	action, err := reconciler.nextAction(context.Background(), change, "/repo", &known)
	if err != nil {
		t.Fatalf("выбрать действие: %v", err)
	}
	stopped, ok := action.(stopPhaseOneAction)
	if !ok || !errors.Is(stopped.err, ErrUnexpectedObservation) {
		t.Fatalf("исчезнувший workspace разрешил действие %T: %#v", action, action)
	}
}

type fakePhaseOneGateway struct {
	workspace          ManagedWorkspaceObservation
	sessions           OwnSessionObservation
	workspaceID        WorkspaceID
	createdSessionID   SessionID
	sessionAfterCreate OwnSessionObservation
	knownObservations  []OwnSessionObservation
	events             []string
	createdWorkspaces  int
	createdSessions    int
	archivedSessions   int
	createSessionErr   error
	waitErr            error
	archiveSessionErr  error
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

func (gateway *fakePhaseOneGateway) ObserveOwnSession(
	_ context.Context,
	_ ChangeKey,
	_ WorkspaceID,
	_ string,
	known SessionID,
) (OwnSessionObservation, error) {
	gateway.events = append(gateway.events, "session:observe:"+known.String())
	if len(gateway.knownObservations) == 0 {
		return gateway.sessions, nil
	}
	observation := gateway.knownObservations[0]
	gateway.knownObservations = gateway.knownObservations[1:]
	return observation, nil
}

func (gateway *fakePhaseOneGateway) CreateWorkspace(context.Context, ChangeKey, string) error {
	gateway.events = append(gateway.events, "workspace:create")
	gateway.createdWorkspaces++
	gateway.workspace = OneManagedWorkspace{ID: gateway.workspaceID}
	return nil
}

func (gateway *fakePhaseOneGateway) CreateOwnSession(
	context.Context,
	ChangeKey,
	WorkspaceID,
	string,
) (SessionID, error) {
	gateway.events = append(gateway.events, "session:create")
	gateway.createdSessions++
	if gateway.sessionAfterCreate != nil {
		gateway.sessions = gateway.sessionAfterCreate
	}
	err := gateway.createSessionErr
	gateway.createSessionErr = nil
	return gateway.createdSessionID, err
}

func (gateway *fakePhaseOneGateway) WaitOwnSession(_ context.Context, session SessionID) error {
	gateway.events = append(gateway.events, "session:wait:"+session.String())
	return gateway.waitErr
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
	err := gateway.archiveSessionErr
	gateway.archiveSessionErr = nil
	return err
}

func mustPhaseOneReconciler(t *testing.T, gateway *fakePhaseOneGateway) *PhaseOneReconciler {
	t.Helper()
	reconciler, err := NewPhaseOneReconciler(gateway)
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

func mustReconcileSessionID(t *testing.T, value string) SessionID {
	t.Helper()
	id, err := NewSessionID(value)
	if err != nil {
		t.Fatalf("создать ID сессии: %v", err)
	}
	return id
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

func countEventPrefix(events []string, prefix string) int {
	count := 0
	for _, event := range events {
		if len(event) >= len(prefix) && event[:len(prefix)] == prefix {
			count++
		}
	}
	return count
}
