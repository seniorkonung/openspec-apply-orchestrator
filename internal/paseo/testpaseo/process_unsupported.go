//go:build paseo_integration && !linux

package testpaseo

import (
	"os"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) error {
	return nil
}

func signalProcessGroup(processGroupID int, signal os.Signal) error {
	process, err := os.FindProcess(processGroupID)
	if err != nil {
		return err
	}
	if err := process.Signal(signal); err != nil && err != os.ErrProcessDone {
		return err
	}
	return nil
}

func processGroupAlive(_ int) (bool, error) {
	return false, nil
}
