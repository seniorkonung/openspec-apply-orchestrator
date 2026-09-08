package gitstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultCommandTimeout = 10 * time.Second
	defaultOutputLimit    = 1 << 20
	defaultWaitDelay      = 500 * time.Millisecond
)

var (
	ErrExecutableNotFound = errors.New("исполняемый файл git не найден")
	ErrCommandStart       = errors.New("не удалось запустить команду Git")
	ErrCommandExit        = errors.New("команда Git завершилась неуспешно")
	ErrCommandTimeout     = errors.New("истёк срок выполнения команды Git")
	ErrCommandCanceled    = errors.New("выполнение команды Git отменено")
	ErrStdoutLimit        = errors.New("stdout команды Git превысил предел")
	ErrStderrLimit        = errors.New("stderr команды Git превысил предел")
)

type runnerConfig struct {
	timeout     time.Duration
	stdoutLimit int
	stderrLimit int
}

func defaultRunnerConfig() runnerConfig {
	return runnerConfig{
		timeout:     defaultCommandTimeout,
		stdoutLimit: defaultOutputLimit,
		stderrLimit: defaultOutputLimit,
	}
}

type command struct {
	name             string
	workingDirectory string
	arguments        []string
}

type commandResult struct {
	stdout []byte
}

type runner struct {
	executable  string
	timeout     time.Duration
	stdoutLimit int
	stderrLimit int
}

func newRunner(config runnerConfig) (*runner, error) {
	if config.timeout <= 0 || config.stdoutLimit <= 0 || config.stderrLimit <= 0 {
		return nil, ErrInvalidRepository
	}
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExecutableNotFound, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("%w: разрешить абсолютный путь: %v", ErrExecutableNotFound, err)
	}
	return &runner{
		executable:  executable,
		timeout:     config.timeout,
		stdoutLimit: config.stdoutLimit,
		stderrLimit: config.stderrLimit,
	}, nil
}

func (runner *runner) resolveRoot(ctx context.Context, workingDirectory string) (string, error) {
	result, err := runner.run(ctx, command{
		name:             "rev-parse",
		workingDirectory: workingDirectory,
		arguments:        []string{"rev-parse", "--show-toplevel"},
	})
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(result.stdout), "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("%w: git rev-parse вернул некорректный корень", ErrUnexpectedOutput)
	}
	root, err := canonicalExistingDirectory(value)
	if err != nil {
		return "", fmt.Errorf("%w: канонизировать корень из git rev-parse: %v", ErrUnexpectedOutput, err)
	}
	if !pathContains(root, workingDirectory) {
		return "", fmt.Errorf(
			"%w: корень %q не содержит рабочий каталог %q",
			ErrUnexpectedOutput,
			root,
			workingDirectory,
		)
	}
	return root, nil
}

func pathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (runner *runner) run(ctx context.Context, invocation command) (commandResult, error) {
	if runner == nil || runner.executable == "" || runner.timeout <= 0 ||
		runner.stdoutLimit <= 0 || runner.stderrLimit <= 0 || ctx == nil ||
		invocation.name == "" || invocation.workingDirectory == "" {
		return commandResult{}, ErrInvalidRepository
	}

	commandContext, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()

	stdout := newCappedBuffer(runner.stdoutLimit)
	stderr := newCappedBuffer(runner.stderrLimit)
	cmd := exec.CommandContext(commandContext, runner.executable, invocation.arguments...)
	cmd.Dir = invocation.workingDirectory
	cmd.Env = environmentWithOptionalLocksDisabled(os.Environ())
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = defaultWaitDelay

	err := cmd.Run()
	result := commandResult{stdout: stdout.bytes()}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return commandResult{}, fmt.Errorf("%w: команда %s", ErrCommandTimeout, invocation.name)
		}
		return commandResult{}, fmt.Errorf("%w: команда %s", ErrCommandCanceled, invocation.name)
	}
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return commandResult{}, fmt.Errorf("%w: команда %s", ErrCommandTimeout, invocation.name)
	}
	if stdout.overflow {
		return commandResult{}, fmt.Errorf(
			"%w: команда %s, получено не менее %d байт",
			ErrStdoutLimit,
			invocation.name,
			stdout.total,
		)
	}
	if stderr.overflow {
		return commandResult{}, fmt.Errorf(
			"%w: команда %s, получено не менее %d байт",
			ErrStderrLimit,
			invocation.name,
			stderr.total,
		)
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return result, &CommandExitError{
				Command:     invocation.name,
				ExitCode:    exitError.ExitCode(),
				StdoutBytes: stdout.total,
				StderrBytes: stderr.total,
			}
		}
		return commandResult{}, fmt.Errorf("%w: команда %s: %v", ErrCommandStart, invocation.name, err)
	}
	return result, nil
}

func environmentWithOptionalLocksDisabled(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, variable := range environment {
		if !strings.HasPrefix(variable, "GIT_OPTIONAL_LOCKS=") {
			result = append(result, variable)
		}
	}
	return append(result, "GIT_OPTIONAL_LOCKS=0")
}

type CommandExitError struct {
	Command     string
	ExitCode    int
	StdoutBytes int64
	StderrBytes int64
}

func (err *CommandExitError) Error() string {
	return fmt.Sprintf(
		"%s: команда %s, код %d, stdout %d байт, stderr %d байт",
		ErrCommandExit,
		err.Command,
		err.ExitCode,
		err.StdoutBytes,
		err.StderrBytes,
	)
}

func (err *CommandExitError) Unwrap() error {
	return ErrCommandExit
}

type cappedBuffer struct {
	data     []byte
	limit    int
	total    int64
	overflow bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{data: make([]byte, 0, limit), limit: limit}
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	buffer.total += int64(len(data))
	remaining := buffer.limit - len(buffer.data)
	if remaining > 0 {
		stored := len(data)
		if stored > remaining {
			stored = remaining
		}
		buffer.data = append(buffer.data, data[:stored]...)
	}
	if len(data) > remaining {
		buffer.overflow = true
	}
	return len(data), nil
}

func (buffer *cappedBuffer) bytes() []byte {
	result := make([]byte, len(buffer.data))
	copy(result, buffer.data)
	return result
}
