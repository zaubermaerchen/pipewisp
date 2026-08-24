//go:build netbsd

package main

// This file lists NetBSD signal names used by verbose diagnostics.

import "syscall"

var unixSignalNames = map[syscall.Signal]string{
	syscall.SIGHUP:    "SIGHUP",
	syscall.SIGINT:    "SIGINT",
	syscall.SIGQUIT:   "SIGQUIT",
	syscall.SIGILL:    "SIGILL",
	syscall.SIGTRAP:   "SIGTRAP",
	syscall.SIGABRT:   "SIGABRT",
	syscall.SIGEMT:    "SIGEMT",
	syscall.SIGFPE:    "SIGFPE",
	syscall.SIGKILL:   "SIGKILL",
	syscall.SIGBUS:    "SIGBUS",
	syscall.SIGSEGV:   "SIGSEGV",
	syscall.SIGSYS:    "SIGSYS",
	syscall.SIGPIPE:   "SIGPIPE",
	syscall.SIGALRM:   "SIGALRM",
	syscall.SIGTERM:   "SIGTERM",
	syscall.SIGURG:    "SIGURG",
	syscall.SIGSTOP:   "SIGSTOP",
	syscall.SIGTSTP:   "SIGTSTP",
	syscall.SIGCONT:   "SIGCONT",
	syscall.SIGCHLD:   "SIGCHLD",
	syscall.SIGTTIN:   "SIGTTIN",
	syscall.SIGTTOU:   "SIGTTOU",
	syscall.SIGIO:     "SIGIO",
	syscall.SIGXCPU:   "SIGXCPU",
	syscall.SIGXFSZ:   "SIGXFSZ",
	syscall.SIGVTALRM: "SIGVTALRM",
	syscall.SIGPROF:   "SIGPROF",
	syscall.SIGWINCH:  "SIGWINCH",
	syscall.SIGINFO:   "SIGINFO",
	syscall.SIGUSR1:   "SIGUSR1",
	syscall.SIGUSR2:   "SIGUSR2",
	syscall.SIGPWR:    "SIGPWR",
}
