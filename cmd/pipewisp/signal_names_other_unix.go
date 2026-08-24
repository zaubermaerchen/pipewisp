//go:build !windows && !linux && !android && !darwin && !ios && !freebsd && !openbsd && !netbsd && !dragonfly && !solaris && !illumos && !aix

package main

// This file provides a conservative fallback for Unix targets without a dedicated signal table.

import "syscall"

var unixSignalNames = map[syscall.Signal]string{
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGILL:  "SIGILL",
	syscall.SIGTRAP: "SIGTRAP",
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGFPE:  "SIGFPE",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGTERM: "SIGTERM",
}
