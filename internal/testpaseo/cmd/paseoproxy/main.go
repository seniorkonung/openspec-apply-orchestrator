//go:build paseo_integration

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func main() {
	realCLI := os.Getenv("OA_TESTPASEO_REAL_CLI")
	if realCLI == "" {
		fmt.Fprintln(os.Stderr, "не задан путь настоящего paseo")
		os.Exit(2)
	}
	mutation := mutationName(os.Args[1:])
	if mutation != "" {
		if err := recordMutation(mutation); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	command := exec.Command(realCLI, os.Args[1:]...)
	command.Stdin = os.Stdin
	command.Stderr = os.Stderr
	if mutation != "run" {
		command.Stdout = os.Stdout
	}
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
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
