package main

// This file verifies opt-in hook failure tolerance without changing stream or signal failures.

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseIgnoreHookErrors(t *testing.T) {
	got, help, err := parseArgs([]string{"--ignore-hook-errors"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if help {
		t.Fatal("parseArgs() help = true, want false")
	}
	if !got.ignoreHookErrors {
		t.Fatal("parseArgs() ignoreHookErrors = false, want true")
	}
}

func TestParseRejectsDuplicateIgnoreHookErrors(t *testing.T) {
	_, help, err := parseArgs([]string{"--ignore-hook-errors", "--ignore-hook-errors"})
	if err == nil {
		t.Fatal("parseArgs() error = nil, want duplicate option error")
	}
	if help {
		t.Fatal("parseArgs() help = true, want false")
	}
	if got, want := err.Error(), "--ignore-hook-errors specified more than once"; got != want {
		t.Fatalf("parseArgs() error = %q, want %q", got, want)
	}
}

func TestIgnoreReadyFailureContinuesLifecycle(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{
		onReady:          failingHookCommand("ready"),
		onReadySet:       true,
		onShutdown:       hookOutputCommand("shutdown"),
		onShutdownSet:    true,
		ignoreHookErrors: true,
	}

	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "pipewisp: on-ready hook failed:") || !strings.Contains(got, "shutdown") {
		t.Fatalf("diagnostics = %q, want ready failure and shutdown output", got)
	}
}

func TestIgnoreFirstDataFailurePreservesAndContinuesStream(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	input := &shortReadReader{chunks: [][]byte{[]byte("first"), []byte("later")}}
	config := options{
		onFirstData:      failingHookCommand("first"),
		onFirstDataSet:   true,
		ignoreHookErrors: true,
	}

	if got := runWithOptions(config, input, &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got, want := output.String(), "firstlater"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "on-first-data hook failed") {
		t.Fatalf("diagnostics = %q, want first-data failure", got)
	}
}

func TestIgnoreIdleAndResumeFailuresKeepFinalStatusSuccessful(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:             5 * time.Millisecond,
		idleSet:          true,
		onIdle:           failingHookCommand("idle"),
		onIdleSet:        true,
		onResume:         failingHookCommand("resume"),
		onResumeSet:      true,
		ignoreHookErrors: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	waitForEvent(t, events, "on-idle hook failed")
	input.releaseSecond()
	waitForEvent(t, events, "on-resume hook failed")

	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestIgnoreShutdownFailurePreservesLifecycleResult(t *testing.T) {
	t.Run("normal eof", func(t *testing.T) {
		var output bytes.Buffer
		var diagnostics bytes.Buffer
		config := options{
			onShutdown:       failingHookCommand("shutdown"),
			onShutdownSet:    true,
			ignoreHookErrors: true,
		}

		if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
		}
		if !strings.Contains(diagnostics.String(), "pipewisp: on-shutdown hook failed:") {
			t.Fatalf("diagnostics = %q, want shutdown failure", diagnostics.String())
		}
	})

	t.Run("copy error", func(t *testing.T) {
		wantErr := errors.New("output unavailable")
		var diagnostics bytes.Buffer
		config := options{
			onShutdown:       failingHookCommand("shutdown"),
			onShutdownSet:    true,
			ignoreHookErrors: true,
		}

		if got := runWithOptions(config, strings.NewReader("input"), errorWriter{err: wantErr}, &diagnostics); got != 1 {
			t.Fatalf("runWithOptions() exit code = %d, want 1; diagnostics = %q", got, diagnostics.String())
		}
		if got := diagnostics.String(); !strings.Contains(got, wantErr.Error()) || !strings.Contains(got, "pipewisp: on-shutdown hook failed:") {
			t.Fatalf("diagnostics = %q, want copy error and shutdown failure", got)
		}
	})
}

func TestIgnoreReadyHookTimeoutContinuesLifecycle(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{
		onReady:          hookSleepCommand(time.Second),
		onReadySet:       true,
		hookTimeout:      20 * time.Millisecond,
		hookTimeoutSet:   true,
		ignoreHookErrors: true,
	}

	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "pipewisp: on-ready hook failed:") || !strings.Contains(got, "timed out") {
		t.Fatalf("diagnostics = %q, want hook failure and timeout", got)
	}
}
