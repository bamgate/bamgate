//go:build !windows

package main

import "syscall"

// syscallExec replaces the current process with the given command.
// On Unix systems this uses the execve(2) syscall.
func syscallExec(argv0 string, argv []string, envv []string) error {
	return syscall.Exec(argv0, argv, envv)
}
