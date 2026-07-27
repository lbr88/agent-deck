//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package update

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockSelfUpdateFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX)
}

func unlockSelfUpdateFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func atomicReplaceSelfUpdateFile(source, target string) error {
	return os.Rename(source, target)
}

func syncSelfUpdateDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		// A few otherwise usable filesystems reject fsync on directories. The
		// staged file itself was still fsync'd, so degrade only for those known
		// unsupported-operation results.
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
			return nil
		}
		return err
	}
	return nil
}
