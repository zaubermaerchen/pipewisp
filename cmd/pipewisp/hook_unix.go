//go:build !windows

package main

// This file selects /bin/sh as the Unix hook interpreter.

import "os/exec"

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}
