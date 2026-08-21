//go:build !windows

package main

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

func subscribePassthroughSignals() (*signalTracker, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	sigpipe := make(chan os.Signal, 1)
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
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return 1
	}
}
