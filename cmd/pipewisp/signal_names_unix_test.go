//go:build !windows

package main

// This file provides shared assertions for Unix signal-name tables.

import (
	"syscall"
	"testing"
)

type signalNameExpectation struct {
	signal syscall.Signal
	name   string
}

func assertSignalNameTable(t *testing.T, expected []signalNameExpectation) {
	t.Helper()
	if len(unixSignalNames) != len(expected) {
		t.Fatalf("unixSignalNames has %d entries, want %d", len(unixSignalNames), len(expected))
	}

	expectedBySignal := make(map[syscall.Signal]string, len(expected))
	for _, want := range expected {
		expectedBySignal[want.signal] = want.name
		if got := canonicalSignal(want.signal); got != want.name {
			t.Errorf("canonicalSignal(%d) = %q, want %q", want.signal, got, want.name)
		}
	}
	for signal := range unixSignalNames {
		if _, ok := expectedBySignal[signal]; !ok {
			t.Errorf("unixSignalNames[%d] is not expected", signal)
		}
	}
}
