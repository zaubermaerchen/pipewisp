//go:build solaris || illumos

package main

// This file fixes Solaris and illumos signal names outside the POSIX range.

import (
	"syscall"
	"testing"
)

func TestSolarisSignalNames(t *testing.T) {
	if got, want := len(unixSignalNames), 41; got != want {
		t.Fatalf("unixSignalNames has %d entries, want %d", got, want)
	}
	for _, tt := range []signalNameExpectation{
		{signal: syscall.Signal(32), name: "SIGWAITING"},
		{signal: syscall.Signal(40), name: "SIGJVM2"},
		{signal: syscall.Signal(41), name: "SIGINFO"},
	} {
		if got := canonicalSignal(tt.signal); got != tt.name {
			t.Errorf("canonicalSignal(%d) = %q, want %q", tt.signal, got, tt.name)
		}
	}
	if got, want := canonicalSignal(syscall.Signal(255)), "unknown signal_number=255"; got != want {
		t.Errorf("canonicalSignal(255) = %q, want %q", got, want)
	}
}
