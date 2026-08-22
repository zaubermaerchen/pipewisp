//go:build !windows

package main

// This file tests Unix-specific copy-error classification and signal status values.

import (
	"errors"
	"syscall"
	"testing"
)

func TestClassifyCompletionRecognizesBrokenPipe(t *testing.T) {
	for _, err := range []error{syscall.EPIPE, errors.Join(errors.New("wrapped"), syscall.EPIPE)} {
		if got := classifyCompletion(completion{copyErr: err}); got != completionBrokenPipe {
			t.Errorf("classifyCompletion(%v) = %v, want %v", err, got, completionBrokenPipe)
		}
	}
}

func TestSignalExitCodeUnix(t *testing.T) {
	if got := signalExitCode(syscall.SIGTERM); got != 143 {
		t.Fatalf("signalExitCode(SIGTERM) = %d, want 143", got)
	}
}
