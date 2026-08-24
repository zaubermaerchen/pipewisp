//go:build (linux || android) && (mips || mipsle || mips64 || mips64le)

package main

// This file adds Linux MIPS's architecture-dependent SIGEMT entry.

import "syscall"

func init() {
	unixSignalNames[syscall.SIGEMT] = "SIGEMT"
}
