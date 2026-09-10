//go:build paseo_integration

package main

import (
	"encoding/pem"
	"fmt"
	"io"
	"net"
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

func TestProductionКомандаПродолжаетСопровождениеПриСетевыхОтказахNtfy(t *testing.T) {
	t.Parallel()
	scenario := startRecoverableDeliveryScenario(t)
	expected := expectedIntegrationNtfyRequest{
		change:      productionIntegrationChange,
		message:     "После хода агента в Git остались незакоммиченные изменения.",
		sessionID:   scenario.sessionID,
		sessionLink: integrationSessionLink(t, scenario.harness, scenario.sessionID),
		priority:    config.NtfyPriorityDefault,
	}

	for _, failure := range newDeliveryFailureFixtures(t) {
		t.Run(failure.name, func(t *testing.T) {
			scenario.harness.ResetCommandRecording(t)
			writeDeliveryConfigurationFixture(
				t,
				scenario.harness.Workspace(),
				deliveryConfigurationJSON(failure.address, "", ""),
			)

			process := startProductionCommandWithEnvironment(
				t,
				scenario.binary,
				scenario.harness,
				failure.environment,
			)
			if failure.source != nil {
				request := failure.source.WaitRequest(t)
				if request.path != "/topic" {
					t.Fatalf("исходный ntfy-запрос пришёл на неожиданный путь %q", request.path)
				}
				if err := validateIntegrationNtfyRequest(request.notification, expected); err != nil {
					t.Fatalf("ntfy-запрос до отказа не соответствует контракту: %v", err)
				}
			}
			waitForOutput(t, &process.output, "Уведомление не доставлено")
			if err := process.command.Process.Signal(os.Interrupt); err != nil {
				t.Fatalf("прервать сопровождение после сетевого отказа: %v", err)
			}
			result := process.wait(t)

			if result.exitCode != 130 {
				t.Fatalf("сопровождение завершилось с кодом %d вместо 130:\n%s", result.exitCode, result.output)
			}
			for _, private := range []string{failure.address, strings.TrimSpace(promptsPackageText())} {
				if strings.Contains(result.output, private) {
					t.Fatalf("вывод раскрыл приватные данные %q:\n%s", private, result.output)
				}
			}
			assertNoPaseoMutations(t, scenario.harness.RecordedCommands(t))
			assertOnlyOwnSession(t, scenario.harness, scenario.sessionID)
			if failure.source != nil {
				failure.source.AssertNoRequest(t)
			}
			if failure.target != nil {
				failure.target.AssertNoRequest(t)
			}
		})
	}
}

func TestProductionКомандаСохраняетПолитикуПовтораИПрименяетНовыйСнимокПослеПерезапуска(t *testing.T) {
	t.Parallel()
	scenario := startRecoverableDeliveryScenario(t)
	failing := startDeliveryProbe(t, false, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	recovered := startDeliveryProbe(t, false, nil)
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
		scenario.binary,
		scenario.harness,
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
	waitForOutputCount(t, &firstProcess.output, "Уведомление не доставлено", 2)
	if err := firstProcess.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать процесс после подтверждённого повтора: %v", err)
	}
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
		scenario.binary,
		scenario.harness,
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
	waitForOutput(t, &secondProcess.output, "Уведомление доставлено")
	inspections := countCommandEvents(
		scenario.harness.RecordedCommandEvents(t),
		testpaseo.CommandStarted,
		"inspect",
	)
	waitForRecordedCommandEventCount(
		t,
		scenario.harness,
		testpaseo.CommandStarted,
		"inspect",
		inspections+1,
	)
	recovered.AssertNoRequest(t)
	if err := secondProcess.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("прервать процесс после проверки успешного эпизода: %v", err)
	}
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
	output *synchronizedBuffer,
	fragment string,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(productionIntegrationEventTimeout)
	for time.Now().Before(deadline) {
		if strings.Count(output.String(), fragment) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
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

type deliveryFailureFixture struct {
	name        string
	address     string
	environment []string
	source      *deliveryProbe
	target      *deliveryProbe
}

func newDeliveryFailureFixtures(t *testing.T) []deliveryFailureFixture {
	t.Helper()

	timeout := startDeliveryProbe(t, false, func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	})
	serverError := startDeliveryProbe(t, false, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})

	var sameOrigin *deliveryProbe
	sameOrigin = startDeliveryProbe(t, false, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", sameOrigin.server.URL+"/redirect-target")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	})

	crossOriginTarget := startDeliveryProbe(t, false, nil)
	crossOrigin := startDeliveryProbe(t, false, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", crossOriginTarget.URL())
		writer.WriteHeader(http.StatusTemporaryRedirect)
	})

	downgradeTarget := startDeliveryProbe(t, false, nil)
	downgrade := startDeliveryProbe(t, true, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", downgradeTarget.URL())
		writer.WriteHeader(http.StatusTemporaryRedirect)
	})

	return []deliveryFailureFixture{
		{
			name:    "сервис недоступен",
			address: unavailableLoopbackURL(t),
		},
		{
			name:    "тайм-аут",
			address: timeout.URL(),
			source:  timeout,
		},
		{
			name:    "неуспешный HTTP-ответ",
			address: serverError.URL(),
			source:  serverError,
		},
		{
			name:    "same-origin redirect",
			address: sameOrigin.URL(),
			source:  sameOrigin,
		},
		{
			name:    "cross-origin redirect",
			address: crossOrigin.URL(),
			source:  crossOrigin,
			target:  crossOriginTarget,
		},
		{
			name:        "HTTPS в HTTP redirect",
			address:     downgrade.URL(),
			environment: []string{"SSL_CERT_FILE=" + writeDeliveryProbeCertificate(t, downgrade)},
			source:      downgrade,
			target:      downgradeTarget,
		},
	}
}

type deliveryCapturedRequest struct {
	notification  capturedIntegrationNtfyRequest
	authorization string
	path          string
	observedAt    time.Time
}

type deliveryProbe struct {
	server   *httptest.Server
	requests chan deliveryCapturedRequest
}

func startDeliveryProbe(
	t *testing.T,
	useTLS bool,
	respond func(http.ResponseWriter, *http.Request),
) *deliveryProbe {
	t.Helper()
	probe := &deliveryProbe{requests: make(chan deliveryCapturedRequest, 16)}
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

func unavailableLoopbackURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("выбрать недоступный loopback-адрес: %v", err)
	}
	address := "http://" + listener.Addr().String() + "/topic"
	if err := listener.Close(); err != nil {
		t.Fatalf("освободить недоступный loopback-адрес: %v", err)
	}
	return address
}

func writeDeliveryProbeCertificate(t *testing.T, probe *deliveryProbe) string {
	t.Helper()
	certificate := probe.server.Certificate()
	if certificate == nil {
		t.Fatal("TLS-стенд не предоставил сертификат")
	}
	path := filepath.Join(t.TempDir(), "delivery-probe.pem")
	writeIntegrationFile(t, path, string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate.Raw,
	})))
	return path
}

func startProductionCommandWithEnvironment(
	t *testing.T,
	binary string,
	harness *testpaseo.Harness,
	environment []string,
) *productionCommandProcess {
	t.Helper()
	command := exec.Command(
		binary,
		"prepare-commits",
		"--change",
		productionIntegrationChange,
	)
	command.Dir = harness.Workspace()
	command.Env = replaceProcessEnvironment(harness.Environment(), environment)
	process := &productionCommandProcess{command: command}
	command.Stdout = &process.output
	command.Stderr = &process.output
	if err := command.Start(); err != nil {
		t.Fatalf("запустить production-команду с окружением доставки: %v", err)
	}
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
