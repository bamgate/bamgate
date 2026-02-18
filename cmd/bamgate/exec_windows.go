//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// syscallExec on Windows falls back to running the command as a child process,
// since Windows does not support the Unix execve(2) syscall.
func syscallExec(argv0 string, argv []string, envv []string) error {
	cmd := exec.Command(argv0, argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = envv
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", argv0, err)
	}
	return nil
}
