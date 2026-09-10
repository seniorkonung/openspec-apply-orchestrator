//go:build paseo_integration

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/testpaseo"
)

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
