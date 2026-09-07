//go:build !linux

package ownership

import (
	"fmt"
	"runtime"
)

type ChangeLock struct{}

func (LocalEnvironment) AcquireChangeLock() (*ChangeLock, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, runtime.GOOS)
}

func (*ChangeLock) Close() error {
	return nil
}
