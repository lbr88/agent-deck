//go:build windows

package update

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockSelfUpdateFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&overlapped,
	)
}

func unlockSelfUpdateFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func atomicReplaceSelfUpdateFile(source, target string) error {
	sourceUTF16, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourceUTF16,
		targetUTF16,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

// Windows does not expose a generally useful directory handle fsync. File
// contents are flushed explicitly and MoveFileEx uses WRITE_THROUGH instead.
func syncSelfUpdateDirectory(string) error { return nil }
