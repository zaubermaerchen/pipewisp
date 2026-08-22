//go:build windows

package main

// This file configures Windows interrupt handling and broken-pipe classification.

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

const windowsErrorNoData = syscall.Errno(0xe8) // ERROR_NO_DATA (232): The pipe is being closed.

// pipewisp uses POSIX-compatible signal statuses for its cross-platform CLI contract.
const signalExitCodeBase = 128

func subscribePassthroughSignals() (*signalTracker, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	return &signalTracker{signals: signals}, func() {
		signal.Stop(signals)
	}
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windowsErrorNoData) ||
		errors.Is(err, syscall.EPIPE)
}

func signalExitCode(sig os.Signal) int {
	if sig == os.Interrupt {
		return signalExitCodeBase + int(syscall.SIGINT)
	}
	return 1
}
