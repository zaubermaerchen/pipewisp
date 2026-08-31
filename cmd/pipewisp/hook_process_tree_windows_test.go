//go:build windows

package main

// This file verifies that Windows hook cancellation covers ordinary descendants.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	hookProcessTreeModeEnv       = "PIPEWISP_TEST_HOOK_TREE_MODE"
	hookProcessTreeTokenEnv      = "PIPEWISP_TEST_HOOK_TREE_TOKEN"
	hookProcessTreeExecutableEnv = "PIPEWISP_TEST_HOOK_TREE_EXECUTABLE"
	hookProcessTreeToken         = "pipewisp-hook-tree-v1"
)

func TestMain(m *testing.M) {
	if os.Getenv(hookProcessTreeTokenEnv) != hookProcessTreeToken {
		os.Exit(m.Run())
	}
	switch os.Getenv(hookProcessTreeModeEnv) {
	case "child":
		hookProcessTreeChild()
		return
	case "root":
		hookProcessTreeRoot()
		return
	case "grandchild":
		hookProcessTreeGrandchild()
		return
	default:
		os.Exit(m.Run())
	}
}

func TestHookSignalStopsDescendantAfterRootNaturalExit(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.started")
	grandchild := filepath.Join(directory, "grandchild.started")
	completed := filepath.Join(directory, "completed")
	gate := filepath.Join(directory, "release")
	t.Setenv(hookProcessTreeChildEnv, child)
	t.Setenv(hookProcessTreeGrandchildEnv, grandchild)
	t.Setenv(hookProcessTreeCompletedEnv, completed)
	t.Setenv(hookProcessTreeGateEnv, gate)
	t.Setenv(hookProcessTreeSleepEnv, "10s")
	defer func() { _ = os.WriteFile(gate, []byte("release"), 0600) }()

	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	command := windowsHookProcessTreeCommand(t, "root")
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 0, tracker, nil)
	}()
	waitForWindowsHookProcessMarker(t, child)
	waitForWindowsHookProcessMarker(t, grandchild)
	childHandle := openWindowsHookProcess(t, readWindowsHookPID(t, child))
	grandchildHandle := openWindowsHookProcess(t, readWindowsHookPID(t, grandchild))
	if err := os.WriteFile(gate, []byte("release"), 0600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", gate, err)
	}
	waitForWindowsHookProcessExit(t, childHandle, "child")
	// The child has exited before its grandchild's output handle is released;
	// deliver the signal while cmd.exe is still draining that inherited handle.
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
	waitForWindowsHookProcessExit(t, grandchildHandle, "grandchild")
	if _, statErr := os.Stat(completed); !os.IsNotExist(statErr) {
		t.Fatalf("grandchild completion marker exists after signal: err = %v", statErr)
	}
}

func TestHookTimeoutStopsOrdinaryChildAndGrandchild(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.started")
	grandchild := filepath.Join(directory, "grandchild.started")
	completed := filepath.Join(directory, "completed")
	t.Setenv(hookProcessTreeChildEnv, child)
	t.Setenv(hookProcessTreeGrandchildEnv, grandchild)
	t.Setenv(hookProcessTreeCompletedEnv, completed)
	t.Setenv(hookProcessTreeSleepEnv, "10s")

	command := windowsHookProcessTreeCommand(t, "child")
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 3*time.Second, nil, nil)
	}()
	waitForWindowsHookProcessMarker(t, child)
	waitForWindowsHookProcessMarker(t, grandchild)
	childHandle := openWindowsHookProcess(t, readWindowsHookPID(t, child))
	grandchildHandle := openWindowsHookProcess(t, readWindowsHookPID(t, grandchild))
	err := <-done
	var timeoutErr *hookTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("executeHookWithControl() error = %v, want hook timeout", err)
	}
	waitForWindowsHookProcessExit(t, childHandle, "child")
	waitForWindowsHookProcessExit(t, grandchildHandle, "grandchild")
	if _, statErr := os.Stat(completed); !os.IsNotExist(statErr) {
		t.Fatalf("grandchild completion marker exists after timeout: err = %v", statErr)
	}
}

func TestHookSignalStopsOrdinaryChildAndGrandchild(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.started")
	grandchild := filepath.Join(directory, "grandchild.started")
	completed := filepath.Join(directory, "completed")
	t.Setenv(hookProcessTreeChildEnv, child)
	t.Setenv(hookProcessTreeGrandchildEnv, grandchild)
	t.Setenv(hookProcessTreeCompletedEnv, completed)
	t.Setenv(hookProcessTreeSleepEnv, "10s")

	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	command := windowsHookProcessTreeCommand(t, "child")
	go func() {
		done <- executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, 0, tracker, nil)
	}()
	waitForWindowsHookProcessMarker(t, child)
	waitForWindowsHookProcessMarker(t, grandchild)
	childHandle := openWindowsHookProcess(t, readWindowsHookPID(t, child))
	grandchildHandle := openWindowsHookProcess(t, readWindowsHookPID(t, grandchild))
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
	waitForWindowsHookProcessExit(t, childHandle, "child")
	waitForWindowsHookProcessExit(t, grandchildHandle, "grandchild")
	if _, statErr := os.Stat(completed); !os.IsNotExist(statErr) {
		t.Fatalf("grandchild completion marker exists after signal: err = %v", statErr)
	}
}

func TestHookCancellationDoesNotStopUnrelatedProcess(t *testing.T) {
	directory := t.TempDir()
	unrelatedCompleted := filepath.Join(directory, "unrelated.completed")
	unrelatedGrandchild := filepath.Join(directory, "unrelated.grandchild.started")
	unrelated := exec.Command(os.Args[0])
	unrelated.Env = hookProcessTreeEnvironment("grandchild", unrelatedGrandchild, unrelatedCompleted, "2s")
	if err := unrelated.Start(); err != nil {
		t.Fatalf("unrelated Start() error = %v", err)
	}
	waitForWindowsHookProcessMarker(t, unrelatedGrandchild)
	unrelatedHandle := openWindowsHookProcess(t, readWindowsHookPID(t, unrelatedGrandchild))
	unrelatedDone := false
	defer func() {
		if !unrelatedDone {
			_ = unrelated.Process.Kill()
			_ = unrelated.Wait()
		}
	}()

	child := filepath.Join(directory, "child.started")
	grandchild := filepath.Join(directory, "grandchild.started")
	completed := filepath.Join(directory, "completed")
	t.Setenv(hookProcessTreeChildEnv, child)
	t.Setenv(hookProcessTreeGrandchildEnv, grandchild)
	t.Setenv(hookProcessTreeCompletedEnv, completed)
	t.Setenv(hookProcessTreeSleepEnv, "10s")
	command := windowsHookProcessTreeCommand(t, "child")
	var diagnostics bytes.Buffer
	err := executeHookWithControl(command, hookContext{event: "ready"}, &diagnostics, time.Second, nil, nil)
	var timeoutErr *hookTimeoutError
	if !errors.As(err, &timeoutErr) {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
		t.Fatalf("executeHookWithControl() error = %v, want hook timeout", err)
	}
	if status, err := windows.WaitForSingleObject(unrelatedHandle, 0); err != nil {
		t.Fatalf("unrelated WaitForSingleObject(0) error = %v", err)
	} else if status != windowsWaitTimeout {
		t.Fatalf("unrelated process ended during hook cancellation: wait status = %#x", status)
	}
	waitForWindowsHookProcessExit(t, unrelatedHandle, "unrelated")
	if err := unrelated.Wait(); err != nil {
		t.Fatalf("unrelated process was stopped: Wait() error = %v", err)
	}
	unrelatedDone = true
	if _, err := os.Stat(unrelatedCompleted); err != nil {
		t.Fatalf("unrelated completion marker error = %v", err)
	}
}

const (
	hookProcessTreeChildEnv      = "PIPEWISP_TEST_HOOK_TREE_CHILD"
	hookProcessTreeGrandchildEnv = "PIPEWISP_TEST_HOOK_TREE_GRANDCHILD"
	hookProcessTreeCompletedEnv  = "PIPEWISP_TEST_HOOK_TREE_COMPLETED"
	hookProcessTreeGateEnv       = "PIPEWISP_TEST_HOOK_TREE_GATE"
	hookProcessTreeSleepEnv      = "PIPEWISP_TEST_HOOK_TREE_SLEEP"
)

func windowsHookProcessTreeCommand(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv(hookProcessTreeTokenEnv, hookProcessTreeToken)
	t.Setenv(hookProcessTreeModeEnv, mode)
	t.Setenv(hookProcessTreeExecutableEnv, os.Args[0])
	return `"%` + hookProcessTreeExecutableEnv + `%"`
}

func hookProcessTreeChild() {
	if err := os.WriteFile(os.Getenv(hookProcessTreeChildEnv), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(1)
	}
	grandchild := exec.Command(os.Args[0])
	grandchild.Stdout = os.Stdout
	grandchild.Stderr = os.Stderr
	grandchild.Env = hookProcessTreeEnvironment("grandchild", os.Getenv(hookProcessTreeGrandchildEnv), os.Getenv(hookProcessTreeCompletedEnv), os.Getenv(hookProcessTreeSleepEnv))
	if err := grandchild.Run(); err != nil {
		os.Exit(1)
	}
}

func hookProcessTreeRoot() {
	if err := os.WriteFile(os.Getenv(hookProcessTreeChildEnv), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(1)
	}
	grandchild := exec.Command(os.Args[0])
	grandchild.Stdout = os.Stdout
	grandchild.Stderr = os.Stderr
	grandchild.Env = hookProcessTreeEnvironment("grandchild", os.Getenv(hookProcessTreeGrandchildEnv), os.Getenv(hookProcessTreeCompletedEnv), os.Getenv(hookProcessTreeSleepEnv))
	if err := grandchild.Start(); err != nil {
		os.Exit(1)
	}
	for {
		if _, err := os.Stat(os.Getenv(hookProcessTreeGateEnv)); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func hookProcessTreeEnvironment(mode, grandchild, completed, sleep string) []string {
	environment := os.Environ()
	updates := map[string]string{
		hookProcessTreeModeEnv:       mode,
		hookProcessTreeTokenEnv:      hookProcessTreeToken,
		hookProcessTreeGrandchildEnv: grandchild,
		hookProcessTreeCompletedEnv:  completed,
		hookProcessTreeSleepEnv:      sleep,
	}
	for index, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator < 0 {
			continue
		}
		if value, ok := updates[strings.ToUpper(entry[:separator])]; ok {
			environment[index] = entry[:separator] + "=" + value
			delete(updates, strings.ToUpper(entry[:separator]))
		}
	}
	for key, value := range updates {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func hookProcessTreeGrandchild() {
	if err := os.WriteFile(os.Getenv(hookProcessTreeGrandchildEnv), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(1)
	}
	duration := 2 * time.Second
	if value := os.Getenv(hookProcessTreeSleepEnv); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			duration = parsed
		}
	}
	time.Sleep(duration)
	if err := os.WriteFile(os.Getenv(hookProcessTreeCompletedEnv), []byte("done"), 0600); err != nil {
		os.Exit(1)
	}
}

const windowsWaitTimeout = 0x00000102

func readWindowsHookPID(t *testing.T, path string) uint32 {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	pid, err := strconv.ParseUint(string(contents), 10, 32)
	if err != nil {
		t.Fatalf("ParseUint(%q) error = %v", string(contents), err)
	}
	return uint32(pid)
}

func openWindowsHookProcess(t *testing.T, pid uint32) windows.Handle {
	t.Helper()
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatalf("OpenProcess(%d) error = %v", pid, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(handle)
	})
	return handle
}

func waitForWindowsHookProcessExit(t *testing.T, handle windows.Handle, name string) {
	t.Helper()
	status, err := windows.WaitForSingleObject(handle, 2*1000)
	if err != nil {
		t.Fatalf("WaitForSingleObject(%s) error = %v", name, err)
	}
	if status != windows.WAIT_OBJECT_0 {
		t.Fatalf("WaitForSingleObject(%s) status = %#x, want WAIT_OBJECT_0", name, status)
	}
}

func waitForWindowsHookProcessMarker(t *testing.T, path string) {
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
