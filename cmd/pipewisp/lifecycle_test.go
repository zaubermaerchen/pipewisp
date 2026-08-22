package main

// This file tests hook ordering, stream isolation, and lifecycle error handling.

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestRunWithHooksInOrder(t *testing.T) {
	var events []string
	input := []byte("input")
	out := eventWriter{label: "copy", events: &events}
	diagnostics := eventWriter{label: "diagnostics", events: &events}
	config := options{
		on:     hookOutputCommand("on"),
		onSet:  true,
		off:    hookOutputCommand("off"),
		offSet: true,
	}

	if got := runWithOptions(config, bytes.NewReader(input), out, diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	got := strings.NewReplacer("\r", "", "\n", "").Replace(strings.Join(events, ""))
	if want := "diagnostics:oncopy:inputdiagnostics:off"; got != want {
		t.Fatalf("event sequence = %q, want %q", got, want)
	}
}

func TestOnFailurePreventsCopyAndOff(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{
		on:     failingHookCommand("on"),
		onSet:  true,
		off:    hookOutputCommand("off"),
		offSet: true,
	}

	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if output.Len() != 0 {
		t.Fatalf("copy output = %q, want empty", output.String())
	}
	if strings.Contains(diagnostics.String(), "off") {
		t.Fatalf("diagnostics = %q, off hook appears to have run", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "on hook failed") {
		t.Fatalf("diagnostics = %q, want on hook failure", diagnostics.String())
	}
}

func TestOffFailureReturnsErrorAfterCopy(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{off: failingHookCommand("off"), offSet: true}

	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	if !strings.Contains(diagnostics.String(), "off hook failed") {
		t.Fatalf("diagnostics = %q, want off hook failure", diagnostics.String())
	}
}

func TestOffRunsAfterCopyFailure(t *testing.T) {
	var diagnostics bytes.Buffer
	config := options{off: hookOutputCommand("off"), offSet: true}

	if got := runWithOptions(config, strings.NewReader("input"), errorWriter{err: fmt.Errorf("output unavailable")}, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if got := diagnostics.String(); !strings.Contains(got, "pipewisp: output unavailable\n") || !strings.Contains(got, "off") {
		t.Fatalf("diagnostics = %q, want copy error and off output", got)
	}
}

func TestHookOutputNeverReachesPassthroughOutput(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{on: hookOutputAndErrorCommand("hook-out", "hook-err"), onSet: true}

	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "hook-out") || !strings.Contains(got, "hook-err") {
		t.Fatalf("diagnostics = %q, want both hook outputs", got)
	}
}

func TestHookDoesNotConsumePassthroughInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cat is not a cmd.exe builtin")
	}

	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{on: "cat", onSet: true}

	if got := runWithOptions(config, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("hook diagnostics = %q, want empty", diagnostics.String())
	}
}

type eventWriter struct {
	label  string
	events *[]string
}

func (w eventWriter) Write(p []byte) (int, error) {
	*w.events = append(*w.events, w.label+":"+string(p))
	return len(p), nil
}
