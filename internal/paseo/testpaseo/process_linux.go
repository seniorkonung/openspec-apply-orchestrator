//go:build paseo_integration && linux

package testpaseo

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(command *exec.Cmd) error {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	if command.SysProcAttr.Setpgid {
		return nil
	}
	command.SysProcAttr.Setpgid = true
	return nil
}

func signalProcessGroup(processGroupID int, signal os.Signal) error {
	systemSignal, ok := signal.(syscall.Signal)
	if !ok {
		return errors.New("сигнал не поддерживается Linux process group")
	}
	if err := syscall.Kill(-processGroupID, systemSignal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func processGroupAlive(processGroupID int) (bool, error) {
	err := syscall.Kill(-processGroupID, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
