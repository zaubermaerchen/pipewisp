//go:build windows

package main

// This file verifies Windows broken-pipe classification and interrupt status.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
)

func TestWindowsBrokenPipeClassification(t *testing.T) {
	tests := []error{
		syscall.ERROR_BROKEN_PIPE,
		windowsErrorNoData,
		fmt.Errorf("wrapped: %w", syscall.ERROR_BROKEN_PIPE),
	}
	for _, err := range tests {
		if got := classifyCompletion(completion{copyErr: err}); got != completionBrokenPipe {
			t.Errorf("classifyCompletion(%v) = %v, want %v", err, got, completionBrokenPipe)
		}
	}
	if errors.Is(errors.New("ordinary"), syscall.ERROR_BROKEN_PIPE) {
		t.Fatal("ordinary error unexpectedly classified as broken pipe")
	}
}

func TestWindowsFirstDataHookFailureWinsBrokenPipe(t *testing.T) {
	for _, err := range []error{syscall.ERROR_BROKEN_PIPE, windowsErrorNoData, syscall.EPIPE} {
		done := completion{copyErr: err, firstDataHookFailed: true}
		if got := finishCompletion(done, nil, io.Discard); got != 1 {
			t.Errorf("finishCompletion(%v) = %d, want 1", err, got)
		}
	}
}

func TestWindowsInterruptExitCode(t *testing.T) {
	if got := signalExitCode(os.Interrupt); got != 130 {
		t.Fatalf("signalExitCode(os.Interrupt) = %d, want 130", got)
	}
}
