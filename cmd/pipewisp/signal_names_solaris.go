//go:build solaris || illumos

package main

// This file lists Solaris and illumos signal names used by verbose diagnostics.

import "syscall"

var unixSignalNames = map[syscall.Signal]string{
	syscall.SIGHUP:     "SIGHUP",
	syscall.SIGINT:     "SIGINT",
	syscall.SIGQUIT:    "SIGQUIT",
	syscall.SIGILL:     "SIGILL",
	syscall.SIGTRAP:    "SIGTRAP",
	syscall.SIGABRT:    "SIGABRT",
	syscall.SIGEMT:     "SIGEMT",
	syscall.SIGFPE:     "SIGFPE",
	syscall.SIGKILL:    "SIGKILL",
	syscall.SIGBUS:     "SIGBUS",
	syscall.SIGSEGV:    "SIGSEGV",
	syscall.SIGSYS:     "SIGSYS",
	syscall.SIGPIPE:    "SIGPIPE",
	syscall.SIGALRM:    "SIGALRM",
	syscall.SIGTERM:    "SIGTERM",
	syscall.SIGUSR1:    "SIGUSR1",
	syscall.SIGUSR2:    "SIGUSR2",
	syscall.SIGCHLD:    "SIGCHLD",
	syscall.SIGPWR:     "SIGPWR",
	syscall.SIGWINCH:   "SIGWINCH",
	syscall.SIGURG:     "SIGURG",
	syscall.SIGIO:      "SIGIO",
	syscall.SIGSTOP:    "SIGSTOP",
	syscall.SIGTSTP:    "SIGTSTP",
	syscall.SIGCONT:    "SIGCONT",
	syscall.SIGTTIN:    "SIGTTIN",
	syscall.SIGTTOU:    "SIGTTOU",
	syscall.SIGVTALRM:  "SIGVTALRM",
	syscall.SIGPROF:    "SIGPROF",
	syscall.SIGXCPU:    "SIGXCPU",
	syscall.SIGXFSZ:    "SIGXFSZ",
	syscall.SIGWAITING: "SIGWAITING",
	syscall.SIGLWP:     "SIGLWP",
	syscall.SIGFREEZE:  "SIGFREEZE",
	syscall.SIGTHAW:    "SIGTHAW",
	syscall.SIGCANCEL:  "SIGCANCEL",
	syscall.SIGLOST:    "SIGLOST",
	syscall.SIGXRES:    "SIGXRES",
	syscall.SIGJVM1:    "SIGJVM1",
	syscall.SIGJVM2:    "SIGJVM2",
	syscall.Signal(41): "SIGINFO",
}
