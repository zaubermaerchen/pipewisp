//go:build !windows

package main

// This file verifies that Unix hook cancellation covers ordinary descendants.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestHookTimeoutStopsOrdinaryChildAndGrandchild(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	grandchild := filepath.Join(directory, "grandchild.pid")
	completed := filepath.Join(directory, "completed")

	command := unixHookProcessTreeCommand(child, grandchild, completed)

	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, time.Second, nil, nil)
	}()
	waitForHookProcessMarker(t, child)
	waitForHookProcessMarker(t, grandchild)
	err := <-done
	var timeoutErr *hookTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("executeHookWithControl() error = %v, want hook timeout", err)
	}
	time.Sleep(1300 * time.Millisecond)
	if _, statErr := os.Stat(completed); !os.IsNotExist(statErr) {
		t.Fatalf("grandchild completion marker exists after timeout: err = %v", statErr)
	}
}

func TestHookSignalStopsOrdinaryChildAndGrandchild(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	grandchild := filepath.Join(directory, "grandchild.pid")
	completed := filepath.Join(directory, "completed")
	command := unixHookProcessTreeCommand(child, grandchild, completed)

	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 0, tracker, nil)
	}()
	waitForHookProcessMarker(t, child)
	waitForHookProcessMarker(t, grandchild)
	signals <- os.Interrupt

	var signalErr *hookSignalError
	select {
	case err := <-done:
		if !errors.As(err, &signalErr) {
			t.Fatalf("executeHookWithControl() error = %v, want hook interruption", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executeHookWithControl() did not stop after signal")
	}
	time.Sleep(1300 * time.Millisecond)
	if _, statErr := os.Stat(completed); !os.IsNotExist(statErr) {
		t.Fatalf("grandchild completion marker exists after signal: err = %v", statErr)
	}
}

func TestHookSignalStopsDescendantAfterRootNaturalExit(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	completed := filepath.Join(directory, "completed")
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sleep 2; printf done > " + unixQuote(completed)
	command := "sh -c " + unixQuote(childCommand) + " &"

	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 0, tracker, nil)
	}()
	waitForHookProcessMarker(t, child)
	// The direct shell has naturally exited while its background child keeps
	// the diagnostic descriptor open inside the process group.
	time.Sleep(50 * time.Millisecond)
	signals <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeHookWithControl() error = %v, want natural exit", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executeHookWithControl() did not finish after signal")
	}
	time.Sleep(2100 * time.Millisecond)
	if _, statErr := os.Stat(completed); !os.IsNotExist(statErr) {
		t.Fatalf("descendant completion marker exists after signal: err = %v", statErr)
	}
}

func TestHookCancellationDoesNotStopUnrelatedProcess(t *testing.T) {
	unrelated := exec.Command("sleep", "3")
	if err := unrelated.Start(); err != nil {
		t.Fatalf("unrelated Start() error = %v", err)
	}
	defer func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	}()

	directory := t.TempDir()
	command := unixHookProcessTreeCommand(
		filepath.Join(directory, "child.pid"),
		filepath.Join(directory, "grandchild.pid"),
		filepath.Join(directory, "completed"),
	)
	var diagnostics bytes.Buffer
	err := executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 20*time.Millisecond, nil, nil)
	var timeoutErr *hookTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("executeHookWithControl() error = %v, want hook timeout", err)
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated process was stopped: Signal(0) error = %v", err)
	}
}

func unixHookProcessTreeCommand(child, grandchild, completed string) string {
	grandchildCommand := "printf '%s' \"$$\" > " + unixQuote(grandchild) + "; sleep 2; printf done > " + unixQuote(completed)
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sh -c " + unixQuote(grandchildCommand) + " & wait"
	return "sh -c " + unixQuote(childCommand)
}

func waitForHookProcessMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("process marker %q was not created", path)
		}
	}
}
