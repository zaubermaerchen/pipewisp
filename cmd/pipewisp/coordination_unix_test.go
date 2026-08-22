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
	if got := signalExitCode(syscall.SIGTERM); got != 143 {
		t.Fatalf("signalExitCode(SIGTERM) = %d, want 143", got)
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
