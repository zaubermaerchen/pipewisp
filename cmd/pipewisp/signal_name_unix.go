//go:build !windows

package main

// This file normalizes Unix signal names for verbose diagnostics.

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func canonicalSignal(signal os.Signal) string {
	if number, ok := signal.(syscall.Signal); ok {
		if name, ok := unixSignalNames[number]; ok {
			return name
		}
		return "unknown signal_number=" + strconv.Itoa(int(number))
	}
	return "unknown"
}

func hookExitSignal(err *exec.ExitError) (os.Signal, bool) {
	status, ok := err.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return nil, false
	}
	return syscall.Signal(status.Signal()), true
}

func processStateSignal(state *os.ProcessState) (os.Signal, bool) {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return nil, false
	}
	return syscall.Signal(status.Signal()), true
}

func processStateKilledByPipewisp(state *os.ProcessState) bool {
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}
