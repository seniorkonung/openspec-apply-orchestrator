//go:build paseo_integration

package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/testpaseo"
)

const (
	integrationChange       = "integration-process-recovery"
	integrationPrompt       = "Ожидать сигнала стенда и не изменять рабочий каталог."
	integrationEventTimeout = 90 * time.Second
)

func TestСопровождениеПродолжаетсяВНовыхПроцессахИНеТрогаетЧужиеСессии(t *testing.T) {
	harness := testpaseo.Start(t)
	harness.SetBehavior(t, testpaseo.BehaviorWorking)
	request := testpaseo.DriverRequest{
		Operation: testpaseo.DriverReconcile,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	}

	process := harness.StartDriver(t, request)
	first := waitForOwnIntegrationSession(t, harness, integrationChange)
	if err := process.Command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("остановить первый процесс сопровождения: %v", err)
	}
	firstProcessResult := process.Wait(t)
	if firstProcessResult.ErrorKind != testpaseo.DriverCanceled {
		t.Fatalf("первый процесс не сообщил отмену: %#v", firstProcessResult)
	}

	recovered := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverObserve,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if recovered.Observation != testpaseo.ObservationWorking || recovered.SessionID != first.ID().String() {
		t.Fatalf("новый процесс не нашёл работающую сессию: %#v", recovered)
	}

	manualID := runForeignIntegrationSession(t, harness, recovered.WorkspaceID, nil)
	otherID := runForeignIntegrationSession(t, harness, recovered.WorkspaceID, map[string]string{
		orchestrator.LabelOwner:     orchestrator.ManagedOwner,
		orchestrator.LabelVersion:   orchestrator.CurrentOwnershipVersion,
		orchestrator.LabelChange:    "integration-other-change",
		orchestrator.LabelKind:      orchestrator.CommitPreparationKind,
		orchestrator.LabelWorkspace: recovered.WorkspaceID,
	})
	again := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverObserve,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if again.SessionID != first.ID().String() || again.Observation != testpaseo.ObservationWorking {
		t.Fatalf("чужие сессии изменили выбор сопровождения: %#v", again)
	}

	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	waitForTurnFinished(t, harness, integrationChange, first.ID())
	beforeArchive := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverObserve,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if beforeArchive.Observation != testpaseo.ObservationTurnFinished || beforeArchive.SessionID != first.ID().String() {
		t.Fatalf("новый процесс не восстановил ожидание завершившегося хода: %#v", beforeArchive)
	}

	archived := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverReconcile,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if archived.Error != "" || archived.Observation != testpaseo.ObservationNoSession {
		t.Fatalf("новый процесс не подтвердил архивирование: %#v", archived)
	}
	afterArchive := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverObserve,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if afterArchive.Observation != testpaseo.ObservationNoSession {
		t.Fatalf("архивированная сессия участвует в новом выборе: %#v", afterArchive)
	}

	assertIntegrationSessionNotArchived(t, harness, manualID)
	assertIntegrationSessionNotArchived(t, harness, otherID)

	harness.SetBehavior(t, testpaseo.BehaviorPermission)
	startedForPermission := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverStart,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if startedForPermission.Error != "" {
		t.Fatalf("не создать сессию для проверки разрешения: %#v", startedForPermission)
	}
	permissionSession := waitForPermission(t, harness, integrationChange)
	permissionRecovered := harness.RunDriver(t, testpaseo.DriverRequest{
		Operation: testpaseo.DriverObserve,
		Change:    integrationChange,
		Prompt:    integrationPrompt,
	})
	if permissionRecovered.Observation != testpaseo.ObservationPermission ||
		permissionRecovered.SessionID != permissionSession.ID().String() {
		t.Fatalf("новый процесс не восстановил запрос разрешения: %#v", permissionRecovered)
	}
	t.Logf(
		"workspace=%s session=%s permission=%s; остановки: после run, во время работы, при ожидании, до и после archive",
		recovered.WorkspaceID, first.ID(), permissionSession.ID(),
	)
}

func waitForOwnIntegrationSession(
	t *testing.T,
	harness *testpaseo.Harness,
	changeValue string,
) orchestrator.ManagedSession {
	t.Helper()
	change, err := orchestrator.NewChangeKey(changeValue)
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationEventTimeout)
	defer cancel()
	for {
		client, err := paseo.NewClient()
		if err != nil {
			t.Fatalf("создать производственный клиент: %v", err)
		}
		workspaces, err := client.FindActiveWorkspace(ctx, change, harness.Workspace())
		if err == nil {
			if one, ok := workspaces.(paseo.OneActiveWorkspace); ok {
				observation, observeErr := client.FindOwnSessions(ctx, change, one.Workspace.ID(), harness.Workspace())
				if observeErr == nil {
					if working, ok := observation.(orchestrator.WorkingOwnSession); ok {
						return working.Session
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("не дождаться работающей собственной сессии: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForTurnFinished(
	t *testing.T,
	harness *testpaseo.Harness,
	changeValue string,
	expected orchestrator.SessionID,
) {
	t.Helper()
	change, err := orchestrator.NewChangeKey(changeValue)
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationEventTimeout)
	defer cancel()
	client, err := paseo.NewClient()
	if err != nil {
		t.Fatalf("создать производственный клиент: %v", err)
	}
	for {
		workspaces, workspaceErr := client.FindActiveWorkspace(ctx, change, harness.Workspace())
		if workspaceErr == nil {
			if one, ok := workspaces.(paseo.OneActiveWorkspace); ok {
				observation, observeErr := client.FindOwnSessions(ctx, change, one.Workspace.ID(), harness.Workspace())
				if waiting, ok := observation.(orchestrator.OwnSessionAwaitingAction); observeErr == nil && ok && waiting.Reason == orchestrator.SessionTurnFinished &&
					waiting.Session.ID() == expected {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("не дождаться завершения хода сессии %s", expected)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForPermission(
	t *testing.T,
	harness *testpaseo.Harness,
	changeValue string,
) orchestrator.ManagedSession {
	t.Helper()
	change, err := orchestrator.NewChangeKey(changeValue)
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationEventTimeout)
	defer cancel()
	client, err := paseo.NewClient()
	if err != nil {
		t.Fatalf("создать производственный клиент: %v", err)
	}
	for {
		workspaces, workspaceErr := client.FindActiveWorkspace(ctx, change, harness.Workspace())
		if workspaceErr == nil {
			if one, ok := workspaces.(paseo.OneActiveWorkspace); ok {
				observation, observeErr := client.FindOwnSessions(ctx, change, one.Workspace.ID(), harness.Workspace())
				if waiting, ok := observation.(orchestrator.OwnSessionAwaitingAction); observeErr == nil && ok && waiting.Reason == orchestrator.SessionPermissionRequested {
					return waiting.Session
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("не дождаться запроса разрешения собственной сессии")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func runForeignIntegrationSession(
	t *testing.T,
	harness *testpaseo.Harness,
	workspaceID string,
	labels map[string]string,
) string {
	t.Helper()
	args := []string{
		"run", "--background", "--workspace", workspaceID,
		"--provider", testpaseo.ProviderID, "--model", testpaseo.ModelID,
	}
	for key, value := range labels {
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, "--json", "--", "Чужое тестовое поручение без изменения файлов.")
	result := harness.RunCLI(t, args...)
	var created struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(result.Stdout, &created); err != nil || created.AgentID == "" {
		t.Fatalf("прочитать ID чужой сессии: %v, stdout=%s", err, result.Stdout)
	}
	return created.AgentID
}

func assertIntegrationSessionNotArchived(t *testing.T, harness *testpaseo.Harness, sessionID string) {
	t.Helper()
	result := harness.RunCLI(t, "inspect", sessionID, "--json")
	var inspection struct {
		ID       string `json:"Id"`
		Archived bool   `json:"Archived"`
	}
	if err := json.Unmarshal(result.Stdout, &inspection); err != nil {
		t.Fatalf("прочитать inspect чужой сессии: %v", err)
	}
	if inspection.ID != sessionID || inspection.Archived {
		t.Fatalf("чужая сессия изменена сопровождением: %#v", inspection)
	}
}
