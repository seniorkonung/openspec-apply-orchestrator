package paseo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

func TestGatewayПреобразуетНаблюдениеАктивныхWorkspaceДляЯдра(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	cwd := t.TempDir()

	tests := []struct {
		name       string
		workspaces []map[string]any
		assert     func(*testing.T, orchestrator.ManagedWorkspaceObservation)
	}{
		{
			name:       "workspace отсутствует",
			workspaces: []map[string]any{},
			assert: func(t *testing.T, got orchestrator.ManagedWorkspaceObservation) {
				if _, ok := got.(orchestrator.NoManagedWorkspace); !ok {
					t.Fatalf("ожидалось отсутствие workspace, получено %T", got)
				}
			},
		},
		{
			name: "найден один workspace",
			workspaces: []map[string]any{
				workspaceListItem("workspace-1", managedWorkspaceName(change), cwd),
			},
			assert: func(t *testing.T, got orchestrator.ManagedWorkspaceObservation) {
				one, ok := got.(orchestrator.OneManagedWorkspace)
				if !ok || one.ID.String() != "workspace-1" {
					t.Fatalf("ожидался workspace-1, получено %#v", got)
				}
			},
		},
		{
			name: "найдены два workspace",
			workspaces: []map[string]any{
				workspaceListItem("workspace-1", managedWorkspaceName(change), cwd),
				workspaceListItem("workspace-2", managedWorkspaceName(change), cwd),
			},
			assert: func(t *testing.T, got orchestrator.ManagedWorkspaceObservation) {
				many, ok := got.(orchestrator.AmbiguousManagedWorkspaces)
				if !ok || len(many.IDs) != 2 {
					t.Fatalf("ожидалась неоднозначность workspace, получено %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := newTestReconcileGateway(t)
			t.Setenv("FAKE_PASEO_WORKSPACES", encodeDirectoryJSON(t, tt.workspaces))
			observation, err := gateway.FindActiveWorkspace(context.Background(), change, cwd)
			if err != nil {
				t.Fatalf("прочитать workspace: %v", err)
			}
			tt.assert(t, observation)
		})
	}
}

func TestДваНовыхЯдраПродолжаютОднуВидимуюСессиюЧерезCLI(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	setOneWorkspace(t, change, workspace, cwd)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-visible", "агент", "running", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-visible", "running", cwd)))

	for attempt := 1; attempt <= 2; attempt++ {
		reconciler, err := orchestrator.NewPhaseOneReconciler(
			phaseOneTestGateway{ReconcileGateway: gateway},
		)
		if err != nil {
			t.Fatalf("создать ядро %d: %v", attempt, err)
		}
		err = reconciler.Run(context.Background(), change, cwd)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена ядра %d, получено %v", attempt, err)
		}
	}

	recorded := readRecordedCalls(t, recordPath)
	if strings.Contains(recorded, "run\n") || strings.Contains(recorded, "archive\n") || strings.Contains(recorded, "--all") {
		t.Fatalf("видимая сессия получила лишнее воздействие или чтение истории:\n%s", recorded)
	}
	if strings.Count(recorded, "workspace\nls\n--json\n") != 2 ||
		strings.Count(recorded, "ls\n--global\n") != 4 ||
		strings.Count(recorded, "inspect\n") != 2 {
		t.Fatalf("каждое ядро должно заново прочитать workspace, фильтры и inspect:\n%s", recorded)
	}
}

func TestGatewayПослеWaitСвежоНаблюдаетИзвестнуюСессию(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	session := mustSessionID(t, "agent-known")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_WAIT", marshalWaitJSON(t, session.String(), "idle", "не использовать как состояние"))
	setOneWorkspace(t, change, workspace, cwd)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem(session.String(), "подготовка", "idle", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection(session.String(), "idle", cwd)))

	if err := gateway.WaitOwnSession(context.Background(), session); err != nil {
		t.Fatalf("дождаться события известной сессии: %v", err)
	}
	observation, err := gateway.ObserveOwnSession(context.Background(), change, workspace, cwd, session)
	if err != nil {
		t.Fatalf("заново наблюдать известную сессию: %v", err)
	}
	waiting, ok := observation.(orchestrator.OwnSessionAwaitingAction)
	if !ok || waiting.Session.ID() != session {
		t.Fatalf("ожидалось свежее наблюдение той же сессии, получено %#v", observation)
	}

	recorded := readRecordedCalls(t, recordPath)
	waitIndex := strings.Index(recorded, "wait\nagent-known\n--json\n")
	filterIndex := strings.Index(recorded, "ls\n--global\n")
	inspectIndex := strings.Index(recorded, "inspect\nagent-known\n--json\n")
	if waitIndex < 0 || filterIndex <= waitIndex || inspectIndex <= filterIndex {
		t.Fatalf("после wait не выполнены свежие фильтры и inspect той же цели:\n%s", recorded)
	}
}

func TestСозданиеWorkspaceПовторноПроверяетЕгоОтсутствие(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_WORKSPACES", "[]")
	t.Setenv("FAKE_PASEO_WORKSPACE_CREATE", encodeDirectoryJSON(t,
		workspaceListItem("workspace-created", managedWorkspaceName(change), cwd),
	))

	if err := gateway.CreateWorkspace(context.Background(), change, cwd); err != nil {
		t.Fatalf("создать workspace: %v", err)
	}

	recorded := readRecordedCalls(t, recordPath)
	want := strings.Join([]string{
		"workspace", "ls", "--json",
		"workspace", "create", "--isolation", "local", "--path", cwd,
		"--title", managedWorkspaceName(change), "--json",
	}, "\n") + "\n"
	if recorded != want {
		t.Fatalf("неожиданная последовательность создания workspace:\n%s", recorded)
	}
}

func TestСозданиеСессииПовторноПроверяетWorkspaceИСобственныеСессии(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_WORKSPACES", encodeDirectoryJSON(t, []map[string]any{
		workspaceListItem(workspace.String(), managedWorkspaceName(change), cwd),
	}))
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")
	t.Setenv("FAKE_PASEO_RUN", encodeDirectoryJSON(t, map[string]any{
		"agentId": "agent-created", "status": "running", "provider": "codex",
		"cwd": cwd, "title": "проверка",
	}))

	if _, err := gateway.CreateOwnSession(
		context.Background(), change, workspace, cwd,
		verifiedTestSessionSettings("high", true), prompts.CommitPreparation(),
	); err != nil {
		t.Fatalf("создать сессию: %v", err)
	}

	recorded := readRecordedCalls(t, recordPath)
	assertReadBeforeMutation(t, recorded, "run\n")
	if strings.Count(recorded, "run\n") != 1 {
		t.Fatalf("ожидался один run:\n%s", recorded)
	}
}

func TestИзменившеесяСостояниеПередRunОтменяетМутацию(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	setOneWorkspace(t, change, workspace, cwd)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-visible", "агент", "running", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-visible", "running", cwd)))

	_, err := gateway.CreateOwnSession(
		context.Background(), change, workspace, cwd,
		verifiedTestSessionSettings("high", true), prompts.CommitPreparation(),
	)
	if !errors.Is(err, orchestrator.ErrReconcileObservationChanged) {
		t.Fatalf("ожидалось изменение основания run, получено %v", err)
	}
	recorded := readRecordedCalls(t, recordPath)
	if strings.Contains(recorded, "run\n") {
		t.Fatalf("после смены состояния выполнен run:\n%s", recorded)
	}
	assertWorkspaceAndSessionReads(t, recorded)
}

func TestЗапросРазрешенияПослеСозданияПолногоДоступаСохраняетТуЖеСессию(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	setOneWorkspace(t, change, workspace, cwd)
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")
	t.Setenv("FAKE_PASEO_RUN", encodeDirectoryJSON(t, map[string]any{
		"agentId": "agent-created", "status": "running", "provider": "codex",
		"cwd": cwd, "title": "подготовка",
	}))

	if _, err := gateway.CreateOwnSession(
		context.Background(), change, workspace, cwd,
		verifiedTestSessionSettings("high", true), prompts.CommitPreparation(),
	); err != nil {
		t.Fatalf("создать сессию полного доступа: %v", err)
	}

	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-created", "подготовка", "running", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	inspection := agentInspection("agent-created", "running", cwd)
	inspection["Mode"] = "full-access"
	inspection["PendingPermissions"] = []map[string]any{{"id": "permission-1", "tool": "Bash"}}
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, inspection))

	observation, err := gateway.FindOwnSessions(context.Background(), change, workspace, cwd)
	if err != nil {
		t.Fatalf("наблюдать созданную сессию: %v", err)
	}
	waiting, ok := observation.(orchestrator.OwnSessionAwaitingAction)
	if !ok || waiting.Session.ID().String() != "agent-created" ||
		waiting.Reason != orchestrator.SessionPermissionRequested {
		t.Fatalf("запрос разрешения не передан человеку в той же сессии: %#v", observation)
	}
	recorded := readRecordedCalls(t, recordPath)
	if strings.Count(recorded, "run\n") != 1 || strings.Contains(recorded, "archive\n") {
		t.Fatalf("запрос разрешения вызвал замену или архивирование сессии:\n%s", recorded)
	}
}

func TestИзменившеесяСостояниеПередArchiveОтменяетМутацию(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	setOneWorkspace(t, change, workspace, cwd)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-visible", "агент", "running", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-visible", "running", cwd)))
	session := mustMutationManagedSession(t, "agent-visible", change, workspace)

	err := gateway.ArchiveOwnSession(context.Background(), change, workspace, cwd, session)
	if !errors.Is(err, orchestrator.ErrReconcileObservationChanged) {
		t.Fatalf("ожидалось изменение основания archive, получено %v", err)
	}
	recorded := readRecordedCalls(t, recordPath)
	if strings.Contains(recorded, "archive\n") {
		t.Fatalf("после смены состояния выполнен archive:\n%s", recorded)
	}
	assertWorkspaceAndSessionReads(t, recorded)
}

func TestЗавершённыйХодАрхивируетсяПослеСвежихЧтенийИПодтверждения(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	archiveState := filepath.Join(t.TempDir(), "архивировано")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_ARCHIVE_STATE", archiveState)
	setOneWorkspace(t, change, workspace, cwd)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-visible", "агент", "idle", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-visible", "idle", cwd)))
	t.Setenv("FAKE_PASEO_ARCHIVE", encodeDirectoryJSON(t, map[string]any{
		"agentId": "agent-visible", "status": "archived", "archivedAt": "2026-09-07T09:20:00Z",
	}))
	archived := agentInspection("agent-visible", "idle", cwd)
	archived["Archived"] = true
	archived["ArchivedAt"] = "2026-09-07T09:20:00Z"
	t.Setenv("FAKE_PASEO_INSPECT_AFTER_ARCHIVE", encodeDirectoryJSON(t, archived))
	session := mustMutationManagedSession(t, "agent-visible", change, workspace)

	if err := gateway.ArchiveOwnSession(context.Background(), change, workspace, cwd, session); err != nil {
		t.Fatalf("архивировать завершённую сессию: %v", err)
	}

	recorded := readRecordedCalls(t, recordPath)
	assertWorkspaceAndSessionReads(t, recorded)
	if strings.Count(recorded, "inspect\n") != 3 || strings.Count(recorded, "archive\n") != 1 {
		t.Fatalf("ожидались три inspect и один archive:\n%s", recorded)
	}
	if strings.Contains(recorded, "--force") {
		t.Fatalf("gateway использовал принудительное архивирование:\n%s", recorded)
	}
}

func TestИсчезнувшаяИзАктивныхФильтровЦелеваяСессияПодтверждаетсяПоInspect(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	setOneWorkspace(t, change, workspace, cwd)
	t.Setenv("FAKE_PASEO_LS_BROAD", "[]")
	t.Setenv("FAKE_PASEO_LS_EXACT", "[]")
	archived := agentInspection("agent-visible", "idle", cwd)
	archived["Archived"] = true
	archived["ArchivedAt"] = "2026-09-07T09:20:00Z"
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, archived))
	session := mustMutationManagedSession(t, "agent-visible", change, workspace)

	if err := gateway.ArchiveOwnSession(context.Background(), change, workspace, cwd, session); err != nil {
		t.Fatalf("подтвердить закрытие известной сессии: %v", err)
	}

	recorded := readRecordedCalls(t, recordPath)
	assertWorkspaceAndSessionReads(t, recorded)
	if strings.Count(recorded, "inspect\n") != 1 || strings.Contains(recorded, "archive\n") {
		t.Fatalf("ожидался один подтверждающий inspect без archive:\n%s", recorded)
	}
}

func TestОшибкаСвежегоЧтенияПередМутациейВозвращаетПрепятствиеPaseo(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	session := mustMutationManagedSession(t, "agent-visible", change, workspace)

	tests := []struct {
		name   string
		invoke func(*ReconcileGateway) error
	}{
		{
			name: "создание workspace",
			invoke: func(gateway *ReconcileGateway) error {
				return gateway.CreateWorkspace(context.Background(), change, cwd)
			},
		},
		{
			name: "создание собственной сессии",
			invoke: func(gateway *ReconcileGateway) error {
				_, err := gateway.CreateOwnSession(
					context.Background(), change, workspace, cwd,
					verifiedTestSessionSettings("high", true), prompts.CommitPreparation(),
				)
				return err
			},
		},
		{
			name: "архивирование собственной сессии",
			invoke: func(gateway *ReconcileGateway) error {
				return gateway.ArchiveOwnSession(
					context.Background(), change, workspace, cwd, session,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := newTestReconcileGateway(t)
			recordPath := filepath.Join(t.TempDir(), "вызовы")
			t.Setenv("FAKE_PASEO_RECORD", recordPath)
			t.Setenv("FAKE_PASEO_EXIT", "9")

			err := tt.invoke(gateway)
			var obstacle *orchestrator.SourceReadObstacle
			if !errors.As(err, &obstacle) {
				t.Fatalf("ожидалось препятствие чтения Paseo, получено %T: %v", err, err)
			}
			if obstacle.Source() != orchestrator.ReadSourcePaseo || !errors.Is(err, ErrCommandExit) {
				t.Fatalf("препятствие потеряло источник или причину: %v", err)
			}
			recorded := readRecordedCalls(t, recordPath)
			if strings.Contains(recorded, "workspace\ncreate\n") ||
				strings.Contains(recorded, "run\n") || strings.Contains(recorded, "archive\n") {
				t.Fatalf("ошибка свежего чтения не остановила мутацию:\n%s", recorded)
			}
		})
	}
}

func TestОшибкаЧтенияПодтвержденияArchiveНеПризнаётУспех(t *testing.T) {
	change := mustDirectoryChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustDirectoryWorkspaceID(t, "workspace-1")
	cwd := t.TempDir()
	gateway := newTestReconcileGateway(t)
	recordPath := filepath.Join(t.TempDir(), "вызовы")
	archiveState := filepath.Join(t.TempDir(), "архивировано")
	t.Setenv("FAKE_PASEO_RECORD", recordPath)
	t.Setenv("FAKE_PASEO_ARCHIVE_STATE", archiveState)
	setOneWorkspace(t, change, workspace, cwd)
	agents := encodeDirectoryJSON(t, []map[string]any{
		agentListItem("agent-visible", "агент", "idle", cwd),
	})
	t.Setenv("FAKE_PASEO_LS_BROAD", agents)
	t.Setenv("FAKE_PASEO_LS_EXACT", agents)
	t.Setenv("FAKE_PASEO_INSPECT", encodeDirectoryJSON(t, agentInspection("agent-visible", "idle", cwd)))
	t.Setenv("FAKE_PASEO_ARCHIVE", encodeDirectoryJSON(t, map[string]any{
		"agentId": "agent-visible", "status": "archived", "archivedAt": "2026-09-07T09:20:00Z",
	}))
	t.Setenv("FAKE_PASEO_INSPECT_AFTER_ARCHIVE", "{")
	session := mustMutationManagedSession(t, "agent-visible", change, workspace)

	err := gateway.ArchiveOwnSession(context.Background(), change, workspace, cwd, session)
	var obstacle *orchestrator.SourceReadObstacle
	if !errors.As(err, &obstacle) || obstacle.Source() != orchestrator.ReadSourcePaseo {
		t.Fatalf("ожидалось препятствие чтения подтверждения Paseo, получено %T: %v", err, err)
	}
	if !errors.Is(err, ErrArchiveNotConfirmed) || !errors.Is(err, ErrTruncatedJSON) {
		t.Fatalf("ошибка подтверждения потеряла контекст: %v", err)
	}
	recorded := readRecordedCalls(t, recordPath)
	if strings.Count(recorded, "archive\n") != 1 {
		t.Fatalf("archive должен выполняться один раз без автоматического повтора:\n%s", recorded)
	}
}

func newTestReconcileGateway(t *testing.T) *ReconcileGateway {
	t.Helper()
	gateway, err := NewReconcileGateway(newFakeClient(t), compatibleTestEnvironment())
	if err != nil {
		t.Fatalf("создать gateway сопровождения: %v", err)
	}
	return gateway
}

type phaseOneTestGateway struct {
	*ReconcileGateway
}

func (gateway phaseOneTestGateway) CreateOwnSession(
	ctx context.Context,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) (orchestrator.SessionID, error) {
	return gateway.ReconcileGateway.CreateOwnSession(
		ctx,
		change,
		workspace,
		cwd,
		verifiedTestSessionSettings("high", true),
		prompts.CommitPreparation(),
	)
}

func (gateway phaseOneTestGateway) WaitOwnSession(context.Context, orchestrator.SessionID) error {
	return context.Canceled
}

func setOneWorkspace(t *testing.T, change orchestrator.ChangeKey, workspace orchestrator.WorkspaceID, cwd string) {
	t.Helper()
	t.Setenv("FAKE_PASEO_WORKSPACES", encodeDirectoryJSON(t, []map[string]any{
		workspaceListItem(workspace.String(), managedWorkspaceName(change), cwd),
	}))
}

func readRecordedCalls(t *testing.T, path string) string {
	t.Helper()
	recorded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("прочитать журнал вызовов: %v", err)
	}
	return string(recorded)
}

func assertReadBeforeMutation(t *testing.T, recorded, mutation string) {
	t.Helper()
	assertWorkspaceAndSessionReads(t, recorded)
	mutationIndex := strings.Index(recorded, mutation)
	inspectIndex := strings.Index(recorded, "inspect\n")
	if mutationIndex < 0 || (inspectIndex >= 0 && inspectIndex > mutationIndex) {
		t.Fatalf("мутация выполнена до свежих чтений:\n%s", recorded)
	}
}

func assertWorkspaceAndSessionReads(t *testing.T, recorded string) {
	t.Helper()
	workspaceIndex := strings.Index(recorded, "workspace\nls\n--json\n")
	broadIndex := strings.Index(recorded, "ls\n--global\n")
	exactIndex := strings.LastIndex(recorded, "ls\n--global\n")
	if workspaceIndex < 0 || broadIndex <= workspaceIndex || exactIndex <= broadIndex {
		t.Fatalf("не найдены последовательные свежие чтения workspace и фильтров:\n%s", recorded)
	}
}
