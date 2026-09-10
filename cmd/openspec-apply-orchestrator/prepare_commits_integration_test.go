//go:build paseo_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
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

const productionIntegrationSetupTimeout = 3 * time.Minute

var productionIntegrationBinary string
var productionIntegrationBinaries testpaseo.Binaries

func TestMain(m *testing.M) {
	setupContext, cancelSetup := context.WithTimeout(context.Background(), productionIntegrationSetupTimeout)
	defer cancelSetup()

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
	var buildOutput strings.Builder
	command.Stdout = &buildOutput
	command.Stderr = &buildOutput
	if buildErr := testpaseo.RunOwnedCommand(setupContext, command); buildErr != nil {
		fmt.Fprintf(os.Stderr, "собрать production-бинарник: %v\n%s", buildErr, buildOutput.String())
		_ = os.RemoveAll(temporaryRoot)
		os.Exit(1)
	}
	if chmodErr := os.Chmod(productionIntegrationBinary, 0o500); chmodErr != nil {
		fmt.Fprintf(os.Stderr, "сделать production-бинарник неизменяемым: %v\n", chmodErr)
		_ = os.RemoveAll(temporaryRoot)
		os.Exit(1)
	}
	if setupErr := setupContext.Err(); setupErr != nil {
		fmt.Fprintf(os.Stderr, "истёк deadline общей подготовки: %v\n", setupErr)
		_ = os.RemoveAll(temporaryRoot)
		os.Exit(1)
	}
	productionIntegrationBinaries, err = testpaseo.BuildBinaries(
		setupContext,
		moduleRoot,
		filepath.Join(temporaryRoot, "helpers"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "собрать общие helper-бинарники: %v\n", err)
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
	prepareProductionRepository(t, scenario)

	initialHEAD := gitOutput(t, scenario, "rev-parse", "HEAD")
	if status := gitOutput(t, scenario, "status", "--porcelain=v1"); status != "" {
		t.Fatalf("исходный Git неожиданно содержит изменения:\n%s", status)
	}
	clean := runProductionCommand(t, scenario)
	if clean.exitCode != exitSuccess {
		t.Fatalf("чистый репозиторий завершился с кодом %d:\n%s", clean.exitCode, clean.output)
	}
	if !strings.Contains(clean.output, "Поручение не требуется: незакоммиченных изменений нет.") {
		t.Fatalf("вывод чистого запуска не сообщает об отсутствии работы:\n%s", clean.output)
	}
	if currentHEAD := gitOutput(t, scenario, "rev-parse", "HEAD"); currentHEAD != initialHEAD {
		t.Fatalf("чистый запуск изменил HEAD: было %s, стало %s", initialHEAD, currentHEAD)
	}
	if status := gitOutput(t, scenario, "status", "--porcelain=v1"); status != "" {
		t.Fatalf("чистый запуск изменил рабочее дерево:\n%s", status)
	}
	if prompts := harness.Prompts(t); len(prompts) != 0 {
		t.Fatalf("для чистого Git неожиданно создан агент: %#v", prompts)
	}
	assertNoPaseoMutations(t, harness.RecordedCommands(t))
	harness.ResetCommandRecording(t)

	makeProductionRepositoryDirty(t, scenario)
	harness.SetBehavior(t, testpaseo.Behavior("commit"))
	completed := runProductionCommand(t, scenario)
	if completed.exitCode != exitSuccess {
		catalog := harness.RunCLI(t, "provider", "ls", "--json")
		t.Fatalf("подготовка коммитов завершилась с кодом %d:\n%s\nкаталог:\n%s", completed.exitCode, completed.output, catalog.Stdout)
	}
	if status := gitOutput(t, scenario, "status", "--porcelain=v1"); status != "" {
		t.Fatalf("после поручения Git остался изменённым:\n%s", status)
	}
	if delivered := harness.Prompts(t); len(delivered) != 1 ||
		strings.TrimSpace(delivered[0]) != strings.TrimSpace(promptsPackageText()) {
		t.Fatalf("тестовый провайдер получил неожиданные поручения: %#v", delivered)
	}
	if count := gitOutput(t, scenario, "rev-list", "--count", "HEAD"); count != "2" {
		t.Fatalf("ожидался один новый коммит агента, количество коммитов: %q", count)
	}
	assertIntegrationRunContract(t, harness.RecordedCommands(t))
}

func TestProductionПользовательПослеПрерыванияПродолжаетТоЖеПоручение(t *testing.T) {
	scenario := startProductionScenario(t)
	harness := scenario.harness
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, scenario)
	makeProductionRepositoryDirty(t, scenario)
	harness.SetBehavior(t, testpaseo.BehaviorCommitAndWork)

	interruptedProcess := startProductionCommand(t, scenario)
	sessionID := waitForOnlyOwnSession(t, scenario, interruptedProcess)
	waitForCleanGit(t, scenario)
	waitForRecordedCommand(t, scenario, "wait")
	interruptedProcess.interrupt(t)
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
	prepareProductionRepository(t, scenario)
	makeProductionRepositoryDirty(t, scenario)
	writeProductionConfigWithoutNotifications(t, harness.Workspace())
	before := captureProductionGitSnapshot(t, scenario)

	result := runProductionCommand(t, scenario)
	if result.exitCode != exitUsageOrConfiguration {
		t.Fatalf("отказ до мутаций вернул код %d вместо %d:\n%s", result.exitCode, exitUsageOrConfiguration, result.output)
	}
	if !strings.Contains(result.output, "notifications.intervention") {
		t.Fatalf("вывод не объясняет отсутствующий канал участия человека:\n%s", result.output)
	}
	after := captureProductionGitSnapshot(t, scenario)
	assertProductionGitSnapshotEqual(t, before, after)
	if prompts := harness.Prompts(t); len(prompts) != 0 {
		t.Fatalf("безопасный отказ неожиданно создал поручение: %#v", prompts)
	}
	assertNoPaseoMutations(t, harness.RecordedCommands(t))
}

type productionCommandResult struct {
	exitCode int
	output   string
}

type productionGitSnapshot struct {
	head           string
	index          string
	trackedStatus  string
	trackedFiles   map[string]productionFileSnapshot
	untrackedFiles map[string]productionFileSnapshot
}

type productionFileSnapshot struct {
	exists  bool
	mode    fs.FileMode
	content string
}

type productionScenario struct {
	context context.Context
	harness *testpaseo.Harness
	binary  string
}

type productionCommandProcess struct {
	context context.Context
	process *testpaseo.OwnedProcess
	output  synchronizedBuffer
}

func startProductionScenario(t *testing.T) *productionScenario {
	return startProductionScenarioWithTimeout(t, productionIntegrationScenarioTimeout)
}

func startProductionScenarioWithTimeout(t *testing.T, timeout time.Duration) *productionScenario {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	if productionIntegrationBinary == "" {
		t.Fatal("production-бинарник не собран общим стендом")
	}
	harness := testpaseo.StartIsolatedWithBinaries(t, ctx, productionIntegrationBinaries)
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
	command := exec.Command(
		scenario.binary,
		"prepare-commits",
		"--change",
		productionIntegrationChange,
	)
	command.Dir = scenario.harness.Workspace()
	command.Env = scenario.harness.Environment()
	process := &productionCommandProcess{context: scenario.context}
	command.Stdout = &process.output
	command.Stderr = &process.output
	owned, err := testpaseo.StartOwnedProcess(command)
	if err != nil {
		t.Fatalf("запустить production-команду: %v", err)
	}
	process.process = owned
	t.Cleanup(func() { process.cleanup(t) })
	return process
}

func (process *productionCommandProcess) wait(t *testing.T) productionCommandResult {
	t.Helper()
	timer := time.NewTimer(productionIntegrationEventTimeout)
	defer timer.Stop()
	var err error
	select {
	case <-process.process.Done():
		err = process.process.WaitError()
	case <-timer.C:
		process.terminate(t)
		t.Fatalf("production-команда не завершилась вовремя:\n%s", process.output.String())
	case <-process.context.Done():
		process.terminate(t)
		t.Fatalf("истёк deadline пользовательского сценария: %v\n%s", process.context.Err(), process.output.String())
	}
	process.terminate(t)
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

func (process *productionCommandProcess) interrupt(t *testing.T) {
	t.Helper()
	if err := process.process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать production-команду и её потомков: %v", err)
	}
}

func (process *productionCommandProcess) terminate(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := process.process.TerminateAndWait(ctx); err != nil {
		t.Fatalf("завершить production-команду и её потомков: %v", err)
	}
}

func (process *productionCommandProcess) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := process.process.TerminateAndWait(ctx); err != nil {
		t.Errorf("освободить production-команду и её потомков: %v", err)
	}
}

func prepareProductionRepository(t *testing.T, scenario *productionScenario) {
	t.Helper()
	root := scenario.harness.Workspace()
	runTool(t, scenario, root, "git", "init", "--initial-branch=main")
	runTool(t, scenario, root, "openspec", "init", "--tools", "none", "--language", "ru", "--no-animation", "--no-copilot-cloud", ".")
	runTool(t, scenario, root, "openspec", "new", "change", productionIntegrationChange, "--schema", "spec-driven", "--json")
	writeIntegrationFile(t, filepath.Join(root, "tracked.txt"), "исходное отслеживаемое содержимое\n")
	writeIntegrationFile(t, filepath.Join(root, "staged.txt"), "исходное индексируемое содержимое\n")
	writeIntegrationFile(t, filepath.Join(root, ".git", "info", "exclude"), config.FileName+"\n")
	writeProductionConfig(t, root, testpaseo.ProviderID, testpaseo.ModelID, "")
	runTool(t, scenario, root, "git", "add", "--all")
	runTool(t, scenario, root, "git", "-c", "user.name=OpenSpec Apply Integration", "-c", "user.email=integration@example.invalid", "commit", "-m", "test: prepare integration repository")
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
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		if gitOutput(t, scenario, "status", "--porcelain=v1") == "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertProductionScenarioActive(t, scenario)
	t.Fatalf("агент не очистил Git:\n%s", gitOutput(t, scenario, "status", "--porcelain=v1"))
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

func captureProductionGitSnapshot(t *testing.T, scenario *productionScenario) productionGitSnapshot {
	t.Helper()
	return productionGitSnapshot{
		head:           gitOutput(t, scenario, "rev-parse", "HEAD"),
		index:          string(gitRawOutput(t, scenario, "ls-files", "--stage", "-z")),
		trackedStatus:  string(gitRawOutput(t, scenario, "status", "--porcelain=v1", "-z", "--untracked-files=no")),
		trackedFiles:   captureProductionFiles(t, scenario, gitRawOutput(t, scenario, "ls-files", "-z")),
		untrackedFiles: captureProductionFiles(t, scenario, gitRawOutput(t, scenario, "ls-files", "--others", "--exclude-standard", "-z")),
	}
}

func assertProductionGitSnapshotEqual(
	t *testing.T,
	before productionGitSnapshot,
	after productionGitSnapshot,
) {
	t.Helper()
	if before.head != after.head {
		t.Fatalf("безопасный отказ изменил HEAD: было %s, стало %s", before.head, after.head)
	}
	if before.index != after.index {
		t.Fatal("безопасный отказ изменил логическое содержимое index")
	}
	if before.trackedStatus != after.trackedStatus {
		t.Fatalf(
			"безопасный отказ изменил состояние tracked-файлов: было %q, стало %q",
			before.trackedStatus,
			after.trackedStatus,
		)
	}
	if !reflect.DeepEqual(before.trackedFiles, after.trackedFiles) {
		t.Fatalf(
			"безопасный отказ изменил содержимое tracked-файлов: %v",
			productionChangedFiles(before.trackedFiles, after.trackedFiles),
		)
	}
	if !reflect.DeepEqual(before.untrackedFiles, after.untrackedFiles) {
		t.Fatalf(
			"безопасный отказ изменил набор или содержимое untracked-файлов: %v",
			productionChangedFiles(before.untrackedFiles, after.untrackedFiles),
		)
	}
}

func captureProductionFiles(
	t *testing.T,
	scenario *productionScenario,
	nulSeparatedPaths []byte,
) map[string]productionFileSnapshot {
	t.Helper()
	root := scenario.harness.Workspace()
	files := make(map[string]productionFileSnapshot)
	for _, rawPath := range bytes.Split(nulSeparatedPaths, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		path := string(rawPath)
		if filepath.IsAbs(path) || filepath.Clean(path) == ".." || strings.HasPrefix(filepath.Clean(path), ".."+string(os.PathSeparator)) {
			t.Fatalf("Git вернул путь вне рабочего дерева: %q", path)
		}
		fullPath := filepath.Join(root, path)
		information, err := os.Lstat(fullPath)
		if errors.Is(err, fs.ErrNotExist) {
			files[path] = productionFileSnapshot{}
			continue
		}
		if err != nil {
			t.Fatalf("прочитать состояние %s: %v", path, err)
		}
		var content []byte
		if information.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(fullPath)
			if readErr != nil {
				t.Fatalf("прочитать ссылку %s: %v", path, readErr)
			}
			content = []byte(target)
		} else {
			content, err = os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("прочитать содержимое %s: %v", path, err)
			}
		}
		files[path] = productionFileSnapshot{
			exists:  true,
			mode:    information.Mode(),
			content: string(content),
		}
	}
	return files
}

func productionChangedFiles(
	before map[string]productionFileSnapshot,
	after map[string]productionFileSnapshot,
) []string {
	changed := make(map[string]struct{})
	for path, beforeFile := range before {
		if afterFile, found := after[path]; !found || beforeFile != afterFile {
			changed[path] = struct{}{}
		}
	}
	for path, afterFile := range after {
		if beforeFile, found := before[path]; !found || beforeFile != afterFile {
			changed[path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
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

func makeProductionRepositoryDirty(t *testing.T, scenario *productionScenario) {
	t.Helper()
	root := scenario.harness.Workspace()
	writeIntegrationFile(t, filepath.Join(root, "tracked.txt"), "изменённое отслеживаемое содержимое\n")
	writeIntegrationFile(t, filepath.Join(root, "staged.txt"), "изменённое индексируемое содержимое\n")
	runTool(t, scenario, root, "git", "add", "staged.txt")
	writeIntegrationFile(t, filepath.Join(root, "untracked.txt"), "новое неотслеживаемое содержимое\n")
	status := gitOutput(t, scenario, "status", "--porcelain=v1")
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

func runTool(t *testing.T, scenario *productionScenario, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := testpaseo.RunOwnedCommand(scenario.context, command); err != nil {
		t.Fatalf("выполнить %s %s: %v\n%s", name, strings.Join(arguments, " "), err, output.String())
	}
}

func gitOutput(t *testing.T, scenario *productionScenario, arguments ...string) string {
	t.Helper()
	return strings.TrimSpace(string(gitRawOutput(t, scenario, arguments...)))
}

func gitRawOutput(t *testing.T, scenario *productionScenario, arguments ...string) []byte {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = scenario.harness.Workspace()
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := testpaseo.RunOwnedCommand(scenario.context, command); err != nil {
		t.Fatalf("прочитать Git через git %s: %v\n%s", strings.Join(arguments, " "), err, output.Bytes())
	}
	return output.Bytes()
}

func promptsPackageText() string {
	return prompts.CommitPreparation().Text()
}
