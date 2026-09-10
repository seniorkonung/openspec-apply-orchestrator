//go:build paseo_integration

package testpaseo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

const processTerminationGrace = 250 * time.Millisecond

type OwnedProcess struct {
	processGroupID int
	done           chan struct{}

	mutex   sync.Mutex
	waitErr error
}

func StartOwnedProcess(command *exec.Cmd) (*OwnedProcess, error) {
	if command == nil {
		return nil, errors.New("команда процесса не задана")
	}
	if command.Process != nil {
		return nil, errors.New("команда процесса уже запущена")
	}
	if err := configureProcessGroup(command); err != nil {
		return nil, fmt.Errorf("подготовить отдельную группу процесса: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	process := &OwnedProcess{
		processGroupID: command.Process.Pid,
		done:           make(chan struct{}),
	}
	go func() {
		err := command.Wait()
		process.mutex.Lock()
		process.waitErr = err
		process.mutex.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (process *OwnedProcess) Done() <-chan struct{} {
	return process.done
}

func (process *OwnedProcess) WaitError() error {
	<-process.done
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return process.waitErr
}

func (process *OwnedProcess) Signal(signal os.Signal) error {
	if process == nil {
		return nil
	}
	return signalProcessGroup(process.processGroupID, signal)
}

func (process *OwnedProcess) TerminateAndWait(ctx context.Context) error {
	if process == nil {
		return nil
	}
	select {
	case <-process.done:
	default:
		if err := process.Signal(os.Interrupt); err != nil {
			return fmt.Errorf("прервать группу процесса: %w", err)
		}
		select {
		case <-process.done:
		case <-time.After(processTerminationGrace):
		case <-ctx.Done():
		}
	}

	alive, err := processGroupAlive(process.processGroupID)
	if err != nil {
		return fmt.Errorf("проверить группу процесса: %w", err)
	}
	if alive {
		if err := signalProcessGroup(process.processGroupID, os.Kill); err != nil {
			return fmt.Errorf("завершить группу процесса: %w", err)
		}
	}

	select {
	case <-process.done:
	case <-ctx.Done():
		return fmt.Errorf("дождаться процесса: %w", ctx.Err())
	}
	for {
		alive, err = processGroupAlive(process.processGroupID)
		if err != nil {
			return fmt.Errorf("подтвердить завершение группы процесса: %w", err)
		}
		if !alive {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("дождаться потомков процесса: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func RunOwnedCommand(ctx context.Context, command *exec.Cmd) error {
	return runOwnedCommand(ctx, command, false)
}

func runOwnedCommand(ctx context.Context, command *exec.Cmd, preserveDescendants bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	process, err := StartOwnedProcess(command)
	if err != nil {
		return err
	}
	select {
	case <-process.Done():
		waitErr := process.WaitError()
		if preserveDescendants {
			return waitErr
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return errors.Join(waitErr, process.TerminateAndWait(cleanupContext))
	case <-ctx.Done():
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return errors.Join(ctx.Err(), process.TerminateAndWait(cleanupContext))
	}
}
