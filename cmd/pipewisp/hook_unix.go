//go:build !windows

package main

import "os/exec"

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}
