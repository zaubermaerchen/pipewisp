//go:build !windows

package pipewisp

// This file verifies that Unix hook cancellation covers ordinary descendants.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHookTimeoutStopsOrdinaryChildAndGrandchild(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	grandchild := filepath.Join(directory, "grandchild.pid")
	childCompleted := filepath.Join(directory, "child.completed")
	grandchildCompleted := filepath.Join(directory, "grandchild.completed")

	command := unixHookProcessTreeCommand(child, grandchild, childCompleted, grandchildCompleted)

	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 500*time.Millisecond, nil, nil)
	}()
	waitForHookProcessMarker(t, child)
	waitForHookProcessMarker(t, grandchild)
	err := <-done
	var timeoutErr *hookTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("executeHookWithControl() error = %v, want hook timeout", err)
	}
	assertHookProcessTreeNotCompleted(t, childCompleted, grandchildCompleted)
}

func TestHookSignalStopsOrdinaryChildAndGrandchild(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	grandchild := filepath.Join(directory, "grandchild.pid")
	childCompleted := filepath.Join(directory, "child.completed")
	grandchildCompleted := filepath.Join(directory, "grandchild.completed")
	command := unixHookProcessTreeCommand(child, grandchild, childCompleted, grandchildCompleted)

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
	assertHookProcessTreeNotCompleted(t, childCompleted, grandchildCompleted)
}

func TestHookTimeoutPreservesExternalRootSIGKILL(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	completed := filepath.Join(directory, "child.completed")
	childCommand := "sleep 1; printf done > " + unixQuote(completed)
	command := "sh -c " + unixQuote(childCommand) + " & child=$!; printf '%s' \"$child\" > " + unixQuote(child) + "; kill -KILL $$"

	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		// The timeout must beat hookWaitDelay so it observes the externally
		// killed root while the child still holds the diagnostic descriptor.
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 100*time.Millisecond, nil, nil)
	}()
	waitForHookProcessMarker(t, child)

	err := <-done
	var processErr *hookProcessError
	if !errors.As(err, &processErr) {
		t.Fatalf("executeHookWithControl() error = %v, want externally killed hook process", err)
	}
	signal, signaled := processStateSignal(processErr.state)
	if !signaled || signal != syscall.SIGKILL {
		t.Fatalf("hook process state = %v, want SIGKILL", processErr.state)
	}
	assertHookProcessTreeNotCompleted(t, completed)
}

func TestHookBoundaryKilledRootUsesPostWaitState(t *testing.T) {
	naturalExit := exec.Command("/bin/sh", "-c", "exit 0")
	if err := naturalExit.Run(); err != nil {
		t.Fatalf("natural exit command error = %v", err)
	}
	if (&hookBoundary{}).killedRoot(naturalExit.ProcessState) {
		t.Fatal("killedRoot() = true for a naturally exited root")
	}

	killedRoot := exec.Command("/bin/sh", "-c", "kill -KILL $$")
	if err := killedRoot.Run(); err == nil {
		t.Fatal("killed root command unexpectedly succeeded")
	}
	if !(&hookBoundary{}).killedRoot(killedRoot.ProcessState) {
		t.Fatal("killedRoot() = false for a SIGKILL root")
	}
	if (&hookBoundary{rootExited: true}).killedRoot(killedRoot.ProcessState) {
		t.Fatal("killedRoot() = true after the root was observed exited")
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

func TestAsyncHookRetainsBoundaryAfterRootNaturalExit(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	completed := filepath.Join(directory, "completed")
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sleep 3; printf done > " + unixQuote(completed)
	command := "sh -c " + unixQuote(childCommand) + " &"

	manager := newAsyncHookManager(io.Discard)
	defer manager.stopAndWait()
	manager.start("on-idle", command, hookContext{event: "idle"}, 0)
	waitForHookProcessMarker(t, child)
	// Let Cmd.Wait reach the old WaitDelay path after the root shell exits.
	time.Sleep(hookWaitDelay + 100*time.Millisecond)
	manager.stopAndWait()
	waitForUnixHookProcessExit(t, child)
	if _, err := os.Stat(completed); err == nil {
		t.Fatalf("descendant completion marker exists after async shutdown")
	}
}

func TestAsyncHookRetainsBoundaryWithClosedOutput(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	completed := filepath.Join(directory, "completed")
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sleep 3; printf done > " + unixQuote(completed)
	command := "sh -c " + unixQuote(childCommand) + " >/dev/null 2>&1 &"

	var diagnostics bytes.Buffer
	manager := newAsyncHookManager(&diagnostics)
	defer manager.stopAndWait()
	manager.start("on-idle", command, hookContext{event: "idle"}, 0)
	waitForHookProcessMarker(t, child)
	// Cmd.Wait completes immediately because the child inherited no pipe FDs.
	time.Sleep(hookWaitDelay + 100*time.Millisecond)
	manager.mu.Lock()
	retained := len(manager.hooks)
	manager.mu.Unlock()
	if retained != 1 {
		t.Fatalf("async hook count = %d, want retained boundary", retained)
	}
	manager.stopAndWait()
	waitForUnixHookProcessExit(t, child)
	if _, err := os.Stat(completed); err == nil {
		t.Fatal("descendant completion marker exists after async shutdown")
	}
	if strings.Contains(diagnostics.String(), "on-idle hook failed") {
		t.Fatalf("diagnostics = %q, shutdown cancellation must not be a hook failure", diagnostics.String())
	}
}

func TestAsyncHookRetainedChildTimeoutReportsTimeout(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	completed := filepath.Join(directory, "completed")
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sleep 5; printf done > " + unixQuote(completed)
	command := "sh -c " + unixQuote(childCommand) + " >/dev/null 2>&1 &"
	events := make(chan string, 16)
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(io.MultiWriter(&diagnostics, &eventChannelWriter{label: "diagnostics", events: events}), true)
	manager := newAsyncHookManager(reporter)
	manager.start("on-idle", command, hookContext{event: "idle"}, 300*time.Millisecond)
	waitForHookProcessMarker(t, child)
	waitForEvent(t, events, "type=hook event=idle state=timeout")
	manager.stopAndWait()
	waitForUnixHookProcessExit(t, child)
	got := diagnostics.String()
	if !strings.Contains(got, "type=hook event=idle state=timeout") {
		t.Fatalf("diagnostics = %q, missing timeout record", got)
	}
	if !strings.Contains(got, "on-idle hook failed: hook timed out") {
		t.Fatalf("diagnostics = %q, missing timeout diagnostic", got)
	}
}

func TestAsyncHookShutdownReportsNaturalDirectFailureOnce(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	rootExited := filepath.Join(directory, "root.exited")
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sleep 3"
	command := "sh -c " + unixQuote(childCommand) + " & printf done > " + unixQuote(rootExited) + "; exit 7"
	var diagnostics bytes.Buffer
	manager := newAsyncHookManager(&diagnostics)
	manager.start("on-idle", command, hookContext{event: "idle"}, 0)
	waitForHookProcessMarker(t, child)
	waitForHookProcessMarker(t, rootExited)
	// The descendant keeps the hook output descriptors open, so Cmd.Wait has
	// not reported the already-finished non-zero root until cleanup stops it.
	manager.stopAndWait()
	waitForUnixHookProcessExit(t, child)

	got := diagnostics.String()
	if count := strings.Count(got, "on-idle hook failed:"); count != 1 {
		t.Fatalf("direct failure diagnostics = %d, want exactly one; diagnostics = %q", count, got)
	}
	if !strings.Contains(got, "on-idle hook failed: exit status 7") {
		t.Fatalf("diagnostics = %q, missing natural direct failure", got)
	}
}

func TestAsyncHookTimeoutReportsNaturalDirectFailureSeparately(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	rootExited := filepath.Join(directory, "root.exited")
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sleep 3"
	command := "sh -c " + unixQuote(childCommand) + " & printf done > " + unixQuote(rootExited) + "; exit 7"
	events := make(chan string, 16)
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(io.MultiWriter(&diagnostics, &eventChannelWriter{label: "diagnostics", events: events}), true)
	manager := newAsyncHookManager(reporter)
	manager.start("on-idle", command, hookContext{event: "idle"}, 50*time.Millisecond)
	waitForHookProcessMarker(t, child)
	waitForHookProcessMarker(t, rootExited)
	// The timeout fires while WaitDelay still retains the natural root result.
	waitForEvent(t, events, "type=hook event=idle state=timeout")
	manager.stopAndWait()
	waitForUnixHookProcessExit(t, child)

	got := diagnostics.String()
	if count := strings.Count(got, "on-idle hook failed:"); count != 2 {
		t.Fatalf("failure diagnostics = %d, want direct failure and timeout; diagnostics = %q", count, got)
	}
	if !strings.Contains(got, "on-idle hook failed: exit status 7") {
		t.Fatalf("diagnostics = %q, missing natural direct failure", got)
	}
	if !strings.Contains(got, "on-idle hook failed: hook timed out") {
		t.Fatalf("diagnostics = %q, missing timeout diagnostic", got)
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
		filepath.Join(directory, "child.completed"),
		filepath.Join(directory, "grandchild.completed"),
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

func unixHookProcessTreeCommand(child, grandchild, childCompleted, grandchildCompleted string) string {
	grandchildCommand := "printf '%s' \"$$\" > " + unixQuote(grandchild) + "; sleep 1; printf done > " + unixQuote(grandchildCompleted)
	childCommand := "printf '%s' \"$$\" > " + unixQuote(child) + "; sh -c " + unixQuote(grandchildCommand) + " & wait; printf done > " + unixQuote(childCompleted)
	return "sh -c " + unixQuote(childCommand) + " & wait"
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

func waitForUnixHookProcessExit(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", string(contents), err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Kill(pid, syscall.Signal(0))
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("Kill(%d, 0) error = %v", pid, err)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("process %d from %q did not exit", pid, path)
		}
	}
}

func assertHookProcessTreeNotCompleted(t *testing.T, paths ...string) {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("completion marker %q exists after cancellation: err = %v", path, err)
		}
	}
}
