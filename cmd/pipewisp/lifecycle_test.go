package main

// This file tests hook ordering, stream isolation, and lifecycle error handling.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
		on:             hookOutputCommand("on"),
		onSet:          true,
		onFirstData:    hookOutputCommand("first"),
		onFirstDataSet: true,
		off:            hookOutputCommand("off"),
		offSet:         true,
	}

	if got := runWithOptions(config, bytes.NewReader(input), out, diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	got := strings.NewReplacer("\r", "", "\n", "").Replace(strings.Join(events, ""))
	if want := "diagnostics:ondiagnostics:firstcopy:inputdiagnostics:off"; got != want {
		t.Fatalf("event sequence = %q, want %q", got, want)
	}
}

func TestFirstDataHookRunsExactlyOnceAfterFirstInput(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: hookOutputCommand("first"), onFirstDataSet: true}
	input := &shortReadReader{chunks: [][]byte{[]byte("a"), []byte("bc")}}

	if got := runWithOptions(config, input, &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "abc"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	cleanDiagnostics := strings.ReplaceAll(diagnostics.String(), "\r", "")
	if got, want := strings.Count(cleanDiagnostics, "first"), 1; got != want {
		t.Fatalf("first-data hook output count = %d, want %d; diagnostics = %q", got, want, diagnostics.String())
	}
}

func TestFirstDataHookDoesNotRunForEmptyInput(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: hookOutputCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, strings.NewReader(""), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if output.Len() != 0 {
		t.Fatalf("copy output = %q, want empty", output.String())
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("diagnostics = %q, want empty", diagnostics.String())
	}
}

func TestFirstDataHookRunsBeforeBinaryOutput(t *testing.T) {
	input := []byte{0x00, 0x80, 0xff, 0x01}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: hookOutputCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, bytes.NewReader(input), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("copy output = %x, want %x", output.Bytes(), input)
	}
}

func TestFirstDataHookRunsWhenReadReturnsDataAndEOF(t *testing.T) {
	input := &dataAndErrorReader{data: []byte("input"), err: io.EOF}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: hookOutputCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, input, &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	got := strings.Trim(strings.ReplaceAll(diagnostics.String(), "\r", ""), "\n")
	if want := "first"; got != want {
		t.Fatalf("diagnostics = %q, want exactly one first-data output %q", got, want)
	}
}

func TestFirstDataHookRunsBeforeReadErrorAfterData(t *testing.T) {
	wantErr := errors.New("input unavailable")
	input := &dataAndErrorReader{data: []byte("input"), err: wantErr}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: hookOutputCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, input, &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	if !strings.Contains(diagnostics.String(), "first") || !strings.Contains(diagnostics.String(), wantErr.Error()) {
		t.Fatalf("diagnostics = %q, want first-data output and read error", diagnostics.String())
	}
}

func TestFirstDataHookRunsBeforeWriteError(t *testing.T) {
	var diagnostics bytes.Buffer
	config := options{onFirstData: hookOutputCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, strings.NewReader("input"), errorWriter{err: errors.New("output unavailable")}, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if !strings.Contains(diagnostics.String(), "first") || !strings.Contains(diagnostics.String(), "output unavailable") {
		t.Fatalf("diagnostics = %q, want first-data output and write error", diagnostics.String())
	}
}

func TestFirstDataHookFailurePreservesFirstBytesAndStillRunsOff(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	input := &shortReadReader{chunks: [][]byte{[]byte("first"), []byte("later")}}
	config := options{
		onFirstData:    failingHookCommand("first"),
		onFirstDataSet: true,
		off:            hookOutputCommand("off"),
		offSet:         true,
	}

	if got := runWithOptions(config, input, &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if got, want := output.String(), "first"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	cleanDiagnostics := strings.ReplaceAll(diagnostics.String(), "\r", "")
	if got := strings.Count(cleanDiagnostics, "on-first-data hook failed"); got != 1 {
		t.Fatalf("first-data failure diagnostic count = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if !strings.Contains(cleanDiagnostics, "off") {
		t.Fatalf("diagnostics = %q, want off hook output", diagnostics.String())
	}
}

func TestFirstDataHookFailureAlsoReportsReadError(t *testing.T) {
	wantErr := errors.New("input unavailable")
	input := &dataAndErrorReader{data: []byte("input"), err: wantErr}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: failingHookCommand("first"), onFirstDataSet: true}

	if got := runWithOptions(config, input, &output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1", got)
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	cleanDiagnostics := strings.ReplaceAll(diagnostics.String(), "\r", "")
	if got := strings.Count(cleanDiagnostics, "on-first-data hook failed"); got != 1 {
		t.Fatalf("first-data failure diagnostic count = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if got := strings.Count(cleanDiagnostics, wantErr.Error()); got != 1 {
		t.Fatalf("read failure diagnostic count = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
}

func TestFirstDataHookDoesNotConsumePassthroughInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cat is not a cmd.exe builtin")
	}

	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{onFirstData: "cat", onFirstDataSet: true}

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

type shortReadReader struct {
	chunks [][]byte
	index  int
}

type dataAndErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *dataAndErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
}

func (r *shortReadReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	n := copy(p, chunk)
	return n, nil
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
