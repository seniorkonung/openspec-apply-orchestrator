package openspec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	defaultCommandTimeout = 10 * time.Second
	defaultOutputLimit    = 1 << 20
	defaultWaitDelay      = 500 * time.Millisecond
)

var (
	ErrExecutableNotFound = errors.New("исполняемый файл openspec не найден")
	ErrInvalidRunner      = errors.New("некорректная конфигурация исполнителя OpenSpec")
	ErrCommandStart       = errors.New("не удалось запустить команду OpenSpec")
	ErrCommandExit        = errors.New("команда OpenSpec завершилась неуспешно")
	ErrCommandTimeout     = errors.New("истёк срок выполнения команды OpenSpec")
	ErrCommandCanceled    = errors.New("выполнение команды OpenSpec отменено")
	ErrStdoutLimit        = errors.New("stdout команды OpenSpec превысил предел")
	ErrStderrLimit        = errors.New("stderr команды OpenSpec превысил предел")
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
	name string
	args []string
}

type commandResult struct {
	stdout []byte
}

type runner struct {
	executable       string
	workingDirectory string
	timeout          time.Duration
	stdoutLimit      int
	stderrLimit      int
}

func newRunner(workingDirectory string, config runnerConfig) (*runner, error) {
	if config.timeout <= 0 || config.stdoutLimit <= 0 || config.stderrLimit <= 0 {
		return nil, ErrInvalidRunner
	}

	canonicalWorkingDirectory, err := canonicalExistingDirectory(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("%w: рабочий каталог: %v", ErrInvalidRunner, err)
	}
	executable, err := exec.LookPath("openspec")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExecutableNotFound, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("%w: разрешить абсолютный путь: %v", ErrExecutableNotFound, err)
	}

	return &runner{
		executable:       executable,
		workingDirectory: canonicalWorkingDirectory,
		timeout:          config.timeout,
		stdoutLimit:      config.stdoutLimit,
		stderrLimit:      config.stderrLimit,
	}, nil
}

func (runner *runner) run(ctx context.Context, command command) (commandResult, error) {
	if runner == nil || runner.executable == "" || runner.workingDirectory == "" ||
		runner.timeout <= 0 || runner.stdoutLimit <= 0 || runner.stderrLimit <= 0 || ctx == nil {
		return commandResult{}, ErrInvalidRunner
	}

	commandContext, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()

	stdout := newCappedBuffer(runner.stdoutLimit)
	stderr := newCappedBuffer(runner.stderrLimit)
	// Аргументы передаются напрямую без shell; WaitDelay ограничивает ожидание
	// унаследованных дочерними процессами каналов вывода.
	cmd := exec.CommandContext(commandContext, runner.executable, command.args...)
	cmd.Dir = runner.workingDirectory
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = defaultWaitDelay

	err := cmd.Run()
	result := commandResult{stdout: stdout.bytes()}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return commandResult{}, fmt.Errorf("%w: команда %s", ErrCommandTimeout, command.name)
		}
		return commandResult{}, fmt.Errorf("%w: команда %s", ErrCommandCanceled, command.name)
	}
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return commandResult{}, fmt.Errorf("%w: команда %s", ErrCommandTimeout, command.name)
	}
	if stdout.overflow {
		return commandResult{}, fmt.Errorf(
			"%w: команда %s, получено не менее %d байт",
			ErrStdoutLimit,
			command.name,
			stdout.total,
		)
	}
	if stderr.overflow {
		return commandResult{}, fmt.Errorf(
			"%w: команда %s, получено не менее %d байт",
			ErrStderrLimit,
			command.name,
			stderr.total,
		)
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return result, &CommandExitError{
				Command:     command.name,
				ExitCode:    exitError.ExitCode(),
				StdoutBytes: stdout.total,
				StderrBytes: stderr.total,
			}
		}
		return commandResult{}, fmt.Errorf("%w: команда %s: %v", ErrCommandStart, command.name, err)
	}

	return result, nil
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

func canonicalExistingDirectory(value string) (string, error) {
	if value == "" {
		return "", errors.New("путь пуст")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("получить абсолютный путь: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("канонизировать путь: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("прочитать путь: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("путь не является каталогом")
	}
	return filepath.Clean(canonical), nil
}
