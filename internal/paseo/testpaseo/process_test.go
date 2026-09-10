//go:build paseo_integration && linux

package testpaseo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBuildBinariesDeadlineЗавершаетЗаблокированныйПроцессИПотомка(t *testing.T) {
	temporary := t.TempDir()
	pidPath := filepath.Join(temporary, "setup-processes")
	writeBlockingExecutable(t, filepath.Join(temporary, "go"))
	t.Setenv("PATH", temporary+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OA_TESTPASEO_PROCESS_PIDS", pidPath)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := BuildBinaries(ctx, temporary, filepath.Join(temporary, "binaries"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("сборка завершилась с ошибкой %v вместо deadline", err)
	}

	assertRecordedProcessesGone(t, pidPath)
}

func TestOwnedProcessДосрочноеЗавершениеОжидаетПроцессИПотомка(t *testing.T) {
	temporary := t.TempDir()
	pidPath := filepath.Join(temporary, "scenario-processes")
	executable := filepath.Join(temporary, "production-command")
	writeBlockingExecutable(t, executable)

	command := exec.Command(executable)
	command.Env = append(os.Environ(), "OA_TESTPASEO_PROCESS_PIDS="+pidPath)
	process, err := StartOwnedProcess(command)
	if err != nil {
		t.Fatalf("запустить тестовое process tree: %v", err)
	}
	waitForProcessRecord(t, pidPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := process.TerminateAndWait(ctx); err != nil {
		t.Fatalf("завершить принадлежащее стенду process tree: %v", err)
	}
	assertRecordedProcessesGone(t, pidPath)
}

func writeBlockingExecutable(t *testing.T, path string) {
	t.Helper()
	content := []byte("#!/bin/sh\n" +
		"sleep 300 &\n" +
		"child=$!\n" +
		"printf '%s %s\\n' \"$$\" \"$child\" > \"$OA_TESTPASEO_PROCESS_PIDS\"\n" +
		"wait\n")
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatalf("записать блокирующий исполняемый файл: %v", err)
	}
}

func waitForProcessRecord(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(content)) != "" {
			return parseProcessIDs(t, content)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("не дождаться записи идентификаторов процессов %s", path)
	return nil
}

func assertRecordedProcessesGone(t *testing.T, path string) {
	t.Helper()
	processIDs := waitForProcessRecord(t, path)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allGone := true
		for _, processID := range processIDs {
			if err := syscall.Kill(processID, 0); err == nil || err == syscall.EPERM {
				allGone = false
				break
			}
		}
		if allGone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("после cleanup остались процессы %v", processIDs)
}

func parseProcessIDs(t *testing.T, content []byte) []int {
	t.Helper()
	fields := strings.Fields(string(content))
	if len(fields) != 2 {
		t.Fatalf("ожидались идентификаторы процесса и потомка, получено %q", content)
	}
	result := make([]int, 0, len(fields))
	for _, field := range fields {
		processID, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("прочитать идентификатор процесса %q: %v", field, err)
		}
		result = append(result, processID)
	}
	return result
}
