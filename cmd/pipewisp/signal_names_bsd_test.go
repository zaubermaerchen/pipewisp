//go:build freebsd || openbsd || dragonfly

package main

// This file fixes the signal-name table for supported BSD targets.

import (
	"runtime"
	"syscall"
	"testing"
)

func TestBSDSignalNameTable(t *testing.T) {
	expected := []signalNameExpectation{
		{syscall.SIGHUP, "SIGHUP"},
		{syscall.SIGINT, "SIGINT"},
		{syscall.SIGQUIT, "SIGQUIT"},
		{syscall.SIGILL, "SIGILL"},
		{syscall.SIGTRAP, "SIGTRAP"},
		{syscall.SIGABRT, "SIGABRT"},
		{syscall.SIGEMT, "SIGEMT"},
		{syscall.SIGFPE, "SIGFPE"},
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGBUS, "SIGBUS"},
		{syscall.SIGSEGV, "SIGSEGV"},
		{syscall.SIGSYS, "SIGSYS"},
		{syscall.SIGPIPE, "SIGPIPE"},
		{syscall.SIGALRM, "SIGALRM"},
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGURG, "SIGURG"},
		{syscall.SIGSTOP, "SIGSTOP"},
		{syscall.SIGTSTP, "SIGTSTP"},
		{syscall.SIGCONT, "SIGCONT"},
		{syscall.SIGCHLD, "SIGCHLD"},
		{syscall.SIGTTIN, "SIGTTIN"},
		{syscall.SIGTTOU, "SIGTTOU"},
		{syscall.SIGIO, "SIGIO"},
		{syscall.SIGXCPU, "SIGXCPU"},
		{syscall.SIGXFSZ, "SIGXFSZ"},
		{syscall.SIGVTALRM, "SIGVTALRM"},
		{syscall.SIGPROF, "SIGPROF"},
		{syscall.SIGWINCH, "SIGWINCH"},
		{syscall.SIGINFO, "SIGINFO"},
		{syscall.SIGUSR1, "SIGUSR1"},
		{syscall.SIGUSR2, "SIGUSR2"},
	}
	switch runtime.GOOS {
	case "freebsd":
		expected = append(expected,
			signalNameExpectation{signal: syscall.Signal(32), name: "SIGTHR"},
			signalNameExpectation{signal: syscall.Signal(33), name: "SIGLIBRT"},
		)
	case "openbsd":
		expected = append(expected, signalNameExpectation{signal: syscall.Signal(32), name: "SIGTHR"})
	case "dragonfly":
		expected = append(expected,
			signalNameExpectation{signal: syscall.Signal(32), name: "SIGTHR"},
			signalNameExpectation{signal: syscall.Signal(33), name: "SIGCKPT"},
			signalNameExpectation{signal: syscall.Signal(34), name: "SIGCKPTEXIT"},
		)
	}
	assertSignalNameTable(t, expected)
}
