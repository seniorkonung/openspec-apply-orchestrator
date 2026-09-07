//go:build linux

package ownership

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
)

type ChangeLock struct {
	directory   *os.File
	releaseOnce sync.Once
	releaseErr  error
}

func (environment LocalEnvironment) AcquireChangeLock() (*ChangeLock, error) {
	if environment.changeRoot == "" || environment.changeRootInfo == nil {
		return nil, fmt.Errorf("%w: локальная среда не проверена", ErrInvalidRoot)
	}

	directory, err := os.Open(environment.changeRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: открыть корень change %q: %v", ErrChangeLock, environment.changeRoot, err)
	}
	info, err := directory.Stat()
	if err != nil {
		directory.Close()
		return nil, fmt.Errorf("%w: проверить открытый корень change %q: %v", ErrChangeLock, environment.changeRoot, err)
	}
	if !info.IsDir() || !os.SameFile(environment.changeRootInfo, info) {
		directory.Close()
		return nil, fmt.Errorf("%w: %q", ErrChangeRootChanged, environment.changeRoot)
	}

	var stats syscall.Statfs_t
	if err := syscall.Fstatfs(int(directory.Fd()), &stats); err != nil {
		directory.Close()
		return nil, fmt.Errorf("%w: корень change %q: %v", ErrFilesystemInspection, environment.changeRoot, err)
	}
	if err := validateFilesystem("корень change", environment.changeRoot, filesystemMagic(stats.Type)); err != nil {
		directory.Close()
		return nil, err
	}

	if err := syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		directory.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %q", ErrChangeBusy, environment.changeRoot)
		}
		return nil, fmt.Errorf("%w: %q: %v", ErrChangeLock, environment.changeRoot, err)
	}
	return &ChangeLock{directory: directory}, nil
}

func (lock *ChangeLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.releaseOnce.Do(func() {
		lock.releaseErr = lock.directory.Close()
	})
	return lock.releaseErr
}
