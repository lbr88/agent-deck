//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package update

import (
	"fmt"
	"runtime"
)

func runtimeHandoffSupported() bool { return false }

func replaceCurrentProcess(_ string, _, _ []string) error {
	return fmt.Errorf("%w on %s", ErrExecUnsupported, runtime.GOOS)
}
