//go:build paseo_integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

const recoverableInterventionScenarioTimeout = 5 * time.Minute

func TestProductionПользовательПослеСбояДоставкиПродолжаетТоЖеПоручение(t *testing.T) {
	scenario := startRecoverableInterventionScenario(t)
	harness := scenario.harness
	failing := startIntegrationNtfyReceiver(t, scenario.productionScenario, http.StatusServiceUnavailable)
	recovered := startIntegrationNtfyReceiver(t, scenario.productionScenario, http.StatusOK)
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
		sessionLink: integrationSessionLink(t, harness, scenario.sessionID),
		priority:    config.NtfyPriorityHigh,
	}
	writeInterventionConfigurationFixture(
		t,
		harness.Workspace(),
		interventionConfigurationJSON(failing.URL(), originalTokenEnvironment, "high"),
	)
	harness.ResetCommandRecording(t)

	firstProcess := startProductionCommandWithEnvironment(t, scenario.productionScenario, environment)
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

	writeInterventionConfigurationFixture(
		t,
		harness.Workspace(),
		interventionConfigurationJSON(recovered.URL(), newTokenEnvironment, "min"),
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
		t.Fatalf("процесс с исходным снимком завершился с кодом %d вместо 130:\n%s", firstResult.exitCode, firstResult.output)
	}
	assertInterventionOutputIsSafe(
		t,
		firstResult.output,
		failing.URL(),
		recovered.URL(),
		originalToken,
		newToken,
	)
	assertNoPaseoMutations(t, harness.RecordedCommands(t))
	assertOnlyOwnSession(t, harness, scenario.sessionID)

	harness.ResetCommandRecording(t)
	secondProcess := startProductionCommandWithEnvironment(t, scenario.productionScenario, environment)
	afterRestart := recovered.WaitRequest(t)
	if afterRestart.path != "/topic" {
		t.Fatalf("доставка после перезапуска пришла на неожиданный путь %q", afterRestart.path)
	}
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
	assertOnlyOwnSession(t, harness, scenario.sessionID)
	assertNoPaseoMutations(t, harness.RecordedCommands(t))
	waitForCompletedPostSuccessObservation(t, scenario.productionScenario, &secondProcess.output)

	const followUp = "Продолжить подготовку коммитов в той же сессии"
	harness.SetBehavior(t, testpaseo.BehaviorAwaitRelease)
	harness.RunCLI(t, "send", scenario.sessionID, followUp, "--no-wait", "--json")
	waitForRecordedCommandEventCount(t, scenario.productionScenario, testpaseo.CommandStarted, "wait", 1)
	recovered.AssertNoRequest(t)
	if err := validateRecoveredInterventionContinuationEvents(
		harness.RecordedCommandEvents(t),
		scenario.sessionID,
	); err != nil {
		t.Fatalf("переход от idle к продолжению нарушен: %v\nсобытия: %#v", err, harness.RecordedCommandEvents(t))
	}

	runTool(t, scenario.productionScenario, harness.Workspace(), "git", "add", "--all")
	runTool(
		t,
		scenario.productionScenario,
		harness.Workspace(),
		"git",
		"-c", "user.name=OpenSpec Apply Integration",
		"-c", "user.email=integration@example.invalid",
		"commit", "-m", "test: complete continued intervention",
	)
	harness.ReleasePrompt(t)
	waitForRecordedCommandEventCount(t, scenario.productionScenario, testpaseo.CommandFinished, "wait", 1)
	harness.RunCLI(t, "archive", scenario.sessionID, "--json")
	result := secondProcess.wait(t)
	if result.exitCode != exitSuccess {
		t.Fatalf("продолженная production-команда завершилась с кодом %d:\n%s", result.exitCode, result.output)
	}
	if status := gitOutput(t, scenario.productionScenario, "status", "--porcelain=v1"); status != "" {
		t.Fatalf("после продолжения Git остался изменённым:\n%s", status)
	}
	assertSessionArchived(t, harness, scenario.sessionID)
	assertCommandCount(t, harness.RecordedCommands(t), "run", 0)
	recovered.AssertNoRequest(t)
	assertInterventionOutputIsSafe(
		t,
		result.output,
		failing.URL(),
		recovered.URL(),
		originalToken,
		newToken,
	)

	prompts := harness.Prompts(t)
	if len(prompts) != 2 || strings.TrimSpace(prompts[1]) != followUp {
		t.Fatalf("продолжение не доставлено тому же агенту: %#v", prompts)
	}
}

type capturedIntegrationNtfyRequest struct {
	method        string
	title         string
	actions       string
	actionHeaders int
	click         string
	clickHeaders  int
	priority      string
	body          string
}

type capturedIntegrationDelivery struct {
	notification  capturedIntegrationNtfyRequest
	authorization string
	path          string
	observedAt    time.Time
}

type integrationNtfyReceiver struct {
	context  context.Context
	server   *httptest.Server
	requests chan capturedIntegrationDelivery
}

func startIntegrationNtfyReceiver(
	t *testing.T,
	scenario *productionScenario,
	status int,
) *integrationNtfyReceiver {
	t.Helper()
	receiver := &integrationNtfyReceiver{
		context:  scenario.context,
		requests: make(chan capturedIntegrationDelivery, 16),
	}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("прочитать тело сквозного ntfy-запроса: %v", err)
		}
		receiver.requests <- capturedIntegrationDelivery{
			notification: capturedIntegrationNtfyRequest{
				method:        request.Method,
				title:         request.Header.Get("Title"),
				actions:       request.Header.Get("Actions"),
				actionHeaders: len(request.Header.Values("Actions")),
				click:         request.Header.Get("Click"),
				clickHeaders:  len(request.Header.Values("Click")),
				priority:      request.Header.Get("Priority"),
				body:          string(body),
			},
			authorization: request.Header.Get("Authorization"),
			path:          request.URL.Path,
			observedAt:    time.Now(),
		}
		writer.WriteHeader(status)
	}))
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (receiver *integrationNtfyReceiver) URL() string {
	return receiver.server.URL + "/topic"
}

func (receiver *integrationNtfyReceiver) WaitRequest(t *testing.T) capturedIntegrationDelivery {
	t.Helper()
	select {
	case request := <-receiver.requests:
		return request
	case <-receiver.context.Done():
		t.Fatalf("истёк deadline пользовательского сценария: %v", receiver.context.Err())
		return capturedIntegrationDelivery{}
	case <-time.After(productionIntegrationEventTimeout):
		t.Fatal("не дождаться сквозного ntfy-запроса")
		return capturedIntegrationDelivery{}
	}
}

func (receiver *integrationNtfyReceiver) AssertNoRequest(t *testing.T) {
	t.Helper()
	select {
	case request := <-receiver.requests:
		t.Fatalf("обнаружен неожиданный ntfy-запрос: %#v", request)
	default:
	}
}

type expectedIntegrationNtfyRequest struct {
	change      string
	message     string
	sessionID   string
	sessionLink string
	priority    config.NtfyPriority
}

func validateIntegrationNtfyRequest(
	request capturedIntegrationNtfyRequest,
	expected expectedIntegrationNtfyRequest,
) error {
	if request.method != http.MethodPost {
		return fmt.Errorf("ожидался POST, получен %q", request.method)
	}
	if want := "Подготовка коммитов: " + expected.change; request.title != want {
		return fmt.Errorf("ожидался Title %q, получен %q", want, request.title)
	}
	wantAction := "view, Открыть сессию, " + expected.sessionLink + ", clear=true"
	if request.actions != wantAction || request.actionHeaders != 1 {
		return fmt.Errorf("ожидался один Actions %q, получено %d: %q", wantAction, request.actionHeaders, request.actions)
	}
	if request.click != "" || request.clickHeaders != 0 {
		return fmt.Errorf("Click должен отсутствовать, получено %d: %q", request.clickHeaders, request.click)
	}
	if request.priority != string(expected.priority) {
		return fmt.Errorf("ожидался Priority %q, получен %q", expected.priority, request.priority)
	}
	if request.body != expected.message {
		return fmt.Errorf("ожидалось тело %q, получено %q", expected.message, request.body)
	}
	for _, private := range []string{expected.sessionID, expected.sessionLink} {
		if strings.Contains(request.title, private) || strings.Contains(request.body, private) {
			return fmt.Errorf("пользовательское представление раскрывает %q", private)
		}
	}
	return nil
}

func integrationSessionLink(t *testing.T, harness *testpaseo.Harness, sessionID string) string {
	t.Helper()
	status := harness.RunCLI(t, "status", "--json")
	var observation struct {
		ServerID string `json:"serverId"`
	}
	if err := json.Unmarshal(status.Stdout, &observation); err != nil {
		t.Fatalf("прочитать serverId изолированного Paseo: %v\n%s", err, status.Stdout)
	}
	if strings.TrimSpace(observation.ServerID) == "" {
		t.Fatalf("изолированный Paseo не сообщил serverId: %s", status.Stdout)
	}
	return "paseo://h/" + observation.ServerID + "/agent/" + sessionID
}

func interventionConfigurationJSON(address, tokenEnvironment, priority string) string {
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

func writeInterventionConfigurationFixture(t *testing.T, root, content string) {
	t.Helper()
	writeIntegrationFile(t, filepath.Join(root, config.FileName), content)
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
		"--verbose",
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

type recoverableInterventionScenario struct {
	*productionScenario
	sessionID string
}

func startRecoverableInterventionScenario(t *testing.T) recoverableInterventionScenario {
	t.Helper()
	scenario := startProductionScenarioWithTimeout(t, recoverableInterventionScenarioTimeout)
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
	return recoverableInterventionScenario{productionScenario: scenario, sessionID: sessionID}
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

func assertInterventionOutputIsSafe(t *testing.T, output string, private ...string) {
	t.Helper()
	private = append(private, strings.TrimSpace(promptsPackageText()))
	for _, value := range private {
		if value != "" && strings.Contains(output, value) {
			t.Fatalf("вывод доставки раскрыл приватные данные %q:\n%s", value, output)
		}
	}
}

func waitForCompletedPostSuccessObservation(
	t *testing.T,
	scenario *productionScenario,
	output *synchronizedBuffer,
) {
	t.Helper()
	events := scenario.harness.RecordedCommandEvents(t)
	completedInspections := countCommandEvents(events, testpaseo.CommandFinished, "inspect")
	const gitRead = "Подробно: состояние Git прочитано."
	completedGitReads := strings.Count(output.String(), gitRead)

	waitForRecordedCommandEventCount(
		t,
		scenario,
		testpaseo.CommandFinished,
		"inspect",
		completedInspections+1,
	)
	waitForOutputCount(t, scenario, output, gitRead, completedGitReads+1)
}

func waitForRecordedCommandEventCount(
	t *testing.T,
	scenario *productionScenario,
	phase testpaseo.CommandPhase,
	name string,
	want int,
) {
	t.Helper()
	deadline := productionEventDeadline(t, scenario)
	for time.Now().Before(deadline) {
		if countCommandEvents(scenario.harness.RecordedCommandEvents(t), phase, name) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf(
		"не дождаться %d событий %q команды %q: %#v",
		want,
		phase,
		name,
		scenario.harness.RecordedCommandEvents(t),
	)
}

func countCommandEvents(events []testpaseo.CommandEvent, phase testpaseo.CommandPhase, name string) int {
	count := 0
	for _, event := range events {
		if event.Phase == phase && hasCommandPrefix(event.Arguments, name) {
			count++
		}
	}
	return count
}

func validateRecoveredInterventionContinuationEvents(
	events []testpaseo.CommandEvent,
	sessionID string,
) error {
	wait := commandEventIndex(events, testpaseo.CommandStarted, "wait", 0)
	if wait < 0 {
		return fmt.Errorf("не зафиксировано начало wait после продолжения")
	}
	if countCommandEvents(events, testpaseo.CommandStarted, "wait") != 1 {
		return fmt.Errorf("ожидался ровно один запуск wait после продолжения")
	}
	if !containsArgument(events[wait].Arguments, sessionID) {
		return fmt.Errorf("wait относится не к восстановленной сессии %s", sessionID)
	}

	completedInspection := false
	for index, event := range events[:wait] {
		if event.Phase == testpaseo.CommandStarted && hasCommandPrefix(event.Arguments, "run") {
			return fmt.Errorf("до продолжения создано новое поручение в событии %d", index)
		}
		if event.Phase == testpaseo.CommandFinished &&
			hasCommandPrefix(event.Arguments, "inspect") &&
			containsArgument(event.Arguments, sessionID) {
			completedInspection = true
		}
	}
	if !completedInspection {
		return fmt.Errorf("до продолжения не завершено наблюдение восстановленной сессии %s", sessionID)
	}
	return nil
}
