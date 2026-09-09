//go:build paseo_integration

package testpaseo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

const (
	ProviderID        = "oa-integration"
	ProfileProviderID = "oa-profile"
	ModelID           = "deterministic"
	UserPluginID      = "oa-contract-fixture"
)

func PaseoVersion() string {
	return paseocli.ActiveContract().CLIVersion()
}

func ModeID() string {
	mode, supported := paseocli.IntegrationFullAccessMode(ProviderID)
	if !supported {
		panic("активный интеграционный контракт не содержит режим тестового провайдера")
	}
	return mode.ID()
}

const (
	controlEnvironment = "OA_TESTPASEO_CONTROL"
	recordEnvironment  = "OA_TESTPASEO_RECORD"
)

type CLIResult struct {
	Stdout []byte
	Stderr []byte
}

type CommandPhase string

const (
	CommandStarted  CommandPhase = "started"
	CommandFinished CommandPhase = "finished"
)

type CommandEvent struct {
	Phase     CommandPhase `json:"phase"`
	Arguments []string     `json:"arguments"`
}

type Behavior string

const (
	BehaviorWorking       Behavior = "working"
	BehaviorFinish        Behavior = "finish"
	BehaviorCommit        Behavior = "commit"
	BehaviorCommitAndWork Behavior = "commit-and-work"
	BehaviorPermission    Behavior = "permission"
	BehaviorError         Behavior = "error"
)

type DriverOperation string

const (
	DriverStart     DriverOperation = "start"
	DriverObserve   DriverOperation = "observe"
	DriverReconcile DriverOperation = "reconcile"
)

type DriverObservation string

const (
	ObservationNoWorkspace   DriverObservation = "no_workspace"
	ObservationNoSession     DriverObservation = "no_session"
	ObservationWorking       DriverObservation = "working"
	ObservationTurnFinished  DriverObservation = "turn_finished"
	ObservationPermission    DriverObservation = "permission"
	ObservationAgentError    DriverObservation = "agent_error"
	ObservationClosed        DriverObservation = "closed"
	ObservationAmbiguous     DriverObservation = "ambiguous"
	ObservationNotApplicable DriverObservation = "not_applicable"
)

type DriverErrorKind string

const (
	DriverCanceled              DriverErrorKind = "canceled"
	DriverUnsupportedFilesystem DriverErrorKind = "unsupported_filesystem"
	DriverRunOutcomeUnknown     DriverErrorKind = "run_outcome_unknown"
	DriverOtherError            DriverErrorKind = "other"
)

type DriverRequest struct {
	Operation   DriverOperation
	Change      string
	Prompt      string
	WorkingRoot string
	ChangeRoot  string
}

type DriverResult struct {
	Operation   DriverOperation   `json:"operation"`
	Version     string            `json:"version,omitempty"`
	ServerID    string            `json:"serverId,omitempty"`
	WorkspaceID string            `json:"workspaceId,omitempty"`
	SessionID   string            `json:"sessionId,omitempty"`
	Observation DriverObservation `json:"observation"`
	Error       string            `json:"error,omitempty"`
	ErrorKind   DriverErrorKind   `json:"errorKind,omitempty"`
}

type DriverProcess struct {
	Command *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

type Harness struct {
	cliPath     string
	path        string
	home        string
	listen      string
	host        string
	workspace   string
	changeRoot  string
	controlPath string
	recordPath  string
	driverPath  string
	proxyPath   string
	mutationLog string
	commandLog  string
	eventLog    string
	faultPath   string
	pluginMark  string

	exportEnvironment bool
	recordCommands    bool
	recordMutations   bool
	dropRunOutput     bool
}

func Start(t *testing.T) *Harness {
	t.Helper()
	return start(t, true, false)
}

func StartIsolated(t *testing.T) *Harness {
	t.Helper()
	return start(t, false, false)
}

func StartWithUserPlugin(t *testing.T) *Harness {
	t.Helper()
	return start(t, true, true)
}

func start(t *testing.T, exportEnvironment, withUserPlugin bool) *Harness {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("интеграционный стенд Paseo поддерживается только на Linux")
	}

	cliPath, err := exec.LookPath("paseo")
	if err != nil {
		t.Fatalf("найти установленный paseo: %v", err)
	}
	root := moduleRoot(t)
	home, err := os.MkdirTemp("", "oa-paseo-")
	if err != nil {
		t.Fatalf("создать временный каталог Paseo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(home); err != nil {
			t.Errorf("удалить временный каталог Paseo: %v", err)
		}
	})

	harness := &Harness{
		cliPath:     cliPath,
		path:        os.Getenv("PATH"),
		home:        home,
		listen:      filepath.Join(home, "daemon.sock"),
		workspace:   filepath.Join(home, "workspace"),
		changeRoot:  filepath.Join(home, "change"),
		controlPath: filepath.Join(home, "provider.control"),
		recordPath:  filepath.Join(home, "provider-prompts.jsonl"),
		driverPath:  filepath.Join(home, "reconcile-driver"),
		proxyPath:   filepath.Join(home, "proxy-bin", "paseo"),
		mutationLog: filepath.Join(home, "intercepted-mutations.log"),
		commandLog:  filepath.Join(home, "commands.jsonl"),
		eventLog:    filepath.Join(home, "command-events.jsonl"),
		faultPath:   filepath.Join(home, "proxy.fault"),
		pluginMark:  filepath.Join(home, "user-plugin-observed"),

		exportEnvironment: exportEnvironment,
	}
	harness.host = "unix://" + harness.listen
	for _, directory := range []string{harness.workspace, harness.changeRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("создать каталог стенда: %v", err)
		}
	}
	providerPath := filepath.Join(home, "test-provider")
	buildTestBinary(t, root, providerPath, "./internal/paseo/testpaseo/cmd/provider")
	buildTestBinary(t, root, harness.driverPath, "./internal/paseo/testpaseo/cmd/reconcile")
	if err := os.Mkdir(filepath.Dir(harness.proxyPath), 0o700); err != nil {
		t.Fatalf("создать каталог прокси Paseo: %v", err)
	}
	buildTestBinary(t, root, harness.proxyPath, "./internal/paseo/testpaseo/cmd/paseoproxy")
	harness.SetBehavior(t, BehaviorFinish)
	pluginPath := ""
	if withUserPlugin {
		pluginPath = filepath.Join(home, "user-plugin")
		if err := harness.writeUserPlugin(pluginPath); err != nil {
			t.Fatalf("подготовить пользовательский плагин Paseo: %v", err)
		}
	}
	if err := harness.writeConfig(providerPath, pluginPath); err != nil {
		t.Fatalf("записать изолированную конфигурацию Paseo: %v", err)
	}

	if exportEnvironment {
		setProcessEnvironment(t, harness)
	}
	result, err := harness.runCLI(
		"daemon", "start",
		"--home", harness.home,
		"--listen", harness.listen,
		"--no-relay", "--no-mcp", "--no-inject-mcp", "--no-web-ui",
	)
	if err != nil {
		t.Fatalf("запустить изолированный daemon Paseo: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
	t.Cleanup(func() { harness.stop(t) })
	harness.waitUntilReady(t)
	harness.waitUntilProvidersReady(t)
	if withUserPlugin {
		harness.waitUntilUserPluginReady(t)
	}
	return harness
}

func (harness *Harness) Workspace() string {
	return harness.workspace
}

func (harness *Harness) Environment() []string {
	return harness.environment()
}

func (harness *Harness) SetBehavior(t *testing.T, behavior Behavior) {
	t.Helper()
	switch behavior {
	case BehaviorWorking, BehaviorFinish, BehaviorCommit, BehaviorCommitAndWork, BehaviorPermission, BehaviorError:
	default:
		t.Fatalf("неизвестное поведение тестового провайдера: %q", behavior)
	}
	temporary := harness.controlPath + ".new"
	if err := os.WriteFile(temporary, []byte(behavior+"\n"), 0o600); err != nil {
		t.Fatalf("записать поведение тестового провайдера: %v", err)
	}
	if err := os.Rename(temporary, harness.controlPath); err != nil {
		t.Fatalf("применить поведение тестового провайдера: %v", err)
	}
}

func (harness *Harness) InterceptRunOutput(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(harness.mutationLog, nil, 0o600); err != nil {
		t.Fatalf("подготовить журнал изменяющих команд: %v", err)
	}
	harness.recordMutations = true
	harness.dropRunOutput = true
	if harness.exportEnvironment {
		t.Setenv("OA_TESTPASEO_REAL_CLI", harness.cliPath)
		t.Setenv("OA_TESTPASEO_MUTATION_LOG", harness.mutationLog)
		t.Setenv("OA_TESTPASEO_DROP_RUN_OUTPUT", "1")
		t.Setenv("PATH", harness.proxyPathEnvironment())
	}
}

func (harness *Harness) EnableCommandRecording(t *testing.T) {
	t.Helper()
	harness.ResetCommandRecording(t)
	harness.recordCommands = true
	harness.dropRunOutput = false
	if harness.exportEnvironment {
		t.Setenv("OA_TESTPASEO_REAL_CLI", harness.cliPath)
		t.Setenv("OA_TESTPASEO_COMMAND_LOG", harness.commandLog)
		t.Setenv("OA_TESTPASEO_DROP_RUN_OUTPUT", "")
		t.Setenv("OA_TESTPASEO_FAULT_FILE", harness.faultPath)
		t.Setenv("PATH", harness.proxyPathEnvironment())
	}
}

func (harness *Harness) ResetCommandRecording(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(harness.commandLog, nil, 0o600); err != nil {
		t.Fatalf("очистить журнал команд Paseo: %v", err)
	}
	if err := os.WriteFile(harness.eventLog, nil, 0o600); err != nil {
		t.Fatalf("очистить журнал событий команд Paseo: %v", err)
	}
}

func (harness *Harness) SetCommandFault(t *testing.T, fault string) {
	t.Helper()
	switch fault {
	case "", "provider-ls-invalid-json", "wait-wrong-id", "workspace-ls-error":
	default:
		t.Fatalf("неизвестный сбой прокси Paseo: %q", fault)
	}
	if err := os.WriteFile(harness.faultPath, []byte(fault), 0o600); err != nil {
		t.Fatalf("записать управляемый сбой прокси Paseo: %v", err)
	}
}

func (harness *Harness) RecordedCommands(t *testing.T) [][]string {
	t.Helper()
	content, err := os.ReadFile(harness.commandLog)
	if err != nil {
		t.Fatalf("прочитать журнал команд Paseo: %v", err)
	}
	commands := make([][]string, 0)
	for index, line := range bytes.Split(bytes.TrimSpace(content), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var arguments []string
		if err := json.Unmarshal(line, &arguments); err != nil {
			t.Fatalf("прочитать команду Paseo %d: %v", index+1, err)
		}
		commands = append(commands, arguments)
	}
	return commands
}

func (harness *Harness) RecordedCommandEvents(t *testing.T) []CommandEvent {
	t.Helper()
	content, err := os.ReadFile(harness.eventLog)
	if err != nil {
		t.Fatalf("прочитать журнал событий команд Paseo: %v", err)
	}
	events := make([]CommandEvent, 0)
	for index, line := range bytes.Split(bytes.TrimSpace(content), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event CommandEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("прочитать событие команды Paseo %d: %v", index+1, err)
		}
		switch event.Phase {
		case CommandStarted, CommandFinished:
		default:
			t.Fatalf("прочитать фазу события команды Paseo %d: %q", index+1, event.Phase)
		}
		if len(event.Arguments) == 0 {
			t.Fatalf("событие команды Paseo %d не содержит аргументы", index+1)
		}
		events = append(events, event)
	}
	return events
}

func (harness *Harness) InterceptedRunCount(t *testing.T) int {
	t.Helper()
	count := 0
	for _, command := range harness.interceptedMutations(t) {
		if command == "run" {
			count++
		}
	}
	return count
}

func (harness *Harness) InterceptedMutationCount(t *testing.T) int {
	t.Helper()
	return len(harness.interceptedMutations(t))
}

func (harness *Harness) interceptedMutations(t *testing.T) []string {
	t.Helper()
	content, err := os.ReadFile(harness.mutationLog)
	if err != nil {
		t.Fatalf("прочитать журнал изменяющих команд: %v", err)
	}
	mutations := make([]string, 0)
	for _, line := range bytes.Split(content, []byte{'\n'}) {
		if command := strings.TrimSpace(string(line)); command != "" {
			mutations = append(mutations, command)
		}
	}
	return mutations
}

func (harness *Harness) StartDriver(t *testing.T, request DriverRequest) *DriverProcess {
	t.Helper()
	request = harness.completeDriverRequest(t, request)
	process := &DriverProcess{}
	process.Command = exec.Command(harness.driverPath, string(request.Operation))
	process.Command.Dir = request.WorkingRoot
	process.Command.Env = append(
		harness.environment(),
		"OA_TESTPASEO_CHANGE="+request.Change,
		"OA_TESTPASEO_PROMPT="+request.Prompt,
		"OA_TESTPASEO_WORKING_ROOT="+request.WorkingRoot,
		"OA_TESTPASEO_CHANGE_ROOT="+request.ChangeRoot,
	)
	process.Command.Stdout = &process.stdout
	process.Command.Stderr = &process.stderr
	if err := process.Command.Start(); err != nil {
		t.Fatalf("запустить отдельный процесс сопровождения: %v", err)
	}
	return process
}

func (harness *Harness) RunDriver(t *testing.T, request DriverRequest) DriverResult {
	t.Helper()
	return harness.StartDriver(t, request).Wait(t)
}

func (process *DriverProcess) Wait(t *testing.T) DriverResult {
	t.Helper()
	if err := process.Command.Wait(); err != nil {
		t.Fatalf(
			"процесс сопровождения завершился аварийно: %v\nstdout:\n%s\nstderr:\n%s",
			err, process.stdout.Bytes(), process.stderr.Bytes(),
		)
	}
	var result DriverResult
	decoder := json.NewDecoder(bytes.NewReader(process.stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf(
			"прочитать результат процесса сопровождения: %v\nstdout:\n%s\nstderr:\n%s",
			err, process.stdout.Bytes(), process.stderr.Bytes(),
		)
	}
	return result
}

func (harness *Harness) RunCLI(t *testing.T, args ...string) CLIResult {
	t.Helper()
	result, err := harness.runCLI(args...)
	if err != nil {
		t.Fatalf("выполнить paseo %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, result.Stdout, result.Stderr)
	}
	return result
}

func (harness *Harness) Restart(t *testing.T) {
	t.Helper()
	result, err := harness.runCLI(
		"daemon", "restart",
		"--home", harness.home,
		"--listen", harness.listen,
		"--no-relay", "--no-mcp", "--no-inject-mcp", "--no-web-ui", "--json",
	)
	if err != nil {
		t.Fatalf("перезапустить изолированный daemon Paseo: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
	harness.waitUntilReady(t)
}

func (harness *Harness) Prompts(t *testing.T) []string {
	t.Helper()
	output, err := os.ReadFile(harness.recordPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("прочитать поручения тестового провайдера: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(output), []byte{'\n'})
	prompts := make([]string, 0, len(lines))
	for index, line := range lines {
		if len(line) == 0 {
			continue
		}
		var record struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("прочитать запись поручения %d: %v", index+1, err)
		}
		prompts = append(prompts, record.Prompt)
	}
	return prompts
}

func (harness *Harness) UserPluginObserved(t *testing.T) bool {
	t.Helper()
	content, err := os.ReadFile(harness.pluginMark)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("прочитать отметку пользовательского плагина: %v", err)
	}
	return string(content) == "observed\n"
}

func (harness *Harness) writeConfig(providerPath, pluginPath string) error {
	config := map[string]any{
		"version": 1,
		"daemon": map[string]any{
			"listen": harness.listen,
			"mcp": map[string]any{
				"enabled":          false,
				"injectIntoAgents": false,
			},
			"relay": map[string]any{"enabled": false},
		},
		"agents": map[string]any{
			"providers": map[string]any{
				ProviderID: map[string]any{
					"extends": "acp",
					"label":   "OpenSpec Apply integration provider",
					"command": []string{providerPath},
					"env": map[string]string{
						controlEnvironment: harness.controlPath,
						recordEnvironment:  harness.recordPath,
					},
					"models": []map[string]any{{
						"id": ModelID, "label": "Deterministic", "isDefault": true,
					}},
				},
				ProfileProviderID: map[string]any{
					"extends": "acp",
					"label":   "OpenSpec Apply unsupported profile",
					"command": []string{providerPath},
					"env": map[string]string{
						controlEnvironment: harness.controlPath,
						recordEnvironment:  harness.recordPath,
					},
					"models": []map[string]any{{
						"id": ModelID, "label": "Deterministic", "isDefault": true,
					}},
				},
			},
		},
	}
	if pluginPath != "" {
		config["pluginsEnabled"] = true
		config["plugins"] = map[string]any{
			UserPluginID: map[string]any{
				"source":  "directory",
				"path":    pluginPath,
				"enabled": true,
			},
		}
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("собрать JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(harness.home, "config.json"), encoded, 0o600); err != nil {
		return fmt.Errorf("записать JSON: %w", err)
	}
	return nil
}

func (harness *Harness) writeUserPlugin(pluginPath string) error {
	if err := os.Mkdir(pluginPath, 0o700); err != nil {
		return fmt.Errorf("создать каталог плагина: %w", err)
	}
	manifest := []byte(`{"id":"` + UserPluginID + `","requirements":{"paseo":"` + PaseoVersion() + `"}}` + "\n")
	if err := os.WriteFile(filepath.Join(pluginPath, "paseo-plugin.json"), manifest, 0o600); err != nil {
		return fmt.Errorf("записать manifest плагина: %w", err)
	}
	source := fmt.Sprintf(`import { writeFileSync } from "node:fs";
import type { PluginServerContext } from "@getpaseo/plugin/server";

export default function contribute(server: PluginServerContext) {
  server.before("agent.create", ({ request }) => {
    writeFileSync(%q, "observed\n");
    return request;
  });
  return () => {};
}
`, harness.pluginMark)
	if err := os.WriteFile(filepath.Join(pluginPath, "index.server.ts"), []byte(source), 0o600); err != nil {
		return fmt.Errorf("записать server entry плагина: %w", err)
	}
	return nil
}

func (harness *Harness) waitUntilReady(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var lastResult CLIResult
	var lastErr error
	for {
		lastResult, lastErr = harness.runCLI("status", "--home", harness.home, "--json")
		if lastErr == nil {
			var status struct {
				LocalDaemon     string  `json:"localDaemon"`
				ConnectedDaemon string  `json:"connectedDaemon"`
				CLIVersion      string  `json:"cliVersion"`
				DaemonVersion   *string `json:"daemonVersion"`
			}
			if json.Unmarshal(lastResult.Stdout, &status) == nil &&
				status.LocalDaemon == "running" && status.ConnectedDaemon == "reachable" &&
				status.CLIVersion == PaseoVersion() && status.DaemonVersion != nil &&
				*status.DaemonVersion == PaseoVersion() {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"изолированный daemon Paseo не готов: %v\nstdout:\n%s\nstderr:\n%s",
				lastErr, lastResult.Stdout, lastResult.Stderr,
			)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (harness *Harness) waitUntilProvidersReady(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var lastResult CLIResult
	var lastErr error
	for {
		lastResult, lastErr = harness.runCLI("provider", "ls", "--json")
		if lastErr == nil {
			var providers []struct {
				Provider string `json:"provider"`
				Status   string `json:"status"`
			}
			if json.Unmarshal(lastResult.Stdout, &providers) == nil &&
				providersAvailable(providers, ProviderID, ProfileProviderID) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"тестовые провайдеры Paseo не готовы: %v\nstdout:\n%s\nstderr:\n%s",
				lastErr, lastResult.Stdout, lastResult.Stderr,
			)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (harness *Harness) waitUntilUserPluginReady(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var lastResult CLIResult
	var lastErr error
	for {
		lastResult, lastErr = harness.runCLI("plugin", "ls", "--json")
		if lastErr == nil {
			var plugins []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if json.Unmarshal(lastResult.Stdout, &plugins) == nil {
				for _, plugin := range plugins {
					if plugin.ID == UserPluginID && plugin.Status == "running" {
						return
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"пользовательский плагин Paseo не готов: %v\nstdout:\n%s\nstderr:\n%s",
				lastErr, lastResult.Stdout, lastResult.Stderr,
			)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func providersAvailable(providers []struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
}, expected ...string) bool {
	available := make(map[string]bool, len(providers))
	for _, provider := range providers {
		available[provider.Provider] = provider.Status == "available"
	}
	for _, provider := range expected {
		if !available[provider] {
			return false
		}
	}
	return true
}

func (harness *Harness) stop(t *testing.T) {
	t.Helper()
	result, err := harness.runCLI(
		"daemon", "stop", "--home", harness.home,
		"--timeout", "3", "--kill-timeout", "2", "--force", "--json",
	)
	if err != nil {
		t.Errorf("остановить изолированный daemon Paseo: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
}

func (harness *Harness) runCLI(args ...string) (CLIResult, error) {
	command := exec.Command(harness.cliPath, args...)
	command.Env = harness.environment()
	command.Dir = harness.workspace
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return CLIResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

func (harness *Harness) environment() []string {
	proxyEnabled := harness.recordCommands || harness.recordMutations || harness.dropRunOutput
	path := harness.path
	if proxyEnabled {
		path = harness.proxyPathEnvironment()
	}
	environment := removeEnvironment(
		os.Environ(),
		"PASEO_HOME", "PASEO_HOST", "PASEO_LISTEN", "PASEO_AGENT_ID", "PASEO_WORKSPACE_ID",
		"OA_TESTPASEO_REAL_CLI", "OA_TESTPASEO_MUTATION_LOG", "OA_TESTPASEO_COMMAND_LOG",
		"OA_TESTPASEO_COMMAND_EVENT_LOG",
		"OA_TESTPASEO_DROP_RUN_OUTPUT", "OA_TESTPASEO_FAULT_FILE", "OA_TESTPASEO_FAULT",
		"PATH",
	)
	environment = append(
		environment,
		"PASEO_HOME="+harness.home,
		"PASEO_HOST="+harness.host,
		"PASEO_LISTEN="+harness.listen,
		"PATH="+path,
	)
	if !proxyEnabled {
		return environment
	}
	environment = append(
		environment,
		"OA_TESTPASEO_REAL_CLI="+harness.cliPath,
	)
	if harness.recordCommands {
		environment = append(
			environment,
			"OA_TESTPASEO_COMMAND_LOG="+harness.commandLog,
			"OA_TESTPASEO_COMMAND_EVENT_LOG="+harness.eventLog,
			"OA_TESTPASEO_FAULT_FILE="+harness.faultPath,
		)
	}
	if harness.recordMutations {
		environment = append(environment, "OA_TESTPASEO_MUTATION_LOG="+harness.mutationLog)
	}
	if harness.dropRunOutput {
		environment = append(environment, "OA_TESTPASEO_DROP_RUN_OUTPUT=1")
	}
	return environment
}

func (harness *Harness) proxyPathEnvironment() string {
	return filepath.Dir(harness.proxyPath) + string(os.PathListSeparator) + harness.path
}

func (harness *Harness) completeDriverRequest(t *testing.T, request DriverRequest) DriverRequest {
	t.Helper()
	switch request.Operation {
	case DriverStart, DriverObserve, DriverReconcile:
	default:
		t.Fatalf("неизвестная операция процесса сопровождения: %q", request.Operation)
	}
	if strings.TrimSpace(request.Change) == "" || strings.TrimSpace(request.Prompt) == "" {
		t.Fatal("процессу сопровождения нужны change и поручение")
	}
	if request.WorkingRoot == "" {
		request.WorkingRoot = harness.workspace
	}
	if request.ChangeRoot == "" {
		request.ChangeRoot = harness.changeRoot
	}
	return request
}

func setProcessEnvironment(t *testing.T, harness *Harness) {
	t.Helper()
	t.Setenv("PASEO_HOME", harness.home)
	t.Setenv("PASEO_HOST", harness.host)
	t.Setenv("PASEO_LISTEN", harness.listen)
	t.Setenv("PASEO_AGENT_ID", "")
	t.Setenv("PASEO_WORKSPACE_ID", "")
}

func removeEnvironment(environment []string, names ...string) []string {
	removed := make(map[string]struct{}, len(names))
	for _, name := range names {
		removed[name] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, found := removed[name]; !found {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не определить путь исходного файла стенда")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func buildTestBinary(t *testing.T, root, outputPath, packagePath string) {
	t.Helper()
	build := exec.Command("go", "build", "-tags=paseo_integration", "-o", outputPath, packagePath)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("собрать %s: %v\n%s", packagePath, err, output)
	}
}
