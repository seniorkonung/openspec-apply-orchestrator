//go:build linux

package ownership

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const (
	helperModeEnvironment        = "OA_OWNERSHIP_HELPER_MODE"
	helperWorkingRootEnvironment = "OA_OWNERSHIP_WORKING_ROOT"
	helperChangeRootEnvironment  = "OA_OWNERSHIP_CHANGE_ROOT"
)

func TestВторойПроцессТогоЖеChangeПолучаетОтдельнуюОшибкуЗанятости(t *testing.T) {
	workingRoot, changeRoot := makeRoots(t, "change-a")
	changeAlias := filepath.Join(t.TempDir(), "change-alias")
	if err := os.Symlink(changeRoot, changeAlias); err != nil {
		t.Fatalf("создать ссылку на корень change: %v", err)
	}

	owner := startOwnershipHelper(t, workingRoot, changeRoot)

	for _, candidate := range []string{changeRoot, changeAlias} {
		output, err := runOwnershipHelper("expect-busy", workingRoot, candidate)
		if err != nil {
			t.Fatalf("проверить занятость через %q: %v\n%s", candidate, err, output)
		}
		if !strings.Contains(string(output), "BUSY") {
			t.Fatalf("второй процесс не сообщил занятость через %q: %s", candidate, output)
		}
	}

	owner.stop(t)
	environment := mustLocalEnvironment(t, workingRoot, changeRoot)
	lock, err := environment.AcquireChangeLock()
	if err != nil {
		t.Fatalf("получить lock после обычного завершения владельца: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("освободить lock: %v", err)
	}
}

func TestРазныеChangeОдногоРабочегоКорняНеДелятLock(t *testing.T) {
	workingRoot, firstChangeRoot := makeRoots(t, "change-a")
	secondChangeRoot := filepath.Join(workingRoot, "openspec", "changes", "change-b")
	if err := os.MkdirAll(secondChangeRoot, 0o755); err != nil {
		t.Fatalf("создать второй корень change: %v", err)
	}

	firstEnvironment := mustLocalEnvironment(t, workingRoot, firstChangeRoot)
	secondEnvironment := mustLocalEnvironment(t, workingRoot, secondChangeRoot)
	firstLock, err := firstEnvironment.AcquireChangeLock()
	if err != nil {
		t.Fatalf("получить lock первого change: %v", err)
	}
	defer firstLock.Close()

	secondLock, err := secondEnvironment.AcquireChangeLock()
	if err != nil {
		t.Fatalf("получить независимый lock второго change: %v", err)
	}
	if err := secondLock.Close(); err != nil {
		t.Fatalf("освободить lock второго change: %v", err)
	}
}

func TestLockНеСоздаётСлужебныхФайловИОсвобождаетсяПриЗакрытии(t *testing.T) {
	workingRoot, changeRoot := makeRoots(t, "change-a")
	environment := mustLocalEnvironment(t, workingRoot, changeRoot)

	lock, err := environment.AcquireChangeLock()
	if err != nil {
		t.Fatalf("получить lock: %v", err)
	}
	entries, err := os.ReadDir(changeRoot)
	if err != nil {
		t.Fatalf("прочитать корень change: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("lock создал служебные файлы: %v", entries)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("освободить lock: %v", err)
	}

	replacement, err := environment.AcquireChangeLock()
	if err != nil {
		t.Fatalf("повторно получить освобождённый lock: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatalf("освободить повторный lock: %v", err)
	}
}

func TestОстановкаВладельцаПроцессомОсвобождаетLock(t *testing.T) {
	tests := []struct {
		name      string
		terminate func(*os.Process) error
	}{
		{name: "сигнал", terminate: func(process *os.Process) error { return process.Signal(syscall.SIGTERM) }},
		{name: "аварийная остановка", terminate: func(process *os.Process) error { return process.Kill() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingRoot, changeRoot := makeRoots(t, "change-a")
			owner := startOwnershipHelper(t, workingRoot, changeRoot)
			if err := tt.terminate(owner.command.Process); err != nil {
				t.Fatalf("остановить владельца: %v", err)
			}
			owner.waitAfterSignal(t)

			environment := mustLocalEnvironment(t, workingRoot, changeRoot)
			lock, err := environment.AcquireChangeLock()
			if err != nil {
				t.Fatalf("получить lock после остановки владельца: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatalf("освободить lock: %v", err)
			}
		})
	}
}

func TestПодменаКорняПослеПроверкиНеПереноситLockНаДругойКаталог(t *testing.T) {
	workingRoot, changeRoot := makeRoots(t, "change-a")
	environment := mustLocalEnvironment(t, workingRoot, changeRoot)

	movedRoot := changeRoot + "-moved"
	if err := os.Rename(changeRoot, movedRoot); err != nil {
		t.Fatalf("переместить проверенный корень change: %v", err)
	}
	if err := os.Mkdir(changeRoot, 0o755); err != nil {
		t.Fatalf("создать другой каталог по прежнему пути: %v", err)
	}

	_, err := environment.AcquireChangeLock()
	if !errors.Is(err, ErrChangeRootChanged) {
		t.Fatalf("ожидалась ошибка подмены корня, получено %v", err)
	}
}

func TestНевозможностьОткрытьПроверенныйКореньОбъясняетОтказВладения(t *testing.T) {
	workingRoot, changeRoot := makeRoots(t, "change-a")
	environment := mustLocalEnvironment(t, workingRoot, changeRoot)
	if err := os.Remove(changeRoot); err != nil {
		t.Fatalf("удалить проверенный корень change: %v", err)
	}

	_, err := environment.AcquireChangeLock()
	if !errors.Is(err, ErrChangeLock) {
		t.Fatalf("ожидалась ошибка установки владения, получено %v", err)
	}
	if got := err.Error(); !strings.Contains(got, changeRoot) {
		t.Fatalf("ошибка не указывает корень change: %q", got)
	}
}

func TestВспомогательныйПроцессВладенияChange(t *testing.T) {
	mode := os.Getenv(helperModeEnvironment)
	if mode == "" {
		t.Skip("тест запускается только как вспомогательный процесс")
	}

	environment, err := CheckLocalEnvironment(
		os.Getenv(helperWorkingRootEnvironment),
		os.Getenv(helperChangeRootEnvironment),
	)
	if err != nil {
		t.Fatalf("проверить локальную среду: %v", err)
	}

	switch mode {
	case "hold":
		lock, err := environment.AcquireChangeLock()
		if err != nil {
			t.Fatalf("получить lock владельца: %v", err)
		}
		defer lock.Close()
		if _, err := fmt.Fprintln(os.Stdout, "LOCKED"); err != nil {
			t.Fatalf("сообщить о готовности владельца: %v", err)
		}
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatalf("дождаться завершения владельца: %v", err)
		}
	case "expect-busy":
		lock, err := environment.AcquireChangeLock()
		if lock != nil {
			lock.Close()
		}
		if !errors.Is(err, ErrChangeBusy) {
			t.Fatalf("ожидалась занятость change, получено %v", err)
		}
		if _, err := fmt.Fprintln(os.Stdout, "BUSY"); err != nil {
			t.Fatalf("сообщить о занятости: %v", err)
		}
	default:
		t.Fatalf("неизвестный режим вспомогательного процесса %q", mode)
	}
}

type ownershipHelper struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stderr  *bytes.Buffer
	waited  bool
}

func startOwnershipHelper(t *testing.T, workingRoot, changeRoot string) *ownershipHelper {
	t.Helper()
	command := ownershipHelperCommand("hold", workingRoot, changeRoot)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("открыть stdin владельца: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("открыть stdout владельца: %v", err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatalf("запустить владельца: %v", err)
	}

	helper := &ownershipHelper{command: command, stdin: stdin, stderr: stderr}
	t.Cleanup(func() {
		if helper.waited {
			return
		}
		helper.command.Process.Kill()
		helper.command.Wait()
	})

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == "LOCKED" {
			return helper
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("прочитать готовность владельца: %v", err)
	}
	helper.command.Wait()
	helper.waited = true
	t.Fatalf("владелец не сообщил о готовности: %s", stderr)
	return nil
}

func (helper *ownershipHelper) stop(t *testing.T) {
	t.Helper()
	if helper.waited {
		return
	}
	if err := helper.stdin.Close(); err != nil {
		t.Fatalf("закрыть stdin владельца: %v", err)
	}
	if err := helper.command.Wait(); err != nil {
		t.Fatalf("дождаться владельца: %v: %s", err, helper.stderr)
	}
	helper.waited = true
}

func (helper *ownershipHelper) waitAfterSignal(t *testing.T) {
	t.Helper()
	if err := helper.stdin.Close(); err != nil {
		t.Fatalf("закрыть stdin остановленного владельца: %v", err)
	}
	if err := helper.command.Wait(); err == nil {
		t.Fatal("владелец после сигнала завершился с кодом 0")
	}
	helper.waited = true
}

func runOwnershipHelper(mode, workingRoot, changeRoot string) ([]byte, error) {
	return ownershipHelperCommand(mode, workingRoot, changeRoot).CombinedOutput()
}

func ownershipHelperCommand(mode, workingRoot, changeRoot string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestВспомогательныйПроцессВладенияChange$")
	command.Env = append(
		os.Environ(),
		helperModeEnvironment+"="+mode,
		helperWorkingRootEnvironment+"="+workingRoot,
		helperChangeRootEnvironment+"="+changeRoot,
	)
	return command
}

func makeRoots(t *testing.T, changeName string) (string, string) {
	t.Helper()
	workingRoot := t.TempDir()
	changeRoot := filepath.Join(workingRoot, "openspec", "changes", changeName)
	if err := os.MkdirAll(changeRoot, 0o755); err != nil {
		t.Fatalf("создать корень change: %v", err)
	}
	return workingRoot, changeRoot
}

func mustLocalEnvironment(t *testing.T, workingRoot, changeRoot string) LocalEnvironment {
	t.Helper()
	environment, err := CheckLocalEnvironment(workingRoot, changeRoot)
	if err != nil {
		t.Fatalf("проверить локальную среду: %v", err)
	}
	return environment
}
