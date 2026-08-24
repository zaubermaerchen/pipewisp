//go:build (linux || android) && (386 || amd64 || arm || arm64 || loong64 || ppc64 || ppc64le || riscv64 || s390x)

package main

// This file adds Linux's architecture-dependent SIGSTKFLT entry where the
// target syscall package exposes that signal.

import "syscall"

func init() {
	unixSignalNames[syscall.SIGSTKFLT] = "SIGSTKFLT"
}
