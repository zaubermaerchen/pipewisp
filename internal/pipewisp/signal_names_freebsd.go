//go:build freebsd

package pipewisp

// This file adds FreeBSD-specific signal names to the common BSD table.

import "syscall"

func init() {
	unixSignalNames[syscall.SIGTHR] = "SIGTHR"
	unixSignalNames[syscall.SIGLIBRT] = "SIGLIBRT"
}
