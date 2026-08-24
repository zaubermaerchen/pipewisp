//go:build dragonfly

package main

// This file adds DragonFly BSD-specific signal names to the common BSD table.

import "syscall"

func init() {
	unixSignalNames[syscall.SIGTHR] = "SIGTHR"
	unixSignalNames[syscall.SIGCKPT] = "SIGCKPT"
	unixSignalNames[syscall.SIGCKPTEXIT] = "SIGCKPTEXIT"
}
