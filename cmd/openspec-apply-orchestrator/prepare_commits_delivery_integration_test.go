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
