//go:build paseo_integration

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type commandPhase string

const (
	commandStarted  commandPhase = "started"
	commandFinished commandPhase = "finished"
)

type commandEvent struct {
	Phase     commandPhase `json:"phase"`
	Arguments []string     `json:"arguments"`
}

func main() {
	arguments := os.Args[1:]
	if err := recordCommandEvent(commandStarted, arguments); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	exitCode := run(arguments)
	if err := recordCommandEvent(commandFinished, arguments); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(exitCode)
}

func run(arguments []string) int {
	realCLI := os.Getenv("OA_TESTPASEO_REAL_CLI")
	if realCLI == "" {
		fmt.Fprintln(os.Stderr, "не задан путь настоящего paseo")
		return 2
	}
	if err := recordCommand(arguments); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if handled, code := injectFault(arguments); handled {
		return code
	}
	mutation := mutationName(arguments)
	if mutation != "" && os.Getenv("OA_TESTPASEO_MUTATION_LOG") != "" {
		if err := recordMutation(mutation); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}

	command := exec.Command(realCLI, arguments...)
	command.Stdin = os.Stdin
	command.Stderr = os.Stderr
	if mutation != "run" || os.Getenv("OA_TESTPASEO_DROP_RUN_OUTPUT") != "1" {
		command.Stdout = os.Stdout
	}
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

func injectFault(arguments []string) (bool, int) {
	fault := os.Getenv("OA_TESTPASEO_FAULT")
	if path := os.Getenv("OA_TESTPASEO_FAULT_FILE"); path != "" {
		content, err := os.ReadFile(path)
		if err == nil {
			fault = strings.TrimSpace(string(content))
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "прочитать управляемый сбой прокси Paseo: %v\n", err)
			return true, 2
		}
	}
	switch fault {
	case "":
		return false, 0
	case "provider-ls-invalid-json":
		if hasPrefix(arguments, "provider", "ls") {
			fmt.Fprint(os.Stdout, "{")
			return true, 0
		}
	case "wait-wrong-id":
		if hasPrefix(arguments, "wait") {
			fmt.Fprint(os.Stdout, `{"agentId":"wrong-session","status":"idle","message":"PRIVATE RECENT ACTIVITY"}`)
			return true, 0
		}
	case "workspace-ls-error":
		if hasPrefix(arguments, "workspace", "ls") {
			fmt.Fprintln(os.Stderr, "управляемая ошибка чтения workspace")
			return true, 1
		}
	}
	return false, 0
}

func hasPrefix(arguments []string, expected ...string) bool {
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

func recordCommand(arguments []string) error {
	path := os.Getenv("OA_TESTPASEO_COMMAND_LOG")
	if path == "" {
		return nil
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("собрать запись команды Paseo: %w", err)
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("открыть журнал команд Paseo: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("записать команду Paseo: %w", err)
	}
	return nil
}

func recordCommandEvent(phase commandPhase, arguments []string) error {
	path := os.Getenv("OA_TESTPASEO_COMMAND_EVENT_LOG")
	if path == "" {
		return nil
	}
	encoded, err := json.Marshal(commandEvent{Phase: phase, Arguments: arguments})
	if err != nil {
		return fmt.Errorf("собрать событие команды Paseo: %w", err)
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("открыть журнал событий команд Paseo: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("записать событие команды Paseo: %w", err)
	}
	return nil
}

func mutationName(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "run", "archive":
		return args[0]
	case "workspace":
		if len(args) > 1 && args[1] == "create" {
			return "workspace create"
		}
	}
	return ""
}

func recordMutation(mutation string) error {
	path := os.Getenv("OA_TESTPASEO_MUTATION_LOG")
	if path == "" {
		return errors.New("не задан путь журнала изменяющих команд")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("открыть журнал изменяющих команд: %w", err)
	}
	defer file.Close()
	if _, err := io.WriteString(file, mutation+"\n"); err != nil {
		return fmt.Errorf("записать изменяющую команду: %w", err)
	}
	return nil
}
