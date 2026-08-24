package main

// This file verifies instance-name parsing and source-aware diagnostics.

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestParseArgsName(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "separated", args: []string{"--name", "relay"}, want: "relay"},
		{name: "equals", args: []string{"--name=relay"}, want: "relay"},
		{name: "leading hyphen equals", args: []string{"--name=-relay"}, want: "-relay"},
		{name: "unicode and embedded whitespace", args: []string{"--name", "中 継 🚚"}, want: "中 継 🚚"},
		{name: "percent is data", args: []string{"--name=%s"}, want: "%s"},
		{name: "zero width joiner emoji", args: []string{"--name", "👨‍👩‍👧‍👦"}, want: "👨‍👩‍👧‍👦"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, help, err := parseArgs(tt.args)
			if err != nil {
				t.Fatalf("parseArgs() error = %v", err)
			}
			if help {
				t.Fatal("parseArgs() help = true, want false")
			}
			if !got.nameSet || got.name != tt.want {
				t.Fatalf("parseArgs() name = (%q, %v), want (%q, true)", got.name, got.nameSet, tt.want)
			}
		})
	}
}

func TestParseArgsRejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing", args: []string{"--name"}},
		{name: "missing before option", args: []string{"--name", "--verbose"}},
		{name: "empty equals", args: []string{"--name="}},
		{name: "empty separated", args: []string{"--name", ""}},
		{name: "whitespace only", args: []string{"--name", " \t\u2003"}},
		{name: "leading hyphen separated", args: []string{"--name", "-relay"}},
		{name: "duplicate separated", args: []string{"--name", "first", "--name", "second"}},
		{name: "duplicate mixed forms", args: []string{"--name=first", "--name", "second"}},
		{name: "invalid utf8", args: []string{"--name", string([]byte{0xff})}},
		{name: "control", args: []string{"--name", "relay\x00"}},
		{name: "unicode control", args: []string{"--name", "relay\u0085name"}},
		{name: "line separator", args: []string{"--name", "relay\u2028name"}},
		{name: "paragraph separator", args: []string{"--name", "relay\u2029name"}},
		{name: "bidi arabic letter mark", args: []string{"--name", "relay\u061cname"}},
		{name: "bidi left-to-right mark", args: []string{"--name", "relay\u200ename"}},
		{name: "bidi right-to-left mark", args: []string{"--name", "relay\u200fname"}},
		{name: "bidi embedding", args: []string{"--name", "relay\u202aname"}},
		{name: "bidi override", args: []string{"--name", "relay\u202ename"}},
		{name: "bidi isolate", args: []string{"--name", "relay\u2066name"}},
		{name: "bidi pop isolate", args: []string{"--name", "relay\u2069name"}},
		{name: "left bracket", args: []string{"--name", "relay[name"}},
		{name: "right bracket", args: []string{"--name", "relay]name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, help, err := parseArgs(tt.args); err == nil || help {
				t.Fatalf("parseArgs() = help=%v, err=%v, want validation error", help, err)
			}
		})
	}
}

func TestParseArgsRejectsNameWithStandaloneOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--name", "relay", "--help"},
		{"--help", "--name", "relay"},
		{"--name=relay", "--version"},
		{"--version", "--name=relay"},
	} {
		if _, help, err := parseArgs(args); err == nil || help {
			t.Errorf("parseArgs(%v) = help=%v, err=%v, want combination error", args, help, err)
		}
	}
}

func TestRunCLINameParseErrorsUseDefaultPrefix(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--name", "relay", "--unknown"}, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runCLI() status = %d, want 1", got)
	}
	if got, want := diagnostics.String(), "pipewisp: unknown option --unknown\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestRunCLIUsesNamedRuntimePrefix(t *testing.T) {
	var diagnostics bytes.Buffer
	if got := runCLI([]string{"--name", "relay"}, strings.NewReader("input"), errorWriter{err: errors.New("output unavailable")}, &diagnostics); got != 1 {
		t.Fatalf("runCLI() status = %d, want 1", got)
	}
	if got, want := diagnostics.String(), "pipewisp[relay]: output unavailable\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestRunCLIUsesNamedPrefixForReadAndShutdownErrors(t *testing.T) {
	t.Run("read error", func(t *testing.T) {
		wantErr := errors.New("input unavailable")
		var output, diagnostics bytes.Buffer
		if got := runCLI([]string{"--name=relay"}, &dataAndErrorReader{err: wantErr}, &output, &diagnostics); got != 1 {
			t.Fatalf("runCLI() status = %d, want 1", got)
		}
		if got, want := diagnostics.String(), "pipewisp[relay]: input unavailable\n"; got != want {
			t.Fatalf("diagnostics = %q, want %q", got, want)
		}
	})

	t.Run("shutdown hook failure", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		if got := runCLI([]string{"--name=relay", "--on-shutdown", failingHookCommand("shutdown-failed")}, strings.NewReader("input"), &output, &diagnostics); got != 1 {
			t.Fatalf("runCLI() status = %d, want 1", got)
		}
		if got := output.String(); got != "input" {
			t.Fatalf("stdout = %q, want input", got)
		}
		if got := diagnostics.String(); !strings.Contains(got, "pipewisp[relay]: on-shutdown hook failed:") {
			t.Fatalf("diagnostics = %q, want named shutdown failure", got)
		}
	})
}

func TestRunCLINameOnlyPreservesStreamAndFastPath(t *testing.T) {
	input := &writerToReader{data: []byte("binary\x00\xff")}
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--name=relay"}, input, &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if !bytes.Equal(output.Bytes(), []byte("binary\x00\xff")) {
		t.Fatalf("stdout = %x, want binary bytes", output.Bytes())
	}
	if !input.writeToCalled {
		t.Fatal("--name alone bypassed source WriterTo fast path")
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("diagnostics = %q, want empty", diagnostics.String())
	}
}

func TestRunCLIUsesNamedVerbosePrefixWithoutFormattingName(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--name=%s", "--verbose"}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	for _, line := range strings.Split(strings.TrimSuffix(diagnostics.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "pipewisp[%s]: ") {
			t.Fatalf("diagnostic line = %q, want named prefix", line)
		}
	}
}

func TestRunCLIForwardsNamedHookOutputWithoutPrefix(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--name", "relay", "--verbose", "--on-ready", hookOutputAndErrorCommand("hook-out", "hook-err")}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got := output.String(); got != "input" {
		t.Fatalf("stdout = %q, want input", got)
	}
	got := diagnostics.String()
	if !strings.Contains(got, "hook-out") || !strings.Contains(got, "hook-err") {
		t.Fatalf("diagnostics = %q, want both hook streams", got)
	}
	if strings.Contains(got, "pipewisp[relay]: hook-out") || strings.Contains(got, "pipewisp[relay]: hook-err") {
		t.Fatalf("hook output was prefixed: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if (strings.Contains(line, "type=hook") || strings.Contains(line, "type=event")) && !strings.HasPrefix(line, "pipewisp[relay]: ") {
			t.Fatalf("pipewisp-generated record lacks named prefix: %q", line)
		}
	}
}

func TestNamedVerboseReporterPreservesHookSeparator(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newNamedVerboseReporter(&diagnostics, "relay", true)
	_, _ = reporter.Write([]byte("hook-output"))
	reporter.hookStart("ready")

	if got, want := diagnostics.String(), "hook-output\npipewisp[relay]: type=hook event=ready state=start\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestRunCLINamedHookFailureUsesNamedPrefixWithoutVerbose(t *testing.T) {
	var output, diagnostics bytes.Buffer
	command := hookOutputAndErrorCommand("hook-out", "hook-err")
	if runtime.GOOS == "windows" {
		command += " & exit /b 7"
	} else {
		command += "; exit 7"
	}
	if got := runCLI([]string{"--name=relay", "--on-ready", command}, strings.NewReader("input"), &output, &diagnostics); got != 1 {
		t.Fatalf("runCLI() status = %d, want 1", got)
	}
	if got, want := output.String(), ""; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	got := diagnostics.String()
	if !strings.HasPrefix(got, "hook-out") && !strings.HasPrefix(got, "hook-err") {
		t.Fatalf("diagnostics = %q, want raw hook output first", got)
	}
	if !strings.Contains(got, "pipewisp[relay]: on-ready hook failed:") {
		t.Fatalf("diagnostics = %q, want named hook failure", got)
	}
	if strings.Contains(got, "pipewisp[relay]: hook-out") || strings.Contains(got, "pipewisp[relay]: hook-err") {
		t.Fatalf("hook output was prefixed: %q", got)
	}
}

func TestRunCLINameDoesNotChangeHookEnvironment(t *testing.T) {
	t.Setenv("PIPEWISP_NAME", "parent-name")
	command := `printf '%s' "$PIPEWISP_NAME"`
	if runtime.GOOS == "windows" {
		command = "echo %PIPEWISP_NAME%"
	}
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--name", "relay", "--on-ready", command}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got := strings.TrimSpace(diagnostics.String()); got != "parent-name" {
		t.Fatalf("hook environment PIPEWISP_NAME = %q, want parent-name", got)
	}
}

func TestRunCLINameDoesNotInjectHookEnvironment(t *testing.T) {
	previous, present := os.LookupEnv("PIPEWISP_NAME")
	if err := os.Unsetenv("PIPEWISP_NAME"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv("PIPEWISP_NAME", previous)
		} else {
			_ = os.Unsetenv("PIPEWISP_NAME")
		}
	})

	command := `if [ "${PIPEWISP_NAME+x}" = x ]; then printf set; else printf unset; fi`
	if runtime.GOOS == "windows" {
		command = "if defined PIPEWISP_NAME (echo set) else (echo unset)"
	}
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--name=relay", "--on-ready", command}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got := strings.TrimSpace(diagnostics.String()); got != "unset" {
		t.Fatalf("hook environment PIPEWISP_NAME = %q, want unset", got)
	}
}
