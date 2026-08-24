//go:build windows

package main

// This file normalizes Windows interrupt names for verbose diagnostics.

import (
	"os"
	"os/exec"
)

func canonicalSignal(signal os.Signal) string {
	if signal == os.Interrupt {
		return "SIGINT"
	}
	return "unknown"
}

func hookExitSignal(*exec.ExitError) (os.Signal, bool)      { return nil, false }
func processStateSignal(*os.ProcessState) (os.Signal, bool) { return nil, false }
func processStateKilledByPipewisp(*os.ProcessState) bool    { return true }
