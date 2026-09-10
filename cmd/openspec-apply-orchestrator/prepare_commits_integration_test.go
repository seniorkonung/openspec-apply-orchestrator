//go:build paseo_integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
)

const productionIntegrationChange = "integration-commit-preparation"

const productionIntegrationEventTimeout = 90 * time.Second

const productionIntegrationScenarioTimeout = 3 * time.Minute

var productionIntegrationBinary string

func TestMain(m *testing.M) {
	temporaryRoot, err := os.MkdirTemp("", "oa-production-scenarios-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "создать каталог сборки production-бинарника: %v\n", err)
		os.Exit(1)
	}

	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "определить корень Go-модуля: %v\n", err)
		_ = os.RemoveAll(temporaryRoot)
		os.Exit(1)
	}
	productionIntegrationBinary = filepath.Join(temporaryRoot, "openspec-apply-orchestrator")
	command := exec.Command(
		"go", "build", "-tags=paseo_integration",
		"-o", productionIntegrationBinary,
		"./cmd/openspec-apply-orchestrator",
	)
	command.Dir = moduleRoot
	if output, buildErr := command.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "собрать production-бинарник: %v\n%s", buildErr, output)
		_ = os.RemoveAll(temporaryRoot)
		os.Exit(1)
	}

	exitCode := m.Run()
	if removeErr := os.RemoveAll(temporaryRoot); removeErr != nil {
		fmt.Fprintf(os.Stderr, "удалить каталог сборки production-бинарника: %v\n", removeErr)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func TestProductionПользовательПодготавливаетВсеИзмененияОднимПоручением(t *testing.T) {
	scenario := startProductionScenario(t)
	harness := scenario.harness
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())

	initialHEAD := gitOutput(t, harness.Workspace(), "rev-parse", "HEAD")
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status != "" {
		t.Fatalf("исходный Git неожиданно содержит изменения:\n%s", status)
	}
	clean := runProductionCommand(t, scenario)
	if clean.exitCode != exitSuccess {
		t.Fatalf("чистый репозиторий завершился с кодом %d:\n%s", clean.exitCode, clean.output)
	}
	if !strings.Contains(clean.output, "Поручение не требуется: незакоммиченных изменений нет.") {
		t.Fatalf("вывод чистого запуска не сообщает об отсутствии работы:\n%s", clean.output)
	}
	if currentHEAD := gitOutput(t, harness.Workspace(), "rev-parse", "HEAD"); currentHEAD != initialHEAD {
		t.Fatalf("чистый запуск изменил HEAD: было %s, стало %s", initialHEAD, currentHEAD)
	}
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status != "" {
		t.Fatalf("чистый запуск изменил рабочее дерево:\n%s", status)
	}
	if prompts := harness.Prompts(t); len(prompts) != 0 {
		t.Fatalf("для чистого Git неожиданно создан агент: %#v", prompts)
	}
	assertNoPaseoMutations(t, harness.RecordedCommands(t))
	harness.ResetCommandRecording(t)

	makeProductionRepositoryDirty(t, harness.Workspace())
	harness.SetBehavior(t, testpaseo.Behavior("commit"))
	completed := runProductionCommand(t, scenario)
	if completed.exitCode != exitSuccess {
		catalog := harness.RunCLI(t, "provider", "ls", "--json")
		t.Fatalf("подготовка коммитов завершилась с кодом %d:\n%s\nкаталог:\n%s", completed.exitCode, completed.output, catalog.Stdout)
	}
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status != "" {
		t.Fatalf("после поручения Git остался изменённым:\n%s", status)
	}
	if delivered := harness.Prompts(t); len(delivered) != 1 ||
		strings.TrimSpace(delivered[0]) != strings.TrimSpace(promptsPackageText()) {
		t.Fatalf("тестовый провайдер получил неожиданные поручения: %#v", delivered)
	}
	if count := gitOutput(t, harness.Workspace(), "rev-list", "--count", "HEAD"); count != "2" {
		t.Fatalf("ожидался один новый коммит агента, количество коммитов: %q", count)
	}
	assertIntegrationRunContract(t, harness.RecordedCommands(t))
}

func TestProductionПользовательПослеПрерыванияПродолжаетТоЖеПоручение(t *testing.T) {
	scenario := startProductionScenario(t)
	harness := scenario.harness
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())
	harness.SetBehavior(t, testpaseo.BehaviorCommitAndWork)

	interruptedProcess := startProductionCommand(t, scenario)
	sessionID := waitForOnlyOwnSession(t, scenario, interruptedProcess)
	waitForCleanGit(t, scenario)
	waitForRecordedCommand(t, scenario, "wait")
	if err := interruptedProcess.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать production-команду после создания коммита: %v", err)
	}
	interrupted := interruptedProcess.wait(t)
	if interrupted.exitCode != 130 {
		t.Fatalf("прерванная после коммита команда вернула код %d вместо 130:\n%s", interrupted.exitCode, interrupted.output)
	}
	assertSingleCommand(t, harness.RecordedCommands(t), "run")
	assertSingleDeliveredPrompt(t, harness)

	harness.ResetCommandRecording(t)
	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	recovered := runProductionCommand(t, scenario)
	if recovered.exitCode != exitSuccess {
		t.Fatalf("восстановленная команда завершилась с кодом %d:\n%s", recovered.exitCode, recovered.output)
	}
	if delivered := harness.Prompts(t); len(delivered) != 1 {
		t.Fatalf("восстановление повторно отправило поручение: %#v", delivered)
	}
	if !strings.Contains(recovered.output, "Восстановлена собственная сессия "+sessionID) {
		t.Fatalf("вывод не подтверждает восстановление сессии %s:\n%s", sessionID, recovered.output)
	}
	assertSessionArchived(t, harness, sessionID)
	assertRecoveryReadOrder(t, harness.RecordedCommands(t))
}

func TestProductionПользовательПолучаетБезопасныйОтказДоМутаций(t *testing.T) {
	scenario := startProductionScenario(t)
	harness := scenario.harness
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())
	writeProductionConfigWithoutNotifications(t, harness.Workspace())

	result := runProductionCommand(t, scenario)
	if result.exitCode != exitUsageOrConfiguration {
		t.Fatalf("отказ до мутаций вернул код %d вместо %d:\n%s", result.exitCode, exitUsageOrConfiguration, result.output)
	}
	if !strings.Contains(result.output, "notifications.intervention") {
		t.Fatalf("вывод не объясняет отсутствующий канал участия человека:\n%s", result.output)
	}
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status == "" {
		t.Fatal("безопасный отказ неожиданно очистил Git")
	}
	if prompts := harness.Prompts(t); len(prompts) != 0 {
		t.Fatalf("безопасный отказ неожиданно создал поручение: %#v", prompts)
	}
	assertNoPaseoMutations(t, harness.RecordedCommands(t))
}

type productionCommandResult struct {
	exitCode int
	output   string
}

type productionScenario struct {
	context context.Context
	harness *testpaseo.Harness
	binary  string
}

type productionCommandProcess struct {
	context context.Context
	command *exec.Cmd
	output  synchronizedBuffer
}

func startProductionScenario(t *testing.T) *productionScenario {
	t.Helper()
	harness := testpaseo.StartIsolated(t)
	ctx, cancel := context.WithTimeout(context.Background(), productionIntegrationScenarioTimeout)
	t.Cleanup(cancel)
	if productionIntegrationBinary == "" {
		t.Fatal("production-бинарник не собран общим стендом")
	}
	return &productionScenario{
		context: ctx,
		harness: harness,
		binary:  productionIntegrationBinary,
	}
}

func runProductionCommand(t *testing.T, scenario *productionScenario) productionCommandResult {
	t.Helper()
	process := startProductionCommand(t, scenario)
	return process.wait(t)
}

func startProductionCommand(t *testing.T, scenario *productionScenario) *productionCommandProcess {
	t.Helper()
	command := exec.CommandContext(
		scenario.context,
		scenario.binary,
		"prepare-commits",
		"--change",
		productionIntegrationChange,
	)
	command.Dir = scenario.harness.Workspace()
	command.Env = scenario.harness.Environment()
	process := &productionCommandProcess{context: scenario.context, command: command}
	command.Stdout = &process.output
	command.Stderr = &process.output
	if err := command.Start(); err != nil {
		t.Fatalf("запустить production-команду: %v", err)
	}
	return process
}

func (process *productionCommandProcess) wait(t *testing.T) productionCommandResult {
	t.Helper()
	finished := make(chan error, 1)
	go func() { finished <- process.command.Wait() }()
	timer := time.NewTimer(productionIntegrationEventTimeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-finished:
	case <-timer.C:
		_ = process.command.Process.Kill()
		<-finished
		t.Fatalf("production-команда не завершилась вовремя:\n%s", process.output.String())
	case <-process.context.Done():
		_ = process.command.Process.Kill()
		<-finished
		t.Fatalf("истёк deadline пользовательского сценария: %v\n%s", process.context.Err(), process.output.String())
	}
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("дождаться production-команды: %v\n%s", err, process.output.String())
		}
		exitCode = exitError.ExitCode()
	}
	return productionCommandResult{exitCode: exitCode, output: process.output.String()}
}

func prepareProductionRepository(t *testing.T, root string) {
	t.Helper()
	runTool(t, root, "git", "init", "--initial-branch=main")
	runTool(t, root, "openspec", "init", "--tools", "none", "--language", "ru", "--no-animation", "--no-copilot-cloud", ".")
	runTool(t, root, "openspec", "new", "change", productionIntegrationChange, "--schema", "spec-driven", "--json")
	writeIntegrationFile(t, filepath.Join(root, "tracked.txt"), "исходное отслеживаемое содержимое\n")
	writeIntegrationFile(t, filepath.Join(root, "staged.txt"), "исходное индексируемое содержимое\n")
	writeIntegrationFile(t, filepath.Join(root, ".git", "info", "exclude"), config.FileName+"\n")
	writeProductionConfig(t, root, testpaseo.ProviderID, testpaseo.ModelID, "")
	runTool(t, root, "git", "add", "--all")
	runTool(t, root, "git", "-c", "user.name=OpenSpec Apply Integration", "-c", "user.email=integration@example.invalid", "commit", "-m", "test: prepare integration repository")
}

func waitForOnlyOwnSession(
	t *testing.T,
	scenario *productionScenario,
	process *productionCommandProcess,
) string {
	t.Helper()
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		sessions := readOwnSessionIDs(t, scenario.harness)
		if len(sessions) == 1 && sessions[0].ID != "" {
			return sessions[0].ID
		}
		if len(sessions) > 1 {
			t.Fatalf("стенд обнаружил несколько собственных сессий: %#v", sessions)
		}
		if strings.Contains(process.output.String(), "Ошибка:") {
			t.Fatalf("production-команда завершила запуск до создания сессии:\n%s", process.output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertProductionScenarioActive(t, scenario)
	t.Fatalf("не дождаться одной активной собственной сессии:\n%s", process.output.String())
	return ""
}

type activeSessionID struct {
	ID string `json:"id"`
}

func readOwnSessionIDs(t *testing.T, harness *testpaseo.Harness) []activeSessionID {
	t.Helper()
	result := harness.RunCLI(
		t,
		"ls", "--global",
		"--label", orchestrator.LabelOwner+"="+orchestrator.ManagedOwner,
		"--label", orchestrator.LabelKind+"="+orchestrator.CommitPreparationKind,
		"--json",
	)
	var sessions []activeSessionID
	if err := json.Unmarshal(result.Stdout, &sessions); err != nil {
		t.Fatalf("прочитать активные собственные сессии: %v\n%s", err, result.Stdout)
	}
	return sessions
}

func waitForCleanGit(t *testing.T, scenario *productionScenario) {
	t.Helper()
	root := scenario.harness.Workspace()
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		if gitOutput(t, root, "status", "--porcelain=v1") == "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertProductionScenarioActive(t, scenario)
	t.Fatalf("агент не очистил Git:\n%s", gitOutput(t, root, "status", "--porcelain=v1"))
}

func productionEventDeadline(t *testing.T, scenario *productionScenario) time.Time {
	t.Helper()
	eventDeadline := time.Now().Add(productionIntegrationEventTimeout)
	scenarioDeadline, ok := scenario.context.Deadline()
	if ok && scenarioDeadline.Before(eventDeadline) {
		return scenarioDeadline
	}
	return eventDeadline
}

func assertProductionScenarioActive(t *testing.T, scenario *productionScenario) {
	t.Helper()
	if err := scenario.context.Err(); err != nil {
		t.Fatalf("истёк deadline пользовательского сценария: %v", err)
	}
}

func assertSessionArchived(t *testing.T, harness *testpaseo.Harness, sessionID string) {
	t.Helper()
	result := harness.RunCLI(t, "inspect", sessionID, "--json")
	var inspection struct {
		ID       string `json:"Id"`
		Archived bool   `json:"Archived"`
	}
	if err := json.Unmarshal(result.Stdout, &inspection); err != nil {
		t.Fatalf("прочитать архивированную сессию: %v\n%s", err, result.Stdout)
	}
	if inspection.ID != sessionID || !inspection.Archived {
		t.Fatalf("сессия %s не подтверждена как архивированная: %#v", sessionID, inspection)
	}
}

func assertIntegrationRunContract(t *testing.T, commands [][]string) {
	t.Helper()
	var run []string
	for _, command := range commands {
		if len(command) > 0 && command[0] == "run" {
			if run != nil {
				t.Fatalf("production-команда вызвала run более одного раза: %#v", commands)
			}
			run = command
		}
	}
	if run == nil {
		t.Fatalf("журнал не содержит run: %#v", commands)
	}
	for _, pair := range [][2]string{
		{"--provider", testpaseo.ProviderID},
		{"--model", testpaseo.ModelID},
		{"--mode", testpaseo.ModeID()},
	} {
		if !containsArgumentPair(run, pair[0], pair[1]) {
			t.Fatalf("run не содержит точную пару %q %q: %#v", pair[0], pair[1], run)
		}
	}
	assertSingleCommand(t, commands, "archive")
}

func assertRecoveryReadOrder(t *testing.T, commands [][]string) {
	t.Helper()
	workspaceIndex := commandIndex(commands, "workspace", 0)
	listIndex := commandIndex(commands, "ls", workspaceIndex+1)
	inspectIndex := commandIndex(commands, "inspect", listIndex+1)
	archiveIndex := commandIndex(commands, "archive", inspectIndex+1)
	if workspaceIndex < 0 || listIndex < 0 || inspectIndex < 0 || archiveIndex < 0 {
		t.Fatalf("восстановление не выполнило полное свежее наблюдение перед archive: %#v", commands)
	}
	for _, command := range commands {
		if len(command) >= 2 && command[0] == "provider" {
			t.Fatalf("восстановление прочитало каталог новой сессии: %#v", commands)
		}
	}
	assertCommandCount(t, commands, "run", 0)
}

func assertSingleCommand(t *testing.T, commands [][]string, name string) {
	t.Helper()
	assertCommandCount(t, commands, name, 1)
}

func assertCommandCount(t *testing.T, commands [][]string, name string, expected int) {
	t.Helper()
	count := 0
	for _, command := range commands {
		if len(command) > 0 && command[0] == name {
			count++
		}
	}
	if count != expected {
		t.Fatalf("команда %q выполнена %d раз, ожидалось %d: %#v", name, count, expected, commands)
	}
}

func assertNoPaseoMutations(t *testing.T, commands [][]string) {
	t.Helper()
	for _, command := range commands {
		if len(command) == 0 {
			continue
		}
		mutation := command[0] == "run" || command[0] == "archive" ||
			(command[0] == "workspace" && len(command) > 1 && command[1] == "create")
		if mutation {
			t.Fatalf("до проверки входов выполнена мутация Paseo: %#v", commands)
		}
	}
}

func assertOnlyOwnSession(t *testing.T, harness *testpaseo.Harness, expectedID string) {
	t.Helper()
	sessions := readOwnSessionIDs(t, harness)
	if len(sessions) != 1 || sessions[0].ID != expectedID {
		t.Fatalf("ожидалась одна собственная сессия %s, получено %#v", expectedID, sessions)
	}
}

func assertSingleDeliveredPrompt(t *testing.T, harness *testpaseo.Harness) {
	t.Helper()
	delivered := harness.Prompts(t)
	if len(delivered) != 1 || strings.TrimSpace(delivered[0]) != strings.TrimSpace(promptsPackageText()) {
		t.Fatalf("ожидалось одно исходное поручение без повторной доставки, получено %#v", delivered)
	}
}

func waitForRecordedCommand(t *testing.T, scenario *productionScenario, name string) {
	t.Helper()
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		if commandIndex(scenario.harness.RecordedCommands(t), name, 0) >= 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	assertProductionScenarioActive(t, scenario)
	t.Fatalf("не дождаться команды %q: %#v", name, scenario.harness.RecordedCommands(t))
}

func commandIndex(commands [][]string, name string, start int) int {
	if start < 0 {
		start = 0
	}
	for index := start; index < len(commands); index++ {
		if len(commands[index]) > 0 && commands[index][0] == name {
			return index
		}
	}
	return -1
}

func containsArgumentPair(arguments []string, key, value string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == key && arguments[index+1] == value {
			return true
		}
	}
	return false
}

func makeProductionRepositoryDirty(t *testing.T, root string) {
	t.Helper()
	writeIntegrationFile(t, filepath.Join(root, "tracked.txt"), "изменённое отслеживаемое содержимое\n")
	writeIntegrationFile(t, filepath.Join(root, "staged.txt"), "изменённое индексируемое содержимое\n")
	runTool(t, root, "git", "add", "staged.txt")
	writeIntegrationFile(t, filepath.Join(root, "untracked.txt"), "новое неотслеживаемое содержимое\n")
	status := gitOutput(t, root, "status", "--porcelain=v1")
	for _, expected := range []string{" M tracked.txt", "M  staged.txt", "?? untracked.txt"} {
		if !strings.Contains(status, expected) {
			t.Fatalf("стенд не создал состояние %q:\n%s", expected, status)
		}
	}
}

func writeProductionConfig(t *testing.T, root, provider, model, reasoning string) {
	t.Helper()
	reasoningField := ""
	if reasoning != "" {
		reasoningField = fmt.Sprintf(",\n      \"reasoning\": %q", reasoning)
	}
	content := fmt.Sprintf(`{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": %q,
      "model": %q%s
    }
  },
  "notifications": {
    "intervention": {
      "type": "ntfy",
      "url": "https://notify.example.invalid/openspec-apply"
    }
  }
}
`, provider, model, reasoningField)
	writeIntegrationFile(t, filepath.Join(root, "openspec-apply-orchestrator.json"), content)
}

func writeProductionConfigWithoutNotifications(t *testing.T, root string) {
	t.Helper()
	content := fmt.Sprintf(`{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": %q,
      "model": %q
    }
  }
}
`, testpaseo.ProviderID, testpaseo.ModelID)
	writeIntegrationFile(t, filepath.Join(root, config.FileName), content)
}

func writeIntegrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("записать %s: %v", path, err)
	}
}

func runTool(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("выполнить %s %s: %v\n%s", name, strings.Join(arguments, " "), err, output)
	}
}

func gitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("прочитать Git через git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func promptsPackageText() string {
	return prompts.CommitPreparation().Text()
}
