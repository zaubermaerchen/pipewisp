//go:build openbsd

package pipewisp

// This file adds OpenBSD-specific signal names to the common BSD table.

import "syscall"

func init() {
	unixSignalNames[syscall.SIGTHR] = "SIGTHR"
}
