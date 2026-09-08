package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestСуществующаяСессияИмеетПриоритетПередGitИВходамиСоздания(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions:  ownSessionObservation(t, change, workspace, "session-1", "running", ""),
		gitStates: []WorkingTreeObservation{DirtyWorkingTree{}},
		waitErr:   context.Canceled,
	}
	reconciler := mustCommitPreparationReconciler(t, gateway)

	_, err := reconciler.Run(context.Background(), change, "/repo")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась отмена блокирующего ожидания, получено %v", err)
	}

	want := []string{
		"change:read", "workspace:read", "sessions:read", "session:wait:session-1",
	}
	if !reflect.DeepEqual(gateway.events, want) {
		t.Fatalf("сессия не получила приоритет перед Git и входами создания:\nполучено: %v\nожидалось: %v", gateway.events, want)
	}
}

func TestПослеWaitЦиклПолучаетНовоеАвторитетноеНаблюдение(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions:  ownSessionObservation(t, change, workspace, "session-1", "running", ""),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "idle", "finished"),
		},
		gitStates: []WorkingTreeObservation{CleanWorkingTree{}},
	}
	reconciler := mustCommitPreparationReconciler(t, gateway)

	outcome, err := reconciler.Run(context.Background(), change, "/repo")
	if err != nil {
		t.Fatalf("сопроводить поручение: %v", err)
	}
	completed, ok := outcome.(CommitPreparationCompleted)
	if !ok || completed.SessionID().String() != "session-1" {
		t.Fatalf("ожидалось завершённое поручение session-1, получено %#v", outcome)
	}

	want := []string{
		"change:read", "workspace:read", "sessions:read", "session:wait:session-1",
		"change:read", "workspace:read", "session:observe:session-1", "git:read", "session:archive:session-1",
	}
	if !reflect.DeepEqual(gateway.events, want) {
		t.Fatalf("после wait не выполнено свежее наблюдение:\nполучено: %v\nожидалось: %v", gateway.events, want)
	}
}

func TestОтсутствующаяСессияВыбираетДействиеПоСвежемуGit(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	t.Run("чистое дерево завершает команду без поручения", func(t *testing.T) {
		gateway := &fakeCommitPreparationGateway{
			workspace: OneManagedWorkspace{ID: workspace},
			sessions:  NoActiveOwnSession{},
			gitStates: []WorkingTreeObservation{CleanWorkingTree{}},
		}
		reconciler := mustCommitPreparationReconciler(t, gateway)

		outcome, err := reconciler.Run(context.Background(), change, "/repo")
		if err != nil {
			t.Fatalf("завершить без поручения: %v", err)
		}
		if _, ok := outcome.(NoCommitPreparationNeeded); !ok {
			t.Fatalf("ожидалось отсутствие работы, получено %T", outcome)
		}
		if gateway.preparedSessions != 0 || gateway.createdWorkspaces != 0 || gateway.createdSessions != 0 {
			t.Fatalf("чистое дерево вызвало подготовку или мутацию: %v", gateway.events)
		}
	})

	t.Run("грязное дерево создаёт ровно одно подготовленное поручение", func(t *testing.T) {
		gateway := &fakeCommitPreparationGateway{
			workspace:        OneManagedWorkspace{ID: workspace},
			sessions:         NoActiveOwnSession{},
			gitStates:        []WorkingTreeObservation{DirtyWorkingTree{}, DirtyWorkingTree{}},
			createdSessionID: mustReconcileSessionID(t, "session-created"),
			waitErr:          context.Canceled,
		}
		gateway.sessionAfterCreate = ownSessionObservation(
			t, change, workspace, "session-created", "running", "",
		)
		reconciler := mustCommitPreparationReconciler(t, gateway)

		_, err := reconciler.Run(context.Background(), change, "/repo")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена после создания поручения, получено %v", err)
		}
		if gateway.preparedSessions != 1 || gateway.createdSessions != 1 || gateway.createdWorkspaces != 0 {
			t.Fatalf("ожидалось одно подготовленное поручение без создания workspace: prepare=%d create=%d workspace=%d",
				gateway.preparedSessions, gateway.createdSessions, gateway.createdWorkspaces)
		}
		prepareIndex := indexEvent(gateway.events, "session:prepare")
		createIndex := indexEvent(gateway.events, "session:create")
		if prepareIndex < 0 || createIndex <= prepareIndex {
			t.Fatalf("сессия создана без предварительно подготовленных входов: %v", gateway.events)
		}
	})

	t.Run("грязное дерево готовит входы до создания отсутствующего workspace", func(t *testing.T) {
		gateway := &fakeCommitPreparationGateway{
			workspace:        NoManagedWorkspace{},
			workspaceID:      workspace,
			sessions:         NoActiveOwnSession{},
			gitStates:        []WorkingTreeObservation{DirtyWorkingTree{}, DirtyWorkingTree{}, DirtyWorkingTree{}},
			createdSessionID: mustReconcileSessionID(t, "session-created"),
			waitErr:          context.Canceled,
		}
		gateway.sessionAfterCreate = ownSessionObservation(
			t, change, workspace, "session-created", "running", "",
		)
		reconciler := mustCommitPreparationReconciler(t, gateway)

		_, err := reconciler.Run(context.Background(), change, "/repo")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена после создания поручения, получено %v", err)
		}
		if gateway.preparedSessions != 1 || gateway.createdWorkspaces != 1 || gateway.createdSessions != 1 {
			t.Fatalf("неожиданное число подготовок и мутаций: prepare=%d workspace=%d create=%d",
				gateway.preparedSessions, gateway.createdWorkspaces, gateway.createdSessions)
		}
		prepareIndex := indexEvent(gateway.events, "session:prepare")
		workspaceIndex := indexEvent(gateway.events, "workspace:create")
		if prepareIndex < 0 || workspaceIndex <= prepareIndex {
			t.Fatalf("workspace создан до проверки входов нового поручения: %v", gateway.events)
		}
	})
}

func TestОшибкаПодготовкиНовойСессииУсловнаДляГрязногоGit(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	configErr := errors.New("конфигурация новой сессии недоступна")

	t.Run("существующая сессия не читает конфигурацию", func(t *testing.T) {
		gateway := &fakeCommitPreparationGateway{
			workspace:  OneManagedWorkspace{ID: workspace},
			sessions:   ownSessionObservation(t, change, workspace, "session-1", "running", ""),
			prepareErr: configErr,
			waitErr:    context.Canceled,
		}
		reconciler := mustCommitPreparationReconciler(t, gateway)

		_, err := reconciler.Run(context.Background(), change, "/repo")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("конфигурация помешала восстановлению сессии: %v", err)
		}
		if gateway.preparedSessions != 0 {
			t.Fatalf("существующая сессия вызвала чтение конфигурации: %v", gateway.events)
		}
	})

	t.Run("чистое дерево не читает конфигурацию", func(t *testing.T) {
		gateway := &fakeCommitPreparationGateway{
			workspace:  OneManagedWorkspace{ID: workspace},
			sessions:   NoActiveOwnSession{},
			gitStates:  []WorkingTreeObservation{CleanWorkingTree{}},
			prepareErr: configErr,
		}
		reconciler := mustCommitPreparationReconciler(t, gateway)

		if _, err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
			t.Fatalf("конфигурация помешала выходу при чистом Git: %v", err)
		}
		if gateway.preparedSessions != 0 {
			t.Fatalf("чистый Git вызвал чтение конфигурации: %v", gateway.events)
		}
	})

	t.Run("грязное дерево возвращает ошибку конфигурации до мутаций", func(t *testing.T) {
		gateway := &fakeCommitPreparationGateway{
			workspace:  OneManagedWorkspace{ID: workspace},
			sessions:   NoActiveOwnSession{},
			gitStates:  []WorkingTreeObservation{DirtyWorkingTree{}},
			prepareErr: configErr,
		}
		reconciler := mustCommitPreparationReconciler(t, gateway)

		_, err := reconciler.Run(context.Background(), change, "/repo")
		if !errors.Is(err, configErr) {
			t.Fatalf("ожидалась условная ошибка конфигурации, получено %v", err)
		}
		if gateway.createdWorkspaces != 0 || gateway.createdSessions != 0 {
			t.Fatalf("ошибка входов привела к мутации: %v", gateway.events)
		}
	})
}

func TestЗавершившийсяХодВыбираетArchiveИлиПотребностьВЧеловекеПоGit(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")

	tests := []struct {
		name        string
		git         WorkingTreeObservation
		wantHuman   bool
		wantArchive int
	}{
		{name: "чистый Git архивирует сессию", git: CleanWorkingTree{}, wantArchive: 1},
		{name: "грязный Git требует человека", git: DirtyWorkingTree{}, wantHuman: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &fakeCommitPreparationGateway{
				workspace: OneManagedWorkspace{ID: workspace},
				sessions: ownSessionObservation(
					t, change, workspace, "session-1", "idle", "finished",
				),
				gitStates: []WorkingTreeObservation{tt.git},
			}
			reconciler := mustCommitPreparationReconciler(t, gateway)

			outcome, err := reconciler.Run(context.Background(), change, "/repo")
			if err != nil {
				t.Fatalf("выбрать исход завершившегося хода: %v", err)
			}
			if tt.wantHuman {
				need, ok := outcome.(HumanInterventionRequired)
				if !ok || need.SessionID().String() != "session-1" || need.Reason() != SessionTurnFinished {
					t.Fatalf("ожидалась типизированная потребность в человеке, получено %#v", outcome)
				}
			} else if _, ok := outcome.(CommitPreparationCompleted); !ok {
				t.Fatalf("ожидалось завершённое поручение, получено %T", outcome)
			}
			if gateway.archivedSessions != tt.wantArchive {
				t.Fatalf("неожиданное число архивирований: %d", gateway.archivedSessions)
			}
		})
	}
}

func TestОшибкаАгентаИЗапросРазрешенияТребуютЧеловекаБезНовойСессии(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	tests := []struct {
		name   string
		status string
		reason string
		want   SessionAttentionReason
	}{
		{name: "ошибка агента", status: "error", reason: "error", want: SessionAgentError},
		{name: "запрос разрешения", status: "idle", reason: "permission", want: SessionPermissionCompatibilityViolation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &fakeCommitPreparationGateway{
				workspace: OneManagedWorkspace{ID: workspace},
				sessions:  ownSessionObservation(t, change, workspace, "session-1", tt.status, tt.reason),
			}
			reconciler := mustCommitPreparationReconciler(t, gateway)

			outcome, err := reconciler.Run(context.Background(), change, "/repo")
			if err != nil {
				t.Fatalf("вернуть потребность в человеке: %v", err)
			}
			need, ok := outcome.(HumanInterventionRequired)
			if !ok || need.Reason() != tt.want || need.SessionID().String() != "session-1" {
				t.Fatalf("неожиданная потребность в человеке: %#v", outcome)
			}
			if gateway.preparedSessions != 0 || gateway.createdSessions != 0 || gateway.archivedSessions != 0 {
				t.Fatalf("потребность в человеке вызвала воздействие: %v", gateway.events)
			}
		})
	}
}

func TestПодтверждённоеЗакрытиеПеречитываетGitИНеСоздаётЗамену(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	tests := []struct {
		name   string
		git    WorkingTreeObservation
		assert func(*testing.T, CommitPreparationOutcome)
	}{
		{
			name: "чистый Git подтверждает завершение",
			git:  CleanWorkingTree{},
			assert: func(t *testing.T, outcome CommitPreparationOutcome) {
				if _, ok := outcome.(CommitPreparationCompleted); !ok {
					t.Fatalf("ожидалось завершённое поручение, получено %T", outcome)
				}
			},
		},
		{
			name: "грязный Git завершает попытку без замены",
			git:  DirtyWorkingTree{},
			assert: func(t *testing.T, outcome CommitPreparationOutcome) {
				closed, ok := outcome.(ClosedSessionWithChanges)
				if !ok || closed.SessionID().String() != "session-1" {
					t.Fatalf("ожидалось закрытие с изменениями, получено %#v", outcome)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &fakeCommitPreparationGateway{
				workspace: OneManagedWorkspace{ID: workspace},
				sessions:  ownSessionObservation(t, change, workspace, "session-1", "running", ""),
				knownObservations: []OwnSessionObservation{
					ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
				},
				gitStates: []WorkingTreeObservation{tt.git},
			}
			reconciler := mustCommitPreparationReconciler(t, gateway)

			outcome, err := reconciler.Run(context.Background(), change, "/repo")
			if err != nil {
				t.Fatalf("обработать закрытие: %v", err)
			}
			tt.assert(t, outcome)
			if countEvent(gateway.events, "git:read") != 1 {
				t.Fatalf("закрытие не вызвало свежую проверку Git: %v", gateway.events)
			}
			if gateway.preparedSessions != 0 || gateway.createdSessions != 0 || gateway.archivedSessions != 0 {
				t.Fatalf("закрытие вызвало замену или архивирование: %v", gateway.events)
			}
		})
	}
}

func TestИзменившеесяОснованиеПередВоздействиемЗапускаетПолноеПеречитывание(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace:        OneManagedWorkspace{ID: workspace},
		sessions:         NoActiveOwnSession{},
		gitStates:        []WorkingTreeObservation{DirtyWorkingTree{}, DirtyWorkingTree{}, CleanWorkingTree{}},
		createdSessionID: mustReconcileSessionID(t, "session-created"),
		createSessionErr: ErrReconcileObservationChanged,
	}
	reconciler := mustCommitPreparationReconciler(t, gateway)

	outcome, err := reconciler.Run(context.Background(), change, "/repo")
	if err != nil {
		t.Fatalf("перечитать изменившееся основание: %v", err)
	}
	if _, ok := outcome.(NoCommitPreparationNeeded); !ok {
		t.Fatalf("ожидалось отсутствие работы после перечитывания, получено %T", outcome)
	}
	if gateway.createdSessions != 1 || countEvent(gateway.events, "change:read") != 3 ||
		countEvent(gateway.events, "workspace:read") != 3 || countEvent(gateway.events, "git:read") != 3 {
		t.Fatalf("смена основания не вызвала полное перечитывание: %v", gateway.events)
	}
}

type fakeCommitPreparationGateway struct {
	workspace          ManagedWorkspaceObservation
	workspaceID        WorkspaceID
	sessions           OwnSessionObservation
	knownObservations  []OwnSessionObservation
	gitStates          []WorkingTreeObservation
	createdSessionID   SessionID
	sessionAfterCreate OwnSessionObservation
	events             []string
	preparedSessions   int
	createdWorkspaces  int
	createdSessions    int
	archivedSessions   int
	changeErr          error
	gitErr             error
	prepareErr         error
	createWorkspaceErr error
	createSessionErr   error
	waitErr            error
	archiveErr         error
}

func (gateway *fakeCommitPreparationGateway) RefreshActiveChange(context.Context, ChangeKey, string) error {
	gateway.events = append(gateway.events, "change:read")
	return gateway.changeErr
}

func (gateway *fakeCommitPreparationGateway) FindActiveWorkspace(
	context.Context,
	ChangeKey,
	string,
) (ManagedWorkspaceObservation, error) {
	gateway.events = append(gateway.events, "workspace:read")
	return gateway.workspace, nil
}

func (gateway *fakeCommitPreparationGateway) FindOwnSessions(
	context.Context,
	ChangeKey,
	WorkspaceID,
	string,
) (OwnSessionObservation, error) {
	gateway.events = append(gateway.events, "sessions:read")
	return gateway.sessions, nil
}

func (gateway *fakeCommitPreparationGateway) ObserveOwnSession(
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

func (gateway *fakeCommitPreparationGateway) ReadWorkingTree(context.Context, string) (WorkingTreeObservation, error) {
	gateway.events = append(gateway.events, "git:read")
	if gateway.gitErr != nil {
		return nil, gateway.gitErr
	}
	if len(gateway.gitStates) == 0 {
		return nil, errors.New("тест не задал состояние Git")
	}
	state := gateway.gitStates[0]
	if len(gateway.gitStates) > 1 {
		gateway.gitStates = gateway.gitStates[1:]
	}
	return state, nil
}

func (gateway *fakeCommitPreparationGateway) PrepareNewSession(
	context.Context,
	ChangeKey,
	string,
) (PreparedSessionCreation, error) {
	gateway.events = append(gateway.events, "session:prepare")
	gateway.preparedSessions++
	if gateway.prepareErr != nil {
		return PreparedSessionCreation{}, gateway.prepareErr
	}
	return NewPreparedSessionCreation(gateway.createSession), nil
}

func (gateway *fakeCommitPreparationGateway) CreateWorkspace(context.Context, ChangeKey, string) error {
	gateway.events = append(gateway.events, "workspace:create")
	gateway.createdWorkspaces++
	if gateway.createWorkspaceErr != nil {
		return gateway.createWorkspaceErr
	}
	gateway.workspace = OneManagedWorkspace{ID: gateway.workspaceID}
	return nil
}

func (gateway *fakeCommitPreparationGateway) createSession(
	context.Context,
	ChangeKey,
	WorkspaceID,
	string,
) (SessionID, error) {
	gateway.events = append(gateway.events, "session:create")
	gateway.createdSessions++
	if gateway.createSessionErr != nil {
		err := gateway.createSessionErr
		gateway.createSessionErr = nil
		return SessionID{}, err
	}
	if gateway.sessionAfterCreate != nil {
		gateway.sessions = gateway.sessionAfterCreate
	}
	return gateway.createdSessionID, nil
}

func (gateway *fakeCommitPreparationGateway) WaitOwnSession(_ context.Context, session SessionID) error {
	gateway.events = append(gateway.events, "session:wait:"+session.String())
	return gateway.waitErr
}

func (gateway *fakeCommitPreparationGateway) ArchiveOwnSession(
	_ context.Context,
	_ ChangeKey,
	_ WorkspaceID,
	_ string,
	session ManagedSession,
) error {
	gateway.events = append(gateway.events, "session:archive:"+session.ID().String())
	gateway.archivedSessions++
	return gateway.archiveErr
}

func mustCommitPreparationReconciler(
	t *testing.T,
	gateway *fakeCommitPreparationGateway,
) *CommitPreparationReconciler {
	t.Helper()
	reconciler, err := NewCommitPreparationReconciler(gateway)
	if err != nil {
		t.Fatalf("создать цикл подготовки коммитов: %v", err)
	}
	return reconciler
}

func indexEvent(events []string, target string) int {
	for index, event := range events {
		if event == target {
			return index
		}
	}
	return -1
}
