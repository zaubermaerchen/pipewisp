package main

// This file tests first-read detection independently of shell hook execution.

import (
	"io"
	"testing"
)

func TestFirstDataReaderIgnoresEmptySuccessfulRead(t *testing.T) {
	source := &scriptedReader{results: []scriptedRead{
		{},
		{data: []byte("input"), err: io.EOF},
	}}
	hookCalls := 0
	reader := &firstDataReader{reader: source, hook: func() error {
		hookCalls++
		return nil
	}}
	buffer := make([]byte, 16)

	if n, err := reader.Read(buffer); n != 0 || err != nil {
		t.Fatalf("first Read() = (%d, %v), want (0, nil)", n, err)
	}
	if hookCalls != 0 {
		t.Fatalf("hook calls after empty read = %d, want 0", hookCalls)
	}
	if n, err := reader.Read(buffer); n != 5 || err != io.EOF {
		t.Fatalf("second Read() = (%d, %v), want (5, EOF)", n, err)
	}
	if hookCalls != 1 {
		t.Fatalf("hook calls after data = %d, want 1", hookCalls)
	}
}

func TestFirstDataReaderRunsHookBeforeReadingAgain(t *testing.T) {
	source := &scriptedReader{results: []scriptedRead{
		{data: []byte("a")},
		{data: []byte("b"), err: io.EOF},
	}}
	reader := &firstDataReader{reader: source, hook: func() error {
		if source.reads != 1 {
			t.Fatalf("source reads during hook = %d, want 1", source.reads)
		}
		return nil
	}}
	buffer := make([]byte, 1)

	if n, err := reader.Read(buffer); n != 1 || err != nil || buffer[0] != 'a' {
		t.Fatalf("first Read() = (%d, %v, %q), want (1, nil, %q)", n, err, buffer[:n], "a")
	}
	if source.reads != 1 {
		t.Fatalf("source reads after first data = %d, want 1", source.reads)
	}
	if n, err := reader.Read(buffer); n != 1 || err != io.EOF || buffer[0] != 'b' {
		t.Fatalf("second Read() = (%d, %v, %q), want (1, EOF, %q)", n, err, buffer[:n], "b")
	}
}

type scriptedRead struct {
	data []byte
	err  error
}

type scriptedReader struct {
	results []scriptedRead
	reads   int
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	result := r.results[r.reads]
	r.reads++
	return copy(p, result.data), result.err
}
