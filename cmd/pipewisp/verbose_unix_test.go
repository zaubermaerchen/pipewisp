//go:build !windows

package main

// This file verifies Unix-specific signal records for verbose diagnostics.

import (
	"bytes"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestVerboseCanonicalUnixSignals(t *testing.T) {
	tests := []struct {
		signal syscall.Signal
		want   string
	}{
		{signal: syscall.SIGINT, want: "SIGINT"},
		{signal: syscall.SIGTERM, want: "SIGTERM"},
		{signal: syscall.SIGHUP, want: "SIGHUP"},
		{signal: syscall.SIGPIPE, want: "SIGPIPE"},
		{signal: syscall.SIGKILL, want: "SIGKILL"},
		{signal: syscall.Signal(64), want: "unknown signal_number=64"},
	}
	for _, tt := range tests {
		if got := canonicalSignal(tt.signal); got != tt.want {
			t.Errorf("canonicalSignal(%d) = %q, want %q", tt.signal, got, tt.want)
		}
	}
}

func TestVerboseUnknownInterruptedSignalFieldOrder(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(&diagnostics, true)
	reporter.hookEnd("idle", time.Now(), &hookSignalError{signal: syscall.Signal(64)})
	got := diagnostics.String()
	if !strings.HasPrefix(got, "pipewisp: type=hook event=idle state=interrupted signal=unknown signal_number=64 duration_ms=") {
		t.Fatalf("diagnostics = %q", got)
	}
}

func TestVerboseIndependentSIGPIPEIsExitSignal(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(&diagnostics, true)
	if err := runHookWithContext("on-ready", "kill -PIPE $$", hookContext{event: "ready"}, reporter); err == nil {
		t.Fatal("SIGPIPE hook unexpectedly succeeded")
	}
	got := diagnostics.String()
	if !strings.Contains(got, "state=exit signal=SIGPIPE duration_ms=") {
		t.Fatalf("diagnostics = %q", got)
	}
}

func TestVerboseIndependentSIGKILLIsExitSignal(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(&diagnostics, true)
	if err := runHookWithContext("on-ready", "kill -KILL $$", hookContext{event: "ready"}, reporter); err == nil {
		t.Fatal("SIGKILL hook unexpectedly succeeded")
	}
	got := diagnostics.String()
	if !strings.Contains(got, "state=exit signal=SIGKILL duration_ms=") {
		t.Fatalf("diagnostics = %q", got)
	}
}
