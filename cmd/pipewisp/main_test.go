package main

// This file verifies binary preservation and ordinary copy-error reporting.

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestRunPreservesBinaryInput(t *testing.T) {
	input := []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff, '\n'}
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := run(bytes.NewReader(input), &output, &diagnostics); got != 0 {
		t.Fatalf("run() exit code = %d, want 0", got)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("run() output = %x, want %x", output.Bytes(), input)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("run() diagnostics = %q, want empty", diagnostics.String())
	}
}

func TestRunReportsCopyError(t *testing.T) {
	wantErr := errors.New("output unavailable")
	var diagnostics bytes.Buffer

	if got := run(bytes.NewReader([]byte("input")), errorWriter{err: wantErr}, &diagnostics); got != 1 {
		t.Fatalf("run() exit code = %d, want 1", got)
	}
	if got, want := diagnostics.String(), "pipewisp: output unavailable\n"; got != want {
		t.Fatalf("run() diagnostics = %q, want %q", got, want)
	}
}

func TestRunWithOptionsRequiresIdleSetForIdleMode(t *testing.T) {
	input := &writerToReader{data: []byte("input")}
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := runWithOptions(options{idle: time.Second}, input, &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if !input.writeToCalled {
		t.Fatal("runWithOptions() bypassed the normal io.Copy path without idleSet")
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("runWithOptions() output = %q, want %q", got, want)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ io.Writer = errorWriter{}

type writerToReader struct {
	data          []byte
	writeToCalled bool
}

func (r *writerToReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func (r *writerToReader) WriteTo(w io.Writer) (int64, error) {
	r.writeToCalled = true
	n, err := w.Write(r.data)
	r.data = r.data[n:]
	return int64(n), err
}
