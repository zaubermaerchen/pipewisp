package pipewisp

// This file tests asynchronous idle/resume hook parsing and lifecycle behavior.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAsyncHookBoundaryStopFailureIsDiagnosticOnly(t *testing.T) {
	var diagnostics bytes.Buffer
	err := &hookBoundaryStopError{err: errors.New("boundary stop failed")}
	reportAsyncHookBoundaryStopError(&diagnostics, "on-idle", err)

	got := diagnostics.String()
	if !strings.Contains(got, "on-idle hook cleanup failed: stop hook execution boundary: boundary stop failed") {
		t.Fatalf("diagnostics = %q, want boundary cleanup diagnostic", got)
	}
	if strings.Contains(got, "on-idle hook failed") {
		t.Fatalf("diagnostics = %q, want no hook failure classification", got)
	}
}

func TestParseAsyncIdleAndResumeHooks(t *testing.T) {
	got, help, err := parseArgs([]string{
		"--idle", "25ms",
		"--on-idle.async", "idle",
		"--on-resume.async=resume",
	})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if help {
		t.Fatal("parseArgs() help = true, want false")
	}
	if !got.idleSet || got.idle != 25*time.Millisecond ||
		!got.onIdleAsyncSet || got.onIdleAsync != "idle" ||
		!got.onResumeAsyncSet || got.onResumeAsync != "resume" {
		t.Fatalf("parseArgs() options = %#v, want async idle/resume hooks", got)
	}
}

func TestParseRejectsAsyncSyncHookCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"--idle", "1ms", "--on-idle", "sync", "--on-idle.async", "async"},
		{"--idle", "1ms", "--on-idle.async", "async", "--on-idle", "sync"},
		{"--idle", "1ms", "--on-resume", "sync", "--on-resume.async", "async"},
		{"--idle", "1ms", "--on-resume.async", "async", "--on-resume", "sync"},
		{"--idle", "1ms", "--on-idle.async", "first", "--on-idle.async", "second"},
		{"--idle", "1ms", "--on-resume.async", "first", "--on-resume.async", "second"},
	} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) succeeded, want mutual-exclusion error", args)
		}
	}
}

func TestParseRejectsAsyncBoundaryHooks(t *testing.T) {
	for _, args := range [][]string{
		{"--on-ready.async", "ready"},
		{"--on-shutdown.async", "shutdown"},
	} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) succeeded, want unsupported-option error", args)
		}
	}
}

func TestAsyncIdleHookDoesNotBlockNormalProcessing(t *testing.T) {
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	command := hookStartMarkerCommand(started) + hookSleepCommand(time.Second)
	var output, diagnostics bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--idle", "5ms", "--on-idle.async", command}, input, &output, &diagnostics)
	}()
	waitForHookFile(t, started)
	startedAt := time.Now()
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Run() waited for async hook; elapsed = %s", time.Since(startedAt))
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestAsyncIdleHooksMayOverlap(t *testing.T) {
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	input := &threeStageReader{
		stages: [][]byte{[]byte("a"), []byte("b"), []byte("c"), nil},
		gates:  []chan struct{}{nil, make(chan struct{}), make(chan struct{}), make(chan struct{})},
	}
	command := asyncAppendMarkerSleepCommand(started, time.Second)
	var output, diagnostics bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--idle", "5ms", "--on-idle.async", command}, input, &output, &diagnostics)
	}()
	waitForMarkerCount(t, started, "started", 1)
	close(input.gates[1])
	close(input.gates[2])
	waitForMarkerCount(t, started, "started", 2)
	close(input.gates[3])
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish after releasing EOF")
	}
	if got, want := output.String(), "abc"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestAsyncIdleFailureIsDiagnosticOnly(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	command := failingHookCommand("async-idle-failed")
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--idle", "5ms", "--on-idle.async", command}, input, io.Discard, diagnostics)
	}()
	waitForEvent(t, events, "on-idle hook failed")
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
}

func TestAsyncResumeHookRunsForResumedData(t *testing.T) {
	input := &asyncResumeReader{dataReady: make(chan struct{}), eofReady: make(chan struct{})}
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	var output bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--verbose", "--idle", "5ms", "--on-resume.async", hookOutputCommand("async-resume")}, input, &output, diagnostics)
	}()
	waitForEvent(t, events, "type=event event=idle")
	close(input.dataReady)
	waitForEvent(t, events, "async-resume")
	close(input.eofReady)
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestAsyncHookPreservesHookDiagnosticsAndPassthroughStdout(t *testing.T) {
	input := &gatedEOFReader{first: []byte("payload"), eofReady: make(chan struct{})}
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	command := hookOutputAndErrorCommand("async-stdout", "async-stderr")
	var output bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--idle", "5ms", "--on-idle.async", command}, input, &output, diagnostics)
	}()
	waitForEvent(t, events, "async-stdout")
	input.releaseEOF()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
	if got, want := output.String(), "payload"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestAsyncIdleHookTimeoutIsDiagnosticOnly(t *testing.T) {
	input := &gatedEOFReader{first: []byte("a"), eofReady: make(chan struct{})}
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	command := hookSleepCommand(time.Second)
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--idle", "5ms", "--on-idle.async", command, "--hook-timeout", "20ms"}, input, io.Discard, diagnostics)
	}()
	waitForEvent(t, events, "timed out")
	input.releaseEOF()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
}

func TestAsyncHookVerboseRecordsStartAndEnd(t *testing.T) {
	input := &gatedEOFReader{first: []byte("a"), eofReady: make(chan struct{})}
	events := make(chan string, 16)
	var output, diagnostics bytes.Buffer
	diagnosticEvents := io.MultiWriter(&diagnostics, &eventChannelWriter{label: "diagnostics", events: events})
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--verbose", "--idle", "5ms", "--on-idle.async", "true"}, input, &output, diagnosticEvents)
	}()
	waitForEvent(t, events, "type=hook event=idle state=start")
	input.releaseEOF()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
	if !strings.Contains(diagnostics.String(), "type=hook event=idle state=exit") {
		t.Fatalf("diagnostics = %q, missing async terminal record", diagnostics.String())
	}
}

func TestAsyncHookCompletedRootDoesNotTimeout(t *testing.T) {
	events := make(chan string, 16)
	var diagnostics bytes.Buffer
	diagnosticEvents := io.MultiWriter(&diagnostics, &eventChannelWriter{label: "diagnostics", events: events})
	manager := newAsyncHookManager(newVerboseReporter(diagnosticEvents, true))
	manager.start("on-idle", "true", hookContext{event: "idle"}, 100*time.Millisecond)
	defer manager.stopAndWait()

	waitForEvent(t, events, "type=hook event=idle state=exit")
	if strings.Contains(diagnostics.String(), "state=timeout") {
		t.Fatalf("diagnostics = %q, completed root unexpectedly timed out", diagnostics.String())
	}
}

func TestAsyncHookVerboseFailureReportsImmediately(t *testing.T) {
	events := make(chan string, 16)
	var diagnostics bytes.Buffer
	diagnosticEvents := io.MultiWriter(&diagnostics, &eventChannelWriter{label: "diagnostics", events: events})
	manager := newAsyncHookManager(newVerboseReporter(diagnosticEvents, true))
	manager.start("on-idle", "false", hookContext{event: "idle"}, 0)
	defer manager.stopAndWait()

	waitForEvent(t, events, "on-idle hook failed")
}

func TestAsyncHookReleasesCompletedInvocations(t *testing.T) {
	manager := newAsyncHookManager(io.Discard)
	defer manager.stopAndWait()

	for i := 0; i < 32; i++ {
		manager.start("on-idle", "true", hookContext{event: "idle"}, 0)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		manager.mu.Lock()
		remaining := len(manager.hooks)
		manager.mu.Unlock()
		if remaining == 0 {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("manager retained %d completed hooks", remaining)
		}
	}
}

func TestAsyncHookManagerBroadcastsShutdown(t *testing.T) {
	manager := newAsyncHookManager(io.Discard)
	manager.stopAndWait()
	select {
	case <-manager.shutdown:
	default:
		t.Fatal("manager shutdown broadcast is not closed")
	}
}

func TestAsyncHookShutdownWinsReadyTimeout(t *testing.T) {
	var diagnostics bytes.Buffer
	manager := newAsyncHookManager(&diagnostics)
	execution, err := startHookExecution(hookSleepCommand(time.Second), hookContext{event: "idle"}, manager.diagnostics, nil)
	if err != nil {
		t.Fatalf("startHookExecution() error = %v", err)
	}
	hook := &asyncHook{
		manager:   manager,
		name:      "on-idle",
		event:     "idle",
		started:   time.Now(),
		timeout:   time.Nanosecond,
		execution: execution,
		done:      make(chan struct{}),
	}
	manager.mu.Lock()
	manager.stopped = true
	close(manager.shutdown)
	manager.hooks[hook] = struct{}{}
	manager.mu.Unlock()
	defer manager.stopAndWait()
	go manager.wait(hook)
	select {
	case <-hook.done:
	case <-time.After(time.Second):
		t.Fatal("async hook did not stop after shutdown broadcast")
	}
	manager.stopAndWait()
	if strings.Contains(diagnostics.String(), "hook timed out") {
		t.Fatalf("diagnostics = %q, shutdown was classified as timeout", diagnostics.String())
	}
}

func TestAsyncHooksStopBeforeShutdownHook(t *testing.T) {
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	finished := filepath.Join(directory, "finished")
	shutdown := filepath.Join(directory, "shutdown")
	input := &gatedEOFReader{first: []byte("a"), eofReady: make(chan struct{})}
	asyncCommand := hookStartMarkerCommand(started) + hookSleepCommand(time.Second) + hookMarkerCommand(finished)
	shutdownCommand := hookStartMarkerCommand(shutdown)
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{
			"--idle", "5ms",
			"--on-idle.async", asyncCommand,
			"--on-shutdown", shutdownCommand,
		}, input, io.Discard, io.Discard)
	}()
	waitForHookFile(t, started)
	input.releaseEOF()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("Run() status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish")
	}
	if _, err := os.Stat(shutdown); err != nil {
		t.Fatalf("shutdown marker error = %v", err)
	}
	if _, err := os.Stat(finished); err == nil {
		t.Fatal("async hook ran to completion after shutdown")
	}
}

func asyncAppendMarkerSleepCommand(path string, duration time.Duration) string {
	if runtime.GOOS == "windows" {
		return "echo started>>" + path + " & " + hookSleepCommand(duration)
	}
	return "printf started >> " + unixQuote(path) + "; " + hookSleepCommand(duration)
}

type asyncResumeReader struct {
	dataReady chan struct{}
	eofReady  chan struct{}
	calls     int
}

func (reader *asyncResumeReader) Read(p []byte) (int, error) {
	reader.calls++
	switch reader.calls {
	case 1:
		return copy(p, []byte("a")), nil
	case 2:
		<-reader.dataReady
		return copy(p, []byte("b")), nil
	default:
		<-reader.eofReady
		return 0, io.EOF
	}
}

func waitForMarkerCount(t *testing.T, path, marker string, count int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(path)
		if bytes.Count(data, []byte(marker)) >= count {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("file %q did not contain %d %q markers; got %q", path, count, marker, data)
		}
	}
}
