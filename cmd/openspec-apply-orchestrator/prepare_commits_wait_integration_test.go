//go:build paseo_integration

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/testpaseo"
)

func TestProductionКомандаНеОбращаетсяКPaseoВнутриБлокирующегоWait(t *testing.T) {
	t.Parallel()
	harness := startProductionHarness(t)
	harness.EnableCommandRecording(t)
	prepareProductionRepository(t, harness.Workspace())
	makeProductionRepositoryDirty(t, harness.Workspace())
	binary := buildProductionCommand(t)
	harness.SetBehavior(t, testpaseo.BehaviorCommitAndWork)

	process := startProductionCommand(t, binary, harness)
	sessionID := waitForOnlyOwnSession(t, harness, process)
	waitForRecordedCommandEvent(t, harness, testpaseo.CommandStarted, "wait")
	waitForCleanGit(t, harness.Workspace())
	waitForOutput(t, &process.output, "Ожидание продолжается")

	duringWait := harness.RecordedCommandEvents(t)
	if err := validateBlockingWaitEvents(duringWait, sessionID); err != nil {
		t.Fatalf("граница блокирующего wait нарушена: %v\nсобытия: %#v", err, duringWait)
	}
	withTechnicalRead := append([]testpaseo.CommandEvent{}, duringWait...)
	withTechnicalRead = append(withTechnicalRead, testpaseo.CommandEvent{
		Phase:     testpaseo.CommandStarted,
		Arguments: []string{"inspect", sessionID, "--json"},
	})
	if err := validateBlockingWaitEvents(withTechnicalRead, sessionID); err == nil {
		t.Fatal("проверка границы не обнаружила техническое чтение внутри wait")
	}

	harness.SetBehavior(t, testpaseo.BehaviorFinish)
	result := process.wait(t)
	if result.exitCode != exitSuccess {
		t.Fatalf("production-команда завершилась с кодом %d:\n%s", result.exitCode, result.output)
	}
	afterWait := harness.RecordedCommandEvents(t)
	if err := validateFreshObservationAfterWait(afterWait, sessionID); err != nil {
		t.Fatalf("после wait нарушен порядок свежего наблюдения: %v\nсобытия: %#v", err, afterWait)
	}
	assertSessionArchived(t, harness, sessionID)
}

func waitForRecordedCommandEvent(
	t *testing.T,
	harness *testpaseo.Harness,
	phase testpaseo.CommandPhase,
	name string,
) {
	t.Helper()
	deadline := time.Now().Add(productionIntegrationEventTimeout)
	for time.Now().Before(deadline) {
		if commandEventIndex(harness.RecordedCommandEvents(t), phase, name, 0) >= 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("не дождаться события %q команды %q: %#v", phase, name, harness.RecordedCommandEvents(t))
}

func waitForOutput(t *testing.T, output *synchronizedBuffer, fragment string) {
	t.Helper()
	deadline := time.Now().Add(productionIntegrationEventTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), fragment) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("не дождаться фрагмента %q в выводе:\n%s", fragment, output.String())
}

func validateBlockingWaitEvents(events []testpaseo.CommandEvent, sessionID string) error {
	waitStarted := commandEventIndex(events, testpaseo.CommandStarted, "wait", 0)
	if waitStarted < 0 {
		return errors.New("не зафиксировано начало wait")
	}
	if !containsArgument(events[waitStarted].Arguments, sessionID) {
		return fmt.Errorf("wait относится к другой сессии: %#v", events[waitStarted].Arguments)
	}
	if waitStarted != len(events)-1 {
		return fmt.Errorf("после начала wait зафиксировано событие %#v", events[waitStarted+1])
	}
	return nil
}

func validateFreshObservationAfterWait(events []testpaseo.CommandEvent, sessionID string) error {
	waitStarted := commandEventIndex(events, testpaseo.CommandStarted, "wait", 0)
	if waitStarted < 0 || waitStarted+1 >= len(events) {
		return errors.New("не зафиксированы обе границы wait")
	}
	waitFinished := events[waitStarted+1]
	if waitFinished.Phase != testpaseo.CommandFinished ||
		!sameArguments(events[waitStarted].Arguments, waitFinished.Arguments) {
		return fmt.Errorf("после начала wait ожидалось завершение того же вызова, получено %#v", waitFinished)
	}

	started := make([]testpaseo.CommandEvent, 0)
	for _, event := range events[waitStarted+2:] {
		if event.Phase == testpaseo.CommandStarted {
			started = append(started, event)
		}
	}
	if len(started) < 5 {
		return errors.New("не зафиксирована полная последовательность workspace, фильтров, inspect и archive")
	}
	for index, expected := range [][]string{{"workspace", "ls"}, {"ls"}, {"ls"}, {"inspect"}} {
		if !hasCommandPrefix(started[index].Arguments, expected...) {
			return fmt.Errorf(
				"свежее наблюдение %d ожидало %q, получено %#v",
				index+1,
				expected,
				started[index].Arguments,
			)
		}
	}
	if argumentCount(started[1].Arguments, "--label") != 2 ||
		argumentCount(started[2].Arguments, "--label") != 5 {
		return fmt.Errorf(
			"ожидались широкий и точный фильтры собственной сессии, получено %#v и %#v",
			started[1].Arguments,
			started[2].Arguments,
		)
	}
	if !containsArgument(started[3].Arguments, sessionID) {
		return fmt.Errorf("inspect относится не к сессии %s", sessionID)
	}
	archive := commandEventIndex(events, testpaseo.CommandStarted, "archive", waitStarted+2)
	if archive < 0 || !containsArgument(events[archive].Arguments, sessionID) {
		return fmt.Errorf("inspect или archive относятся не к сессии %s", sessionID)
	}
	return nil
}

func commandEventIndex(
	events []testpaseo.CommandEvent,
	phase testpaseo.CommandPhase,
	name string,
	start int,
) int {
	if start < 0 {
		return -1
	}
	for index := start; index < len(events); index++ {
		if events[index].Phase == phase && hasCommandPrefix(events[index].Arguments, name) {
			return index
		}
	}
	return -1
}

func hasCommandPrefix(arguments []string, expected ...string) bool {
	if len(arguments) < len(expected) {
		return false
	}
	for index := range expected {
		if arguments[index] != expected[index] {
			return false
		}
	}
	return true
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func argumentCount(arguments []string, expected string) int {
	count := 0
	for _, argument := range arguments {
		if argument == expected {
			count++
		}
	}
	return count
}

func sameArguments(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
