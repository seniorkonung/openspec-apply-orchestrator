//go:build paseo_integration

package paseo

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

const integrationPrompt = "Подтвердить восстановление сессии без изменения рабочего репозитория."

func TestРеальныйPaseoКвалифицируетСозданиеИВосстановлениеСессии(t *testing.T) {
	harness := testpaseo.StartWithUserPlugin(t)
	harness.EnableCommandRecording(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient()
	if err != nil {
		t.Fatalf("создать производственный клиент: %v", err)
	}
	environment, err := client.CheckCompatibility(ctx)
	if err != nil {
		t.Fatalf("подтвердить совместимость: %v", err)
	}
	if environment.ServerID().String() == "" {
		t.Fatalf("совместимая среда не содержит serverId")
	}
	mode, supported := compatibleFullAccessMode(environment, testpaseo.ProviderID)
	if !supported {
		t.Fatalf("тестовый провайдер не содержит проверенный режим полного доступа")
	}

	change, err := orchestrator.NewChangeKey("integration-recovery")
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	workspace, err := client.CreateWorkspace(ctx, environment, change, harness.Workspace())
	if err != nil {
		t.Fatalf("создать workspace: %v", err)
	}
	sessionID, err := client.createOwnSession(
		ctx,
		environment,
		change,
		workspace,
		runSessionSettings{
			provider: testpaseo.ProviderID,
			model:    testpaseo.ModelID,
			mode:     mode,
		},
		integrationPrompt,
	)
	if err != nil {
		t.Fatalf("создать собственную сессию: %v", err)
	}
	if !harness.UserPluginObserved(t) {
		t.Fatal("включённый пользовательский плагин не наблюдал agent.create")
	}
	assertIntegrationRunRequest(t, harness.RecordedCommands(t), workspace.ID(), change)

	observation := waitForIntegrationSession(t, ctx, client, change, workspace.ID(), harness.Workspace())
	assertObservedIntegrationSession(t, observation, sessionID)
	if prompts := harness.Prompts(t); len(prompts) != 1 || prompts[0] != integrationPrompt {
		t.Fatalf("тестовый провайдер получил неожиданные поручения: %#v", prompts)
	}

	serverID := environment.ServerID()
	harness.Restart(t)

	restartedClient, err := NewClient()
	if err != nil {
		t.Fatalf("создать клиент после перезапуска daemon: %v", err)
	}
	restartedEnvironment, err := restartedClient.CheckCompatibility(ctx)
	if err != nil {
		t.Fatalf("подтвердить совместимость после перезапуска daemon: %v", err)
	}
	if restartedEnvironment.ServerID() != serverID {
		t.Fatalf("serverId изменился после перезапуска: было %q, стало %q", serverID, restartedEnvironment.ServerID())
	}

	workspaces, err := restartedClient.FindActiveWorkspace(ctx, change, harness.Workspace())
	if err != nil {
		t.Fatalf("найти workspace после перезапуска: %v", err)
	}
	oneWorkspace, ok := workspaces.(OneActiveWorkspace)
	if !ok || oneWorkspace.Workspace.ID() != workspace.ID() {
		t.Fatalf("после перезапуска найден другой workspace: %#v", workspaces)
	}
	restartedObservation := waitForIntegrationSession(
		t, ctx, restartedClient, change, workspace.ID(), harness.Workspace(),
	)
	assertObservedIntegrationSession(t, restartedObservation, sessionID)

	filtered := harness.RunCLI(t,
		"ls", "--global",
		"--label", orchestrator.LabelOwner+"="+orchestrator.ManagedOwner,
		"--label", orchestrator.LabelVersion+"="+orchestrator.CurrentOwnershipVersion,
		"--label", orchestrator.LabelChange+"="+change.String(),
		"--label", orchestrator.LabelKind+"="+orchestrator.CommitPreparationKind,
		"--label", orchestrator.LabelWorkspace+"="+workspace.ID().String(),
		"--json",
	)
	var agents []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(filtered.Stdout, &agents); err != nil {
		t.Fatalf("прочитать результат точного фильтра: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != sessionID.String() ||
		agents[0].Name == "" || !strings.HasPrefix(integrationPrompt, agents[0].Name) {
		t.Fatalf("точный фильтр не сохранил сессию и поручение: %#v", agents)
	}
	if prompts := harness.Prompts(t); len(prompts) != 1 || prompts[0] != integrationPrompt {
		t.Fatalf("полное поручение не сохранилось после перезапуска daemon: %#v", prompts)
	}
	t.Logf(
		"serverId=%s, workspaceId=%s, sessionId=%s, состояние=%T, пользовательский plugin наблюдал создание",
		restartedEnvironment.ServerID(), workspace.ID(), sessionID, restartedObservation,
	)
}

func assertIntegrationRunRequest(
	t *testing.T,
	commands [][]string,
	workspace orchestrator.WorkspaceID,
	change orchestrator.ChangeKey,
) {
	t.Helper()
	wantRun := []string{
		"run", "--background",
		"--workspace", workspace.String(),
		"--provider", testpaseo.ProviderID,
		"--model", testpaseo.ModelID,
		"--mode", testpaseo.ModeID(),
		"--label", orchestrator.LabelOwner + "=" + orchestrator.ManagedOwner,
		"--label", orchestrator.LabelVersion + "=" + orchestrator.CurrentOwnershipVersion,
		"--label", orchestrator.LabelChange + "=" + change.String(),
		"--label", orchestrator.LabelKind + "=" + orchestrator.CommitPreparationKind,
		"--label", orchestrator.LabelWorkspace + "=" + workspace.String(),
		"--json", "--", integrationPrompt,
	}
	var runCommands [][]string
	for _, command := range commands {
		if len(command) > 0 && command[0] == "plugin" {
			t.Fatalf("производственный адаптер запросил топологию плагинов: %#v", command)
		}
		if len(command) > 0 && command[0] == "run" {
			runCommands = append(runCommands, command)
		}
	}
	if len(runCommands) != 1 || !slices.Equal(runCommands[0], wantRun) {
		t.Fatalf("неожиданные команды run при включённом плагине:\nполучено: %#v\nожидалось: %#v", runCommands, wantRun)
	}
}

func waitForIntegrationSession(
	t *testing.T,
	ctx context.Context,
	client *Client,
	change orchestrator.ChangeKey,
	workspace orchestrator.WorkspaceID,
	cwd string,
) orchestrator.OwnSessionObservation {
	t.Helper()
	for {
		observation, err := client.FindOwnSessions(ctx, change, workspace, cwd)
		if err == nil {
			if _, absent := observation.(orchestrator.NoActiveOwnSession); !absent {
				return observation
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("сессия не стала видимой: последняя ошибка: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func assertObservedIntegrationSession(
	t *testing.T,
	observation orchestrator.OwnSessionObservation,
	expected orchestrator.SessionID,
) {
	t.Helper()
	var session orchestrator.ManagedSession
	switch observed := observation.(type) {
	case orchestrator.WorkingOwnSession:
		session = observed.Session
	case orchestrator.OwnSessionAwaitingAction:
		session = observed.Session
	case orchestrator.ObservedOwnSessionClosed:
		session = observed.Session
	default:
		t.Fatalf("ожидалась одна видимая сессия, получено %T", observation)
	}
	if session.ID() != expected {
		t.Fatalf("ожидалась сессия %q, получена %q", expected, session.ID())
	}
}
