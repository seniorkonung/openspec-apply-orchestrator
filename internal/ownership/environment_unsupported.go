//go:build !linux

package ownership

import (
	"fmt"
	"runtime"
)

func CheckLocalEnvironment(_, _ string) (LocalEnvironment, error) {
	return LocalEnvironment{}, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, runtime.GOOS)
}
