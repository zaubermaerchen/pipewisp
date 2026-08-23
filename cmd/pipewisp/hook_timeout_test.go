package main

// This file verifies bounded hook execution and independent timeout windows.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHookTimeoutStopsProcessAndReportsFailure(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "completed")
	command := hookSleepCommand(1*time.Second) + hookMarkerCommand(marker)
	config := options{
		on:             command,
		onSet:          true,
		hookTimeout:    20 * time.Millisecond,
		hookTimeoutSet: true,
	}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	started := time.Now()
	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("timed-out hook took %s, want less than 500ms", elapsed)
	}
	if got := output.String(); got != "" {
		t.Fatalf("output = %q, want empty after on hook failure", got)
	}
	if got := diagnostics.String(); !strings.Contains(got, "on hook failed") || !strings.Contains(got, "timed out") {
		t.Fatalf("diagnostics = %q, want hook failure and timeout", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("completion marker exists after timeout: err = %v", err)
	}
}

func TestHookTimeoutWindowIsIndependentForEachInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gatedReader test uses Unix shell marker commands")
	}

	directory := t.TempDir()
	idleMarker := filepath.Join(directory, "idle")
	resumeMarker := filepath.Join(directory, "resume")
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	config := options{
		idle:           5 * time.Millisecond,
		idleSet:        true,
		onIdle:         hookSleepCommand(30*time.Millisecond) + hookMarkerCommand(idleMarker),
		onIdleSet:      true,
		onResume:       hookSleepCommand(30*time.Millisecond) + hookMarkerCommand(resumeMarker),
		onResumeSet:    true,
		hookTimeout:    50 * time.Millisecond,
		hookTimeoutSet: true,
	}
	var diagnostics bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, io.Discard, &diagnostics) }()
	waitForHookFile(t, idleMarker)
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0; diagnostics = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if _, err := os.Stat(resumeMarker); err != nil {
		t.Fatalf("resume marker error = %v, want second hook to complete", err)
	}
}

func TestFirstDataHookTimeoutPreservesFirstChunkAndRunsOff(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{
		onFirstData:    hookSleepCommand(time.Second),
		onFirstDataSet: true,
		off:            hookOutputCommand("off"),
		offSet:         true,
		hookTimeout:    20 * time.Millisecond,
		hookTimeoutSet: true,
	}
	input := &shortReadReader{chunks: [][]byte{[]byte("first"), []byte("later")}}
	if got := runWithOptions(config, input, &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if got, want := output.String(), "first"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "on-first-data hook failed") || !strings.Contains(got, "timed out") || !strings.Contains(got, "off") {
		t.Fatalf("diagnostics = %q, want first-data timeout and off output", got)
	}
}

func TestIdleHookTimeoutContinuesCopyAndRunsOff(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "idle")
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	config := options{
		idle:           5 * time.Millisecond,
		idleSet:        true,
		onIdle:         hookStartMarkerCommand(marker) + hookSleepCommand(time.Second),
		onIdleSet:      true,
		off:            hookOutputCommand("off"),
		offSet:         true,
		hookTimeout:    20 * time.Millisecond,
		hookTimeoutSet: true,
	}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, &diagnostics) }()
	waitForHookFile(t, marker)
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 1 {
			t.Fatalf("runWithOptions() exit code = %d, want 1; diagnostics = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "on-idle hook failed") || !strings.Contains(got, "timed out") || !strings.Contains(got, "off") {
		t.Fatalf("diagnostics = %q, want idle timeout and off output", got)
	}
}

func TestOffHookTimeoutReportsFailure(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{
		off:            hookSleepCommand(time.Second),
		offSet:         true,
		hookTimeout:    20 * time.Millisecond,
		hookTimeoutSet: true,
	}
	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if got := diagnostics.String(); !strings.Contains(got, "off hook failed") || !strings.Contains(got, "timed out") {
		t.Fatalf("diagnostics = %q, want off timeout", got)
	}
}

func TestHookPreservesLargeTrailingStdoutAndStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dd is not available on Windows")
	}

	const chunkSize = 128 * 1024
	command := "dd if=/dev/zero bs=" + strconv.Itoa(chunkSize) + " count=1 2>/dev/null; dd if=/dev/zero bs=" + strconv.Itoa(chunkSize) + " count=1 1>&2 2>/dev/null"
	var diagnostics bytes.Buffer
	if err := runHook("large-output", command, &diagnostics); err != nil {
		t.Fatalf("runHook() error = %v; diagnostics length = %d", err, diagnostics.Len())
	}
	if got, want := diagnostics.Len(), 2*chunkSize; got != want {
		t.Fatalf("diagnostics length = %d, want %d", got, want)
	}
	for index, value := range diagnostics.Bytes() {
		if value != 0 {
			t.Fatalf("diagnostics[%d] = %d, want zero byte", index, value)
		}
	}
}

func TestHookReturnsAfterShellExitWhenDescendantKeepsOutputOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell process test uses POSIX syntax")
	}

	var diagnostics bytes.Buffer
	started := time.Now()
	if err := runHook("descendant", "sleep 2 &", &diagnostics); err != nil {
		t.Fatalf("runHook() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("runHook() took %s, want direct shell completion without descendant drain", elapsed)
	}
}

func hookSleepCommand(duration time.Duration) string {
	if runtime.GOOS == "windows" {
		// ping provides a shell-independent delay on the Windows CI image.
		count := int(duration/time.Second) + 2
		return "ping -n " + strconv.Itoa(count) + " 127.0.0.1 >NUL"
	}
	return "sleep " + duration.String()
}

func hookMarkerCommand(path string) string {
	if runtime.GOOS == "windows" {
		return " & echo done > " + path
	}
	return "; printf done > " + unixQuote(path)
}

func hookStartMarkerCommand(path string) string {
	if runtime.GOOS == "windows" {
		return "echo started > " + path + " & "
	}
	return "printf started > " + unixQuote(path) + "; "
}

func waitForHookFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
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
			t.Fatalf("file %q was not created", path)
		}
	}
}
