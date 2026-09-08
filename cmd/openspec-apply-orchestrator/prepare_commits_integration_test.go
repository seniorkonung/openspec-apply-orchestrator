//go:build paseo_integration

package main

import (
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
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/prompts"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/testpaseo"
)

const productionIntegrationChange = "integration-commit-preparation"

func TestProductionКомандаПодготавливаетВсеВидыИзмененийЧерезРеальныеПроцессы(t *testing.T) {
	harness := testpaseo.Start(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	binary := buildProductionCommand(t)

	clean := runProductionCommand(t, binary, harness.Workspace())
	if clean.exitCode != exitSuccess {
		t.Fatalf("чистый репозиторий завершился с кодом %d:\n%s", clean.exitCode, clean.output)
	}
	if prompts := harness.Prompts(t); len(prompts) != 0 {
		t.Fatalf("для чистого Git неожиданно создан агент: %#v", prompts)
	}
	harness.ResetCommandRecording(t)

	makeProductionRepositoryDirty(t, harness.Workspace())
	harness.SetBehavior(t, testpaseo.Behavior("commit"))
	completed := runProductionCommand(t, binary, harness.Workspace())
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

func TestProductionКомандаВосстанавливаетСессиюПослеПрерыванияИКоммита(t *testing.T) {
	harness := testpaseo.Start(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())
	binary := buildProductionCommand(t)
	harness.SetBehavior(t, testpaseo.BehaviorWorking)

	workingProcess := startProductionCommand(t, binary, harness.Workspace())
	sessionID := waitForOnlyOwnSession(t, harness, workingProcess)
	waitForRecordedCommand(t, harness, "wait")
	if err := workingProcess.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать production-команду во время работы: %v", err)
	}
	workingResult := workingProcess.wait(t)
	if workingResult.exitCode != 130 {
		t.Fatalf("прерванная во время работы команда вернула код %d вместо 130:\n%s", workingResult.exitCode, workingResult.output)
	}
	assertSingleCommand(t, harness.RecordedCommands(t), "wait")
	if status := gitOutput(t, harness.Workspace(), "status", "--porcelain=v1"); status == "" {
		t.Fatal("остановка во время работы неожиданно очистила Git")
	}

	harness.ResetCommandRecording(t)
	harness.SetBehavior(t, testpaseo.BehaviorCommitAndWork)
	afterCommitProcess := startProductionCommand(t, binary, harness.Workspace())
	waitForCleanGit(t, harness.Workspace())
	waitForRecordedCommand(t, harness, "wait")
	if err := afterCommitProcess.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать production-команду после создания коммита: %v", err)
	}
	afterCommitResult := afterCommitProcess.wait(t)
	if afterCommitResult.exitCode != 130 {
		t.Fatalf("прерванная после коммита команда вернула код %d вместо 130:\n%s", afterCommitResult.exitCode, afterCommitResult.output)
	}
	if !strings.Contains(afterCommitResult.output, "Восстановлена собственная сессия "+sessionID) {
		t.Fatalf("вывод не подтверждает восстановление сессии %s:\n%s", sessionID, afterCommitResult.output)
	}
	afterCommitCommands := harness.RecordedCommands(t)
	assertSingleCommand(t, afterCommitCommands, "wait")
	assertCommandCount(t, afterCommitCommands, "run", 0)
	assertCommandCount(t, afterCommitCommands, "archive", 0)

	if err := os.Remove(filepath.Join(harness.Workspace(), config.FileName)); err != nil {
		t.Fatalf("удалить настройки будущей сессии: %v", err)
	}
	interruptRecoveredProductionCommand(t, harness, binary, sessionID)

	writeProductionConfigWithoutNotifications(t, harness.Workspace())
	interruptRecoveredProductionCommand(t, harness, binary, sessionID)

	writeProductionConfig(
		t,
		harness.Workspace(),
		"unsupported-current-provider",
		"unsupported-current-model",
		"unsupported-current-reasoning",
	)
	interruptRecoveredProductionCommand(t, harness, binary, sessionID)

	harness.ResetCommandRecording(t)
	recoveredProcess := startProductionCommand(t, binary, harness.Workspace())
	waitForRecordedCommand(t, harness, "wait")
	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	recovered := recoveredProcess.wait(t)
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

func interruptRecoveredProductionCommand(
	t *testing.T,
	harness *testpaseo.Harness,
	binary string,
	sessionID string,
) {
	t.Helper()
	harness.ResetCommandRecording(t)
	process := startProductionCommand(t, binary, harness.Workspace())
	waitForRecordedCommand(t, harness, "wait")
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать восстановленную production-команду: %v", err)
	}
	result := process.wait(t)
	if result.exitCode != 130 {
		t.Fatalf("прерванное восстановление вернуло код %d вместо 130:\n%s", result.exitCode, result.output)
	}
	if !strings.Contains(result.output, "Восстановлена собственная сессия "+sessionID) {
		t.Fatalf("вывод не подтверждает восстановление сессии %s:\n%s", sessionID, result.output)
	}
	commands := harness.RecordedCommands(t)
	assertSingleCommand(t, commands, "wait")
	assertCommandCount(t, commands, "run", 0)
	assertCommandCount(t, commands, "archive", 0)
	for _, command := range commands {
		if len(command) >= 2 && command[0] == "provider" {
			t.Fatalf("восстановление прочитало каталог новой сессии: %#v", commands)
		}
	}
}

func TestProductionКомандаОтклоняетНедостоверныеВходыДоМутацийPaseo(t *testing.T) {
	harness := testpaseo.Start(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())

	tests := []struct {
		name         string
		configure    func()
		fault        string
		wantCode     int
		wantFragment string
	}{
		{
			name: "отсутствует канал",
			configure: func() {
				writeIntegrationFile(t, filepath.Join(harness.Workspace(), config.FileName), fmt.Sprintf(`{
  "version": 1,
  "sessions": {"commit-preparation": {"provider": %q, "model": %q}}
}
`, testpaseo.ProviderID, testpaseo.ModelID))
			},
			wantCode:     exitUsageOrConfiguration,
			wantFragment: "notifications.intervention",
		},
		{
			name: "пользовательский профиль без сопоставления",
			configure: func() {
				writeProductionConfig(t, harness.Workspace(), testpaseo.ProfileProviderID, testpaseo.ModelID, "")
			},
			wantCode:     exitUsageOrConfiguration,
			wantFragment: "не имеет проверенного режима полного доступа",
		},
		{
			name: "неизвестный provider",
			configure: func() {
				writeProductionConfig(t, harness.Workspace(), "unknown-provider", testpaseo.ModelID, "")
			},
			wantCode:     exitUsageOrConfiguration,
			wantFragment: "отсутствует в каталоге Paseo",
		},
		{
			name: "неизвестная model",
			configure: func() {
				writeProductionConfig(t, harness.Workspace(), testpaseo.ProviderID, "unknown-model", "")
			},
			wantCode:     exitUsageOrConfiguration,
			wantFragment: "модель отсутствует",
		},
		{
			name: "неизвестный reasoning",
			configure: func() {
				writeProductionConfig(t, harness.Workspace(), testpaseo.ProviderID, testpaseo.ModelID, "unknown-reasoning")
			},
			wantCode:     exitUsageOrConfiguration,
			wantFragment: "reasoning отсутствует",
		},
		{
			name: "недостоверный каталог",
			configure: func() {
				writeProductionConfig(t, harness.Workspace(), testpaseo.ProviderID, testpaseo.ModelID, "")
			},
			fault:        "provider-ls-invalid-json",
			wantCode:     exitObstacle,
			wantFragment: "JSON",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness.ResetCommandRecording(t)
			harness.SetCommandFault(t, test.fault)
			test.configure()
			result := runProductionCommand(t, buildProductionCommand(t), harness.Workspace())
			if result.exitCode != test.wantCode || !strings.Contains(result.output, test.wantFragment) {
				t.Fatalf(
					"получен код %d и вывод:\n%s\nожидались код %d и фрагмент %q",
					result.exitCode, result.output, test.wantCode, test.wantFragment,
				)
			}
			assertNoPaseoMutations(t, harness.RecordedCommands(t))
		})
	}
}

func TestProductionКомандаСохраняетСессиюПриПрепятствии(t *testing.T) {
	binary := buildProductionCommand(t)
	tests := []struct {
		name           string
		behavior       testpaseo.Behavior
		fault          string
		faultAfterWait string
		wantFragment   string
		forbidFragment string
	}{
		{
			name:         "грязный Git после хода",
			behavior:     testpaseo.BehaviorFinish,
			wantFragment: "ход агента завершён, но Git остаётся изменённым",
		},
		{
			name:         "ошибка агента",
			behavior:     testpaseo.BehaviorError,
			wantFragment: "агент сообщил об ошибке",
		},
		{
			name:         "неожиданный запрос разрешения",
			behavior:     testpaseo.BehaviorPermission,
			wantFragment: "Paseo запросил разрешение вопреки режиму полного доступа",
		},
		{
			name:           "ошибка свежего чтения workspace",
			behavior:       testpaseo.BehaviorWorking,
			faultAfterWait: "workspace-ls-error",
			wantFragment:   "ошибка чтения источника Paseo",
		},
		{
			name:           "некорректный результат wait",
			behavior:       testpaseo.BehaviorWorking,
			fault:          "wait-wrong-id",
			wantFragment:   "wait вернул другую сессию",
			forbidFragment: "PRIVATE RECENT ACTIVITY",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := testpaseo.Start(t)
			harness.EnableCommandRecording(t)
			harness.SetCommandFault(t, test.fault)
			prepareProductionRepository(t, harness.Workspace())
			makeProductionRepositoryDirty(t, harness.Workspace())
			harness.SetBehavior(t, test.behavior)

			var result productionCommandResult
			if test.faultAfterWait == "" {
				result = runProductionCommand(t, binary, harness.Workspace())
			} else {
				process := startProductionCommand(t, binary, harness.Workspace())
				waitForRecordedCommand(t, harness, "wait")
				harness.SetCommandFault(t, test.faultAfterWait)
				harness.SetBehavior(t, testpaseo.BehaviorFinish)
				result = process.wait(t)
			}
			if result.exitCode != exitObstacle || !strings.Contains(result.output, test.wantFragment) {
				t.Fatalf(
					"получен код %d и вывод:\n%s\nожидались код %d и фрагмент %q",
					result.exitCode, result.output, exitObstacle, test.wantFragment,
				)
			}
			if test.forbidFragment != "" && strings.Contains(result.output, test.forbidFragment) {
				t.Fatalf("вывод раскрыл непрозрачный текст wait:\n%s", result.output)
			}
			commands := harness.RecordedCommands(t)
			assertCommandCount(t, commands, "run", 1)
			assertCommandCount(t, commands, "archive", 0)
			if sessions := readOwnSessionIDs(t, harness); len(sessions) != 1 {
				t.Fatalf("препятствие не сохранило одну собственную сессию: %#v", sessions)
			}
		})
	}
}

type productionCommandResult struct {
	exitCode int
	output   string
}

type productionCommandProcess struct {
	command *exec.Cmd
	output  synchronizedBuffer
}

func runProductionCommand(t *testing.T, binary, workingDirectory string) productionCommandResult {
	t.Helper()
	process := startProductionCommand(t, binary, workingDirectory)
	return process.wait(t)
}

func startProductionCommand(t *testing.T, binary, workingDirectory string) *productionCommandProcess {
	t.Helper()
	command := exec.Command(
		binary,
		"prepare-commits",
		"--change",
		productionIntegrationChange,
	)
	command.Dir = workingDirectory
	process := &productionCommandProcess{command: command}
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
	var err error
	select {
	case err = <-finished:
	case <-time.After(45 * time.Second):
		_ = process.command.Process.Kill()
		<-finished
		t.Fatalf("production-команда не завершилась вовремя:\n%s", process.output.String())
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
	harness *testpaseo.Harness,
	process *productionCommandProcess,
) string {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		sessions := readOwnSessionIDs(t, harness)
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

func waitForCleanGit(t *testing.T, root string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if gitOutput(t, root, "status", "--porcelain=v1") == "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("агент не очистил Git:\n%s", gitOutput(t, root, "status", "--porcelain=v1"))
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
		{"--mode", testpaseo.ModeID},
	} {
		if !containsArgumentPair(run, pair[0], pair[1]) {
			t.Fatalf("run не содержит точную пару %q %q: %#v", pair[0], pair[1], run)
		}
	}
	assertSingleCommand(t, commands, "archive")
}

func assertRecoveryReadOrder(t *testing.T, commands [][]string) {
	t.Helper()
	waitIndex := commandIndex(commands, "wait", 0)
	workspaceIndex := commandIndex(commands, "workspace", waitIndex+1)
	listIndex := commandIndex(commands, "ls", workspaceIndex+1)
	inspectIndex := commandIndex(commands, "inspect", listIndex+1)
	archiveIndex := commandIndex(commands, "archive", inspectIndex+1)
	if waitIndex < 0 || workspaceIndex < 0 || listIndex < 0 || inspectIndex < 0 || archiveIndex < 0 {
		t.Fatalf("после wait не выполнено полное свежее наблюдение перед archive: %#v", commands)
	}
	for _, command := range commands {
		if len(command) >= 2 && command[0] == "provider" {
			t.Fatalf("восстановление прочитало каталог новой сессии: %#v", commands)
		}
	}
	assertSingleCommand(t, commands, "wait")
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

func waitForRecordedCommand(t *testing.T, harness *testpaseo.Harness, name string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if commandIndex(harness.RecordedCommands(t), name, 0) >= 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("не дождаться команды %q: %#v", name, harness.RecordedCommands(t))
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

func buildProductionCommand(t *testing.T) string {
	t.Helper()
	root := integrationModuleRoot(t)
	binary := filepath.Join(t.TempDir(), "openspec-apply-orchestrator")
	runTool(t, root, "go", "build", "-tags=paseo_integration", "-o", binary, "./cmd/openspec-apply-orchestrator")
	return binary
}

func integrationModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("определить корень модуля: %v", err)
	}
	return root
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
