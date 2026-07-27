//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package update

import (
	"fmt"
	"os"
	"runtime"
)

// Fail closed on unimplemented platforms instead of silently running without
// the cross-process serialization required by the installer transaction.
func lockSelfUpdateFile(*os.File) error {
	return fmt.Errorf("atomic self-update locking is not supported on %s", runtime.GOOS)
}

func unlockSelfUpdateFile(*os.File) error { return nil }

func atomicReplaceSelfUpdateFile(source, target string) error {
	return os.Rename(source, target)
}

func syncSelfUpdateDirectory(string) error { return nil }
