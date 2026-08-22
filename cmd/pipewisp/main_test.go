package main

// This file verifies binary preservation and ordinary copy-error reporting.

import (
	"bytes"
	"errors"
	"io"
	"testing"
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

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ io.Writer = errorWriter{}
