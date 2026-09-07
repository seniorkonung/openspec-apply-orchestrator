package paseo

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	defaultCommandTimeout = 10 * time.Second
	defaultOutputLimit    = 1 << 20
	defaultWaitDelay      = 500 * time.Millisecond
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

type runner struct {
	executable  string
	timeout     time.Duration
	stdoutLimit int
	stderrLimit int
}

func newRunner(config runnerConfig) (*runner, error) {
	if config.timeout <= 0 || config.stdoutLimit <= 0 || config.stderrLimit <= 0 {
		return nil, ErrInvalidRunnerConfig
	}

	executable, err := exec.LookPath("paseo")
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

func (runner *runner) run(ctx context.Context, command command) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: отсутствует контекст", ErrInvalidRunnerConfig)
	}

	commandCtx, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()

	stdout := newCappedBuffer(runner.stdoutLimit)
	stderr := newCappedBuffer(runner.stderrLimit)
	cmd := exec.CommandContext(commandCtx, runner.executable, command.args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = defaultWaitDelay

	err := cmd.Run()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: команда %s", ErrCommandTimeout, command.name)
		}
		return nil, fmt.Errorf("%w: команда %s", ErrCommandCanceled, command.name)
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: команда %s", ErrCommandTimeout, command.name)
	}
	if stdout.overflow {
		return nil, fmt.Errorf("%w: команда %s, получено не менее %d байт", ErrStdoutLimit, command.name, stdout.total)
	}
	if stderr.overflow {
		return nil, fmt.Errorf("%w: команда %s, получено не менее %d байт", ErrStderrLimit, command.name, stderr.total)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &CommandExitError{
				Command:     command.name,
				ExitCode:    exitErr.ExitCode(),
				StdoutBytes: stdout.total,
				StderrBytes: stderr.total,
			}
		}
		return nil, fmt.Errorf("%w: команда %s: %v", ErrCommandStart, command.name, err)
	}

	return stdout.bytes(), nil
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
