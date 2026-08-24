package main

// This file tests CLI error reporting and help behavior through the runtime entry point.

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLIReportsParseError(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := runCLI([]string{"--unknown"}, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runCLI() exit code = %d, want 1", got)
	}
	if output.Len() != 0 {
		t.Fatalf("runCLI() output = %q, want empty", output.String())
	}
	if got, want := diagnostics.String(), "pipewisp: unknown option --unknown\n"; got != want {
		t.Fatalf("runCLI() diagnostics = %q, want %q", got, want)
	}
}

func TestRunCLIHelp(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := runCLI([]string{"--help"}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "Usage: pipewisp [--verbose] [--on-ready COMMAND] [--on-first-data COMMAND] [--on-shutdown COMMAND] [--idle DURATION] [--on-idle COMMAND] [--on-resume COMMAND] [--hook-timeout DURATION] [--ignore-hook-errors]\n       pipewisp --version\n"; got != want {
		t.Fatalf("runCLI() output = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("runCLI() diagnostics = %q, want empty", diagnostics.String())
	}
}

func TestRunCLIVersion(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer

	if got := runCLI([]string{"--version"}, panicReader{}, &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "pipewisp devel\n"; got != want {
		t.Fatalf("runCLI() output = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("runCLI() diagnostics = %q, want empty", diagnostics.String())
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("version path read stdin")
}
