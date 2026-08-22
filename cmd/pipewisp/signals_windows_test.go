//go:build windows

package main

// This file verifies Windows broken-pipe classification and interrupt status.

import (
	"errors"
	"fmt"
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

func TestWindowsInterruptExitCode(t *testing.T) {
	if got := signalExitCode(os.Interrupt); got != 130 {
		t.Fatalf("signalExitCode(os.Interrupt) = %d, want 130", got)
	}
}
