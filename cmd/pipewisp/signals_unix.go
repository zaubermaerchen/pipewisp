//go:build !windows

package main

// This file configures Unix signal handling and pipe-error classification.

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// Shells conventionally report signal termination as 128 plus the signal number.
const signalExitCodeBase = 128

func subscribePassthroughSignals() (*signalTracker, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	sigpipe := make(chan os.Signal, 1)
	// Notify keeps parent writes observable as EPIPE while allowing hook execs to reset SIGPIPE to default.
	signal.Notify(sigpipe, syscall.SIGPIPE)
	return &signalTracker{signals: signals}, func() {
		signal.Stop(signals)
		signal.Stop(sigpipe)
	}
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}

func signalExitCode(sig os.Signal) int {
	switch sig {
	case os.Interrupt:
		return signalExitCodeBase + int(syscall.SIGINT)
	case syscall.SIGTERM:
		return signalExitCodeBase + int(syscall.SIGTERM)
	case syscall.SIGHUP:
		return signalExitCodeBase + int(syscall.SIGHUP)
	default:
		return 1
	}
}
