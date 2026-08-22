//go:build windows

package main

// This file selects cmd.exe as the Windows hook interpreter.

import "os/exec"

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("cmd.exe", "/C", command)
}
