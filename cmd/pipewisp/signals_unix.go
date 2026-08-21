//go:build !windows

package main

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

func subscribePassthroughSignals() (chan os.Signal, func()) {
	// Without this, a closed downstream pipe can terminate the process before
	// io.Copy can report syscall.EPIPE to the lifecycle coordinator.
	signal.Ignore(syscall.SIGPIPE)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals, func() {
		signal.Stop(signals)
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
