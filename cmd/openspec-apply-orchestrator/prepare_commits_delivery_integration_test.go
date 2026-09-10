//go:build paseo_integration

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

func TestProductionПользовательПослеСбояДоставкиПродолжаетТоЖеПоручение(t *testing.T) {
	scenario := startRecoverableDeliveryScenario(t)
	failing := startDeliveryProbe(t, scenario.productionScenario, false, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	recovered := startDeliveryProbe(t, scenario.productionScenario, false, nil)
	const (
		originalTokenEnvironment = "OA_INTEGRATION_NTFY_TOKEN_ORIGINAL_3_10"
		originalToken            = "original-integration-token-3-10"
		newTokenEnvironment      = "OA_INTEGRATION_NTFY_TOKEN_NEW_3_10"
		newToken                 = "new-integration-token-3-10"
		minimumRetryInterval     = 20 * time.Second
	)
	environment := []string{
		originalTokenEnvironment + "=" + originalToken,
		newTokenEnvironment + "=" + newToken,
	}
	originalRequest := expectedIntegrationNtfyRequest{
		change:      productionIntegrationChange,
		message:     "После хода агента в Git остались незакоммиченные изменения.",
		sessionID:   scenario.sessionID,
		sessionLink: integrationSessionLink(t, scenario.harness, scenario.sessionID),
		priority:    config.NtfyPriorityHigh,
	}
	writeDeliveryConfigurationFixture(
		t,
		scenario.harness.Workspace(),
		deliveryConfigurationJSON(failing.URL(), originalTokenEnvironment, "high"),
	)
	scenario.harness.ResetCommandRecording(t)

	firstProcess := startProductionCommandWithEnvironment(
		t,
		scenario.productionScenario,
		environment,
	)
	first := failing.WaitRequest(t)
	if first.path != "/topic" {
		t.Fatalf("первая попытка пришла на неожиданный путь %q", first.path)
	}
	if err := validateIntegrationNtfyRequest(first.notification, originalRequest); err != nil {
		t.Fatalf("первая попытка доставки не соответствует снимку: %v", err)
	}
	if first.authorization != "Bearer "+originalToken {
		t.Fatalf("первая попытка использовала неожиданный Authorization %q", first.authorization)
	}

	writeDeliveryConfigurationFixture(
		t,
		scenario.harness.Workspace(),
		deliveryConfigurationJSON(recovered.URL(), newTokenEnvironment, "min"),
	)
	second := failing.WaitRequest(t)
	if second.path != first.path {
		t.Fatalf("повтор изменил путь доставки с %q на %q", first.path, second.path)
	}
	if err := validateIntegrationNtfyRequest(second.notification, originalRequest); err != nil {
		t.Fatalf("повтор доставки изменил представление исходного снимка: %v", err)
	}
	if second.authorization != "Bearer "+originalToken {
		t.Fatalf("повтор доставки подменил Authorization: %q", second.authorization)
	}
	if elapsed := second.observedAt.Sub(first.observedAt); elapsed < minimumRetryInterval {
		t.Fatalf("повтор выполнен без ограничения частоты через %s", elapsed)
	}
	if first.notification != second.notification {
		t.Fatalf("повтор изменил пользовательские поля: первая=%#v повтор=%#v", first, second)
	}
	recovered.AssertNoRequest(t)
	waitForOutputCount(t, scenario.productionScenario, &firstProcess.output, "Уведомление не доставлено", 2)
	firstProcess.interrupt(t)
	firstResult := firstProcess.wait(t)
	if firstResult.exitCode != 130 {
		t.Fatalf("процесс с повтором завершился с кодом %d вместо 130:\n%s", firstResult.exitCode, firstResult.output)
	}
	assertDeliveryOutputIsSafe(
		t,
		firstResult.output,
		failing.URL(),
		recovered.URL(),
		originalToken,
		newToken,
	)
	assertNoPaseoMutations(t, scenario.harness.RecordedCommands(t))
	assertOnlyOwnSession(t, scenario.harness, scenario.sessionID)

	scenario.harness.ResetCommandRecording(t)
	secondProcess := startProductionCommandWithEnvironment(
		t,
		scenario.productionScenario,
		environment,
	)
	afterRestart := recovered.WaitRequest(t)
	newRequest := originalRequest
	newRequest.priority = config.NtfyPriorityMin
	if err := validateIntegrationNtfyRequest(afterRestart.notification, newRequest); err != nil {
		t.Fatalf("новый снимок после перезапуска не применён: %v", err)
	}
	if afterRestart.authorization != "Bearer "+newToken {
		t.Fatalf("новый процесс не применил новый Authorization: %q", afterRestart.authorization)
	}
	failing.AssertNoRequest(t)
	waitForOutput(t, scenario.productionScenario, &secondProcess.output, "Уведомление доставлено")
	inspections := countCommandEvents(
		scenario.harness.RecordedCommandEvents(t),
		testpaseo.CommandStarted,
		"inspect",
	)
	waitForRecordedCommandEventCount(
		t,
		scenario.productionScenario,
		testpaseo.CommandStarted,
		"inspect",
		inspections+1,
	)
	recovered.AssertNoRequest(t)
	secondProcess.interrupt(t)
	secondResult := secondProcess.wait(t)
	if secondResult.exitCode != 130 {
		t.Fatalf("восстановленный процесс завершился с кодом %d вместо 130:\n%s", secondResult.exitCode, secondResult.output)
	}
	assertDeliveryOutputIsSafe(
		t,
		secondResult.output,
		failing.URL(),
		recovered.URL(),
		originalToken,
		newToken,
	)
	if strings.Contains(secondResult.output, "Повторяю доставку уведомления") {
		t.Fatalf("успешный неизменный эпизод был назначен на повтор:\n%s", secondResult.output)
	}
	assertNoPaseoMutations(t, scenario.harness.RecordedCommands(t))
	assertOnlyOwnSession(t, scenario.harness, scenario.sessionID)
}

func waitForOutputCount(
	t *testing.T,
	scenario *productionScenario,
	output *synchronizedBuffer,
	fragment string,
	want int,
) {
	t.Helper()
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		if strings.Count(output.String(), fragment) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	assertProductionScenarioActive(t, scenario)
	t.Fatalf(
		"не дождаться %d вхождений %q в выводе:\n%s",
		want,
		fragment,
		output.String(),
	)
}

func assertDeliveryOutputIsSafe(t *testing.T, output string, private ...string) {
	t.Helper()
	private = append(private, strings.TrimSpace(promptsPackageText()))
	for _, value := range private {
		if value != "" && strings.Contains(output, value) {
			t.Fatalf("вывод доставки раскрыл приватные данные %q:\n%s", value, output)
		}
	}
}

type deliveryCapturedRequest struct {
	notification  capturedIntegrationNtfyRequest
	authorization string
	path          string
	observedAt    time.Time
}

type deliveryProbe struct {
	scenario *productionScenario
	server   *httptest.Server
	requests chan deliveryCapturedRequest
}

func startDeliveryProbe(
	t *testing.T,
	scenario *productionScenario,
	useTLS bool,
	respond func(http.ResponseWriter, *http.Request),
) *deliveryProbe {
	t.Helper()
	probe := &deliveryProbe{
		scenario: scenario,
		requests: make(chan deliveryCapturedRequest, 16),
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		probe.requests <- deliveryCapturedRequest{
			notification: capturedIntegrationNtfyRequest{
				method:        request.Method,
				title:         request.Header.Get("Title"),
				actions:       request.Header.Get("Actions"),
				actionHeaders: len(request.Header.Values("Actions")),
				click:         request.Header.Get("Click"),
				clickHeaders:  len(request.Header.Values("Click")),
				priority:      request.Header.Get("Priority"),
				body:          readDeliveryRequestBody(t, request),
			},
			authorization: request.Header.Get("Authorization"),
			path:          request.URL.Path,
			observedAt:    time.Now(),
		}
		if respond == nil {
			writer.WriteHeader(http.StatusOK)
			return
		}
		respond(writer, request)
	}))
	probe.server = server
	if useTLS {
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	return probe
}

func (probe *deliveryProbe) URL() string {
	return probe.server.URL + "/topic"
}

func (probe *deliveryProbe) WaitRequest(t *testing.T) deliveryCapturedRequest {
	t.Helper()
	select {
	case request := <-probe.requests:
		return request
	case <-probe.scenario.context.Done():
		t.Fatalf("истёк deadline пользовательского сценария: %v", probe.scenario.context.Err())
		return deliveryCapturedRequest{}
	case <-time.After(productionIntegrationEventTimeout):
		t.Fatal("не дождаться сквозного запроса доставки ntfy")
		return deliveryCapturedRequest{}
	}
}

func (probe *deliveryProbe) AssertNoRequest(t *testing.T) {
	t.Helper()
	select {
	case request := <-probe.requests:
		t.Fatalf("обнаружен неожиданный HTTP-запрос: %#v", request)
	default:
	}
}

func readDeliveryRequestBody(t *testing.T, request *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Errorf("прочитать тело сквозного ntfy-запроса: %v", err)
		return ""
	}
	return string(body)
}

func startProductionCommandWithEnvironment(
	t *testing.T,
	scenario *productionScenario,
	environment []string,
) *productionCommandProcess {
	t.Helper()
	command := exec.Command(
		scenario.binary,
		"prepare-commits",
		"--change",
		productionIntegrationChange,
	)
	command.Dir = scenario.harness.Workspace()
	command.Env = replaceProcessEnvironment(scenario.harness.Environment(), environment)
	process := &productionCommandProcess{context: scenario.context}
	command.Stdout = &process.output
	command.Stderr = &process.output
	owned, err := testpaseo.StartOwnedProcess(command)
	if err != nil {
		t.Fatalf("запустить production-команду с окружением доставки: %v", err)
	}
	process.process = owned
	t.Cleanup(func() { process.cleanup(t) })
	return process
}

func replaceProcessEnvironment(base, replacements []string) []string {
	keys := make(map[string]struct{}, len(replacements))
	for _, replacement := range replacements {
		key, _, _ := strings.Cut(replacement, "=")
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(base)+len(replacements))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := keys[key]; !replaced {
			result = append(result, entry)
		}
	}
	return append(result, replacements...)
}

type recoverableDeliveryScenario struct {
	*productionScenario
	sessionID string
}

func startRecoverableDeliveryScenario(t *testing.T) recoverableDeliveryScenario {
	t.Helper()
	scenario := startProductionScenario(t)
	harness := scenario.harness
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, scenario)
	makeProductionRepositoryDirty(t, scenario)
	harness.SetBehavior(t, testpaseo.BehaviorWorking)

	process := startProductionCommand(t, scenario)
	sessionID := waitForOnlyOwnSession(t, scenario, process)
	waitForRecordedCommandEvent(t, scenario, testpaseo.CommandStarted, "wait")
	process.interrupt(t)
	if result := process.wait(t); result.exitCode != 130 {
		t.Fatalf("исходный процесс завершился с кодом %d вместо 130:\n%s", result.exitCode, result.output)
	}

	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	harness.ResetCommandRecording(t)
	return recoverableDeliveryScenario{productionScenario: scenario, sessionID: sessionID}
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
