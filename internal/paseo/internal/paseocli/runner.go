package paseocli

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

type RunnerConfig struct {
	Timeout     time.Duration
	StdoutLimit int
	StderrLimit int
}

type Invocation struct {
	Name             string
	Arguments        []string
	UnsetEnvironment []string
}

type Adapter struct {
	executable  string
	timeout     time.Duration
	stdoutLimit int
	stderrLimit int
}

func New() (*Adapter, error) {
	return NewWithConfig(RunnerConfig{
		Timeout:     defaultCommandTimeout,
		StdoutLimit: defaultOutputLimit,
		StderrLimit: defaultOutputLimit,
	})
}

func NewWithConfig(config RunnerConfig) (*Adapter, error) {
	if config.Timeout <= 0 || config.StdoutLimit <= 0 || config.StderrLimit <= 0 {
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

	return &Adapter{
		executable:  executable,
		timeout:     config.Timeout,
		stdoutLimit: config.StdoutLimit,
		stderrLimit: config.StderrLimit,
	}, nil
}

func (adapter *Adapter) Run(ctx context.Context, invocation Invocation) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: отсутствует контекст", ErrInvalidRunnerConfig)
	}

	commandCtx, cancel := context.WithTimeout(ctx, adapter.timeout)
	defer cancel()
	return adapter.runWithContext(ctx, commandCtx, invocation)
}

func (adapter *Adapter) RunUntilContextDone(
	ctx context.Context,
	invocation Invocation,
) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: отсутствует контекст", ErrInvalidRunnerConfig)
	}

	return adapter.runWithContext(ctx, ctx, invocation)
}

func (adapter *Adapter) runWithContext(
	callerCtx context.Context,
	commandCtx context.Context,
	invocation Invocation,
) ([]byte, error) {
	stdout := newCappedBuffer(adapter.stdoutLimit)
	stderr := newCappedBuffer(adapter.stderrLimit)
	// CommandContext передаёт аргументы без shell и отменяет только дочерний процесс;
	// WaitDelay ограничивает ожидание унаследованных каналов вывода.
	// Источник: https://pkg.go.dev/os/exec#CommandContext
	cmd := exec.CommandContext(commandCtx, adapter.executable, invocation.Arguments...)
	if len(invocation.UnsetEnvironment) > 0 {
		cmd.Env = environmentWithout(os.Environ(), invocation.UnsetEnvironment)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = defaultWaitDelay

	err := cmd.Run()
	if callerCtx.Err() != nil {
		if errors.Is(callerCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: команда %s", ErrCommandTimeout, invocation.Name)
		}
		return nil, fmt.Errorf("%w: команда %s", ErrCommandCanceled, invocation.Name)
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: команда %s", ErrCommandTimeout, invocation.Name)
	}
	if stdout.overflow {
		return nil, fmt.Errorf(
			"%w: команда %s, получено не менее %d байт",
			ErrStdoutLimit,
			invocation.Name,
			stdout.total,
		)
	}
	if stderr.overflow {
		return nil, fmt.Errorf(
			"%w: команда %s, получено не менее %d байт",
			ErrStderrLimit,
			invocation.Name,
			stderr.total,
		)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &CommandExitError{
				Command:     invocation.Name,
				ExitCode:    exitErr.ExitCode(),
				StdoutBytes: stdout.total,
				StderrBytes: stderr.total,
			}
		}
		return nil, fmt.Errorf("%w: команда %s: %v", ErrCommandStart, invocation.Name, err)
	}

	return stdout.bytes(), nil
}

func environmentWithout(environment, keys []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		remove := false
		for _, key := range keys {
			if strings.HasPrefix(entry, key+"=") {
				remove = true
				break
			}
		}
		if !remove {
			filtered = append(filtered, entry)
		}
	}
	return filtered
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
