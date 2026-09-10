//go:build paseo_integration

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

func TestProductionКомандаНеСоздаётНовоеПоручениеБезКорректногоКанала(t *testing.T) {
	t.Parallel()
	harness := startProductionHarness(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())
	binary := buildProductionCommand(t)

	tests := []struct {
		name          string
		configuration string
		wantFragment  string
	}{
		{
			name:         "файл отсутствует",
			wantFragment: "конфигурация оркестратора не найдена",
		},
		{
			name:          "JSON повреждён",
			configuration: `{"version":`,
			wantFragment:  "конфигурация оркестратора содержит некорректный JSON",
		},
		{
			name: "канал отсутствует",
			configuration: fmt.Sprintf(
				`{"version":1,"sessions":{"commit-preparation":{"provider":%q,"model":%q}}}`,
				testpaseo.ProviderID,
				testpaseo.ModelID,
			),
			wantFragment: "notifications.intervention",
		},
		{
			name: "приоритет недопустим",
			configuration: fmt.Sprintf(
				`{"version":1,"sessions":{"commit-preparation":{"provider":%q,"model":%q}},"notifications":{"intervention":{"type":"ntfy","url":"https://notify.example.invalid/topic","priority":"urgent"}}}`,
				testpaseo.ProviderID,
				testpaseo.ModelID,
			),
			wantFragment: "notifications.intervention.priority",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness.ResetCommandRecording(t)
			writeDeliveryConfigurationFixture(t, harness.Workspace(), test.configuration)

			result := runProductionCommand(t, binary, harness)

			if result.exitCode != exitUsageOrConfiguration ||
				!strings.Contains(result.output, test.wantFragment) {
				t.Fatalf(
					"получен код %d и вывод:\n%s\nожидались код %d и фрагмент %q",
					result.exitCode,
					result.output,
					exitUsageOrConfiguration,
					test.wantFragment,
				)
			}
			assertNoPaseoMutations(t, harness.RecordedCommands(t))
		})
	}
}

func TestProductionКомандаВосстанавливаетСессиюПриОшибкеСнимкаИКанала(t *testing.T) {
	t.Parallel()
	scenario := startRecoverableDeliveryScenario(t)
	const missingToken = "OA_INTEGRATION_MISSING_NTFY_TOKEN_3_10"
	receiver := startIntegrationNtfyReceiver(t)

	tests := []struct {
		name          string
		configuration string
		wantFragment  string
		forbidRequest bool
	}{
		{
			name:         "файл отсутствует",
			wantFragment: "конфигурация оркестратора не найдена",
		},
		{
			name:          "JSON повреждён",
			configuration: `{"version":`,
			wantFragment:  "конфигурация оркестратора содержит некорректный JSON",
		},
		{
			name: "канал отсутствует",
			configuration: fmt.Sprintf(
				`{"version":1,"sessions":{"commit-preparation":{"provider":%q,"model":%q}}}`,
				testpaseo.ProviderID,
				testpaseo.ModelID,
			),
			wantFragment: "notifications.intervention",
		},
		{
			name: "приоритет недопустим",
			configuration: deliveryConfigurationJSON(
				receiver.URL(),
				"",
				"urgent",
			),
			wantFragment: "notifications.intervention.priority",
		},
		{
			name: "переменная токена отсутствует",
			configuration: deliveryConfigurationJSON(
				receiver.URL(),
				missingToken,
				"high",
			),
			wantFragment:  "Уведомление не доставлено",
			forbidRequest: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario.harness.ResetCommandRecording(t)
			writeDeliveryConfigurationFixture(t, scenario.harness.Workspace(), test.configuration)

			process := startProductionCommand(t, scenario.binary, scenario.harness)
			waitForOutput(t, &process.output, "Уведомление не доставлено")
			if err := process.command.Process.Signal(os.Interrupt); err != nil {
				t.Fatalf("прервать сопровождение после ошибки доставки: %v", err)
			}
			result := process.wait(t)

			if result.exitCode != 130 {
				t.Fatalf("сопровождение завершилось с кодом %d вместо 130:\n%s", result.exitCode, result.output)
			}
			for _, fragment := range []string{
				"Восстановлена собственная сессия " + scenario.sessionID,
				test.wantFragment,
				"сопровождение той же сессии продолжается",
			} {
				if !strings.Contains(result.output, fragment) {
					t.Fatalf("вывод не содержит %q:\n%s", fragment, result.output)
				}
			}
			assertNoPaseoMutations(t, scenario.harness.RecordedCommands(t))
			assertOnlyOwnSession(t, scenario.harness, scenario.sessionID)
			if test.forbidRequest {
				receiver.AssertNoRequest(t)
			}
		})
	}
}

type recoverableDeliveryScenario struct {
	harness   *testpaseo.Harness
	binary    string
	sessionID string
}

func startRecoverableDeliveryScenario(t *testing.T) recoverableDeliveryScenario {
	t.Helper()
	harness := startProductionHarness(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())
	harness.SetBehavior(t, testpaseo.BehaviorWorking)
	binary := buildProductionCommand(t)

	process := startProductionCommand(t, binary, harness)
	sessionID := waitForOnlyOwnSession(t, harness, process)
	waitForRecordedCommandEvent(t, harness, testpaseo.CommandStarted, "wait")
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать исходный процесс перед восстановлением: %v", err)
	}
	if result := process.wait(t); result.exitCode != 130 {
		t.Fatalf("исходный процесс завершился с кодом %d вместо 130:\n%s", result.exitCode, result.output)
	}

	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	harness.ResetCommandRecording(t)
	return recoverableDeliveryScenario{harness: harness, binary: binary, sessionID: sessionID}
}

func deliveryConfigurationJSON(address, tokenEnvironment, priority string) string {
	tokenField := ""
	if tokenEnvironment != "" {
		tokenField = fmt.Sprintf(",\"tokenEnv\":%q", tokenEnvironment)
	}
	priorityField := ""
	if priority != "" {
		priorityField = fmt.Sprintf(",\"priority\":%q", priority)
	}
	return fmt.Sprintf(
		`{"version":1,"sessions":{"commit-preparation":{"provider":%q,"model":%q}},"notifications":{"intervention":{"type":"ntfy","url":%q%s%s}}}`,
		testpaseo.ProviderID,
		testpaseo.ModelID,
		address,
		tokenField,
		priorityField,
	)
}

func writeDeliveryConfigurationFixture(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, config.FileName)
	if content == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("удалить конфигурацию доставки: %v", err)
		}
		return
	}
	writeIntegrationFile(t, path, content)
}
