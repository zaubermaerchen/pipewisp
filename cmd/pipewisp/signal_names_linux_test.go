//go:build linux || android

package main

// This file fixes the complete Linux signal-name table used by diagnostics.

import (
	"runtime"
	"syscall"
	"testing"
)

func TestLinuxSignalNameTable(t *testing.T) {
	expected := []signalNameExpectation{
		{syscall.SIGHUP, "SIGHUP"},
		{syscall.SIGINT, "SIGINT"},
		{syscall.SIGQUIT, "SIGQUIT"},
		{syscall.SIGILL, "SIGILL"},
		{syscall.SIGTRAP, "SIGTRAP"},
		{syscall.SIGABRT, "SIGABRT"},
		{syscall.SIGBUS, "SIGBUS"},
		{syscall.SIGFPE, "SIGFPE"},
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGUSR1, "SIGUSR1"},
		{syscall.SIGSEGV, "SIGSEGV"},
		{syscall.SIGUSR2, "SIGUSR2"},
		{syscall.SIGPIPE, "SIGPIPE"},
		{syscall.SIGALRM, "SIGALRM"},
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGCHLD, "SIGCHLD"},
		{syscall.SIGCONT, "SIGCONT"},
		{syscall.SIGSTOP, "SIGSTOP"},
		{syscall.SIGTSTP, "SIGTSTP"},
		{syscall.SIGTTIN, "SIGTTIN"},
		{syscall.SIGTTOU, "SIGTTOU"},
		{syscall.SIGURG, "SIGURG"},
		{syscall.SIGXCPU, "SIGXCPU"},
		{syscall.SIGXFSZ, "SIGXFSZ"},
		{syscall.SIGVTALRM, "SIGVTALRM"},
		{syscall.SIGPROF, "SIGPROF"},
		{syscall.SIGWINCH, "SIGWINCH"},
		{syscall.SIGIO, "SIGIO"},
		{syscall.SIGPWR, "SIGPWR"},
		{syscall.SIGSYS, "SIGSYS"},
	}
	if runtime.GOARCH != "mips" && runtime.GOARCH != "mipsle" && runtime.GOARCH != "mips64" && runtime.GOARCH != "mips64le" {
		expected = append(expected, signalNameExpectation{signal: syscall.Signal(16), name: "SIGSTKFLT"})
	} else {
		expected = append(expected, signalNameExpectation{signal: syscall.Signal(7), name: "SIGEMT"})
	}
	assertSignalNameTable(t, expected)
}
