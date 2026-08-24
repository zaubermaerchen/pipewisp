//go:build aix

package main

// This file fixes AIX-specific signal aliases and sentinel handling.

import (
	"syscall"
	"testing"
)

func TestAIXSignalNames(t *testing.T) {
	if got, want := len(unixSignalNames), 46; got != want {
		t.Fatalf("unixSignalNames has %d entries, want %d", got, want)
	}
	for _, tt := range []signalNameExpectation{
		{signal: syscall.Signal(27), name: "SIGMSG"},
		{signal: syscall.Signal(38), name: "SIGTALRM"},
		{signal: syscall.Signal(63), name: "SIGSAK"},
	} {
		if got := canonicalSignal(tt.signal); got != tt.name {
			t.Errorf("canonicalSignal(%d) = %q, want %q", tt.signal, got, tt.name)
		}
	}
	if got, want := canonicalSignal(syscall.Signal(255)), "unknown signal_number=255"; got != want {
		t.Errorf("canonicalSignal(255) = %q, want %q", got, want)
	}
}
