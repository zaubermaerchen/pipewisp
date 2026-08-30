//go:build !windows

package main

// This file tests Unix-specific copy-error classification and signal status values.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClassifyCompletionRecognizesBrokenPipe(t *testing.T) {
	for _, err := range []error{syscall.EPIPE, errors.Join(errors.New("wrapped"), syscall.EPIPE)} {
		if got := classifyCompletion(completion{copyErr: err}); got != completionBrokenPipe {
			t.Errorf("classifyCompletion(%v) = %v, want %v", err, got, completionBrokenPipe)
		}
	}
}

func TestFirstDataHookFailureWinsBrokenPipe(t *testing.T) {
	var diagnostics bytes.Buffer
	config := options{onFirstData: failingHookCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, strings.NewReader("input"), errorWriter{err: syscall.EPIPE}, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if got := diagnostics.String(); !strings.Contains(got, "on-first-data hook failed") {
		t.Fatalf("diagnostics = %q, want first-data hook failure", got)
	}
}

func TestSignalExitCodeUnix(t *testing.T) {
	for _, tt := range []struct {
		signal syscall.Signal
		status int
	}{
		{signal: syscall.SIGTERM, status: 143},
		{signal: syscall.SIGHUP, status: 129},
	} {
		if got := signalExitCode(tt.signal); got != tt.status {
			t.Errorf("signalExitCode(%v) = %d, want %d", tt.signal, got, tt.status)
		}
	}
}

func TestFinishCompletionSIGHUPStatusWinsShutdownFailure(t *testing.T) {
	var diagnostics bytes.Buffer
	status := finishCompletion(completion{signal: syscall.SIGHUP}, func() error {
		err := errors.New("shutdown failed")
		reportDiagnostic(&diagnostics, err)
		return err
	}, &diagnostics)

	if status != 129 {
		t.Fatalf("finishCompletion() status = %d, want 129", status)
	}
	if diagnostics.Len() == 0 {
		t.Fatal("finishCompletion() diagnostics are empty, want shutdown failure")
	}
}

func TestFinishCompletionRecordsSIGHUPDuringShutdown(t *testing.T) {
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	var diagnostics bytes.Buffer
	status := finishCompletionWithTracker(completion{}, func() error {
		signals <- syscall.SIGHUP
		return errors.New("shutdown failed")
	}, &diagnostics, tracker)

	if status != 129 {
		t.Fatalf("finishCompletionWithTracker() status = %d, want 129", status)
	}
}

func TestSIGHUPDuringShutdownKeepsFrozenContext(t *testing.T) {
	state := newLifecycleState()
	state.bytes.Store(4)
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	context := snapshotShutdownContext(state, completion{})
	var delivered hookContext
	var diagnostics bytes.Buffer

	status := finishCompletionWithTracker(completion{}, func() error {
		delivered = context
		state.bytes.Add(8)
		signals <- syscall.SIGHUP
		return nil
	}, &diagnostics, tracker)
	if status != 129 {
		t.Fatalf("finishCompletionWithTracker() status = %d, want 129", status)
	}
	if delivered != context {
		t.Fatalf("shutdown context changed during late SIGHUP: delivered = %#v, snapshot = %#v", delivered, context)
	}
	if got, want := context.reason, "eof"; got != want {
		t.Fatalf("shutdown reason = %q, want %q", got, want)
	}
	if got, want := context.bytes, int64(4); got != want {
		t.Fatalf("shutdown bytes = %d, want %d", got, want)
	}
}

func TestSignalTrackerKeepsFirstHandledUnixSignal(t *testing.T) {
	for _, tt := range []struct {
		name          string
		first, second os.Signal
		status        int
	}{
		{name: "hangup then interrupt", first: syscall.SIGHUP, second: os.Interrupt, status: 129},
		{name: "hangup then terminate", first: syscall.SIGHUP, second: syscall.SIGTERM, status: 129},
		{name: "interrupt then hangup", first: os.Interrupt, second: syscall.SIGHUP, status: 130},
		{name: "terminate then hangup", first: syscall.SIGTERM, second: syscall.SIGHUP, status: 143},
	} {
		t.Run(tt.name, func(t *testing.T) {
			signals := make(chan os.Signal, 2)
			signals <- tt.first
			signals <- tt.second
			tracker := &signalTracker{signals: signals}

			if got := tracker.poll(); got != tt.first {
				t.Fatalf("signalTracker.poll() = %v, want %v", got, tt.first)
			}
			if got := tracker.first; got != tt.first {
				t.Fatalf("signalTracker.first = %v, want %v", got, tt.first)
			}
			if got := signalExitCode(tracker.first); got != tt.status {
				t.Fatalf("signalExitCode(%v) = %d, want %d", tracker.first, got, tt.status)
			}
		})
	}
}

func TestIdleBrokenPipeIsSuccessfulAndSilent(t *testing.T) {
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	config := options{idle: time.Second, idleSet: true, onIdle: "true", onIdleSet: true}
	done := runIdleCopy(config, strings.NewReader("input"), errorWriter{err: syscall.EPIPE}, io.Discard, tracker)
	var diagnostics bytes.Buffer
	if got := finishCompletion(done, nil, &diagnostics); got != 0 {
		t.Fatalf("finishCompletion() status = %d, want 0", got)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("finishCompletion() diagnostics = %q, want empty", diagnostics.String())
	}
}

func TestIdleFirstDataHookFailureWinsBrokenPipe(t *testing.T) {
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	config := options{
		idle:           time.Hour,
		idleSet:        true,
		onFirstData:    failingHookCommand("first-data"),
		onFirstDataSet: true,
	}
	var diagnostics bytes.Buffer
	done := runIdleCopy(config, strings.NewReader("input"), errorWriter{err: syscall.EPIPE}, &diagnostics, tracker)
	if got := finishCompletion(done, nil, &diagnostics); got != 1 {
		t.Fatalf("finishCompletion() status = %d, want 1", got)
	}
	if !strings.Contains(diagnostics.String(), "on-first-data hook failed") {
		t.Fatalf("diagnostics = %q, want first-data hook failure", diagnostics.String())
	}
}
