//go:build windows

package main

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

const windowsErrorNoData = syscall.Errno(0xe8)

func subscribePassthroughSignals() (chan os.Signal, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	return signals, func() {
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
		return 130
	}
	return 1
}
