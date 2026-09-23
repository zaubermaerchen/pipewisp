//go:build windows

package pipewisp

// This file configures Windows observation pipes for tests.

import (
	"golang.org/x/sys/windows"
	"os"
)

func setTestEventNonblocking(file *os.File) error {
	mode := uint32(windows.PIPE_NOWAIT)
	return windows.SetNamedPipeHandleState(windows.Handle(file.Fd()), &mode, nil, nil)
}
