//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package update

import "syscall"

func runtimeHandoffSupported() bool { return true }

func replaceCurrentProcess(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
