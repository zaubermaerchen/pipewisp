package main

// This file verifies verbose option parsing and lifecycle/hook diagnostics.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerboseReporterSeparatesHookOutput(t *testing.T) {
	for _, output := range []string{"", "hook\n", "hook"} {
		var diagnostics bytes.Buffer
		r := newVerboseReporter(&diagnostics, true)
		_, _ = r.Write([]byte(output))
		r.event(hookContext{event: "idle"})
		if !strings.HasSuffix(diagnostics.String(), "pipewisp: type=event event=idle bytes=0 elapsed_ms=0\n") {
			t.Fatalf("output %q produced %q", output, diagnostics.String())
		}
		if output != "" && !strings.Contains(diagnostics.String(), output+"\npipewisp:") && output != "hook\n" {
			t.Fatalf("output %q was not separated: %q", output, diagnostics.String())
		}
	}
}

func TestVerboseReporterUsesExactV03FieldOrder(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(&diagnostics, true)
	reporter.event(hookContext{event: "resume", bytes: 12, durationMilliseconds: 34})
	reporter.event(hookContext{event: "shutdown", reason: "eof", bytes: 56, durationMilliseconds: 78})
	reporter.event(hookContext{event: "shutdown", bytes: 90, durationMilliseconds: 12})

	want := "pipewisp: type=event event=resume bytes=12 elapsed_ms=34\n" +
		"pipewisp: type=event event=shutdown reason=eof bytes=56 elapsed_ms=78\n" +
		"pipewisp: type=event event=shutdown bytes=90 elapsed_ms=12\n"
	if got := diagnostics.String(); got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestVerboseHookStartErrorShape(t *testing.T) {
	var diagnostics bytes.Buffer
	r := newVerboseReporter(&diagnostics, true)
	start := time.Now()
	r.hookEnd("ready", start, &hookStartError{err: errors.New("start")})
	if !strings.Contains(diagnostics.String(), "state=error phase=start duration_ms=0") {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
}

func TestCanonicalSignals(t *testing.T) {
	if got := canonicalSignal(os.Interrupt); got != "SIGINT" {
		t.Fatalf("canonicalSignal(interrupt) = %q", got)
	}
}

func TestVerboseIndependentSignalIsExitSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal command")
	}
	var diagnostics bytes.Buffer
	if err := runHookWithContext("on-ready", "kill -TERM $$", hookContext{event: "ready"}, newVerboseReporter(&diagnostics, true)); err == nil {
		t.Fatal("signal hook unexpectedly succeeded")
	}
	got := diagnostics.String()
	if !strings.Contains(got, "state=exit signal=SIGTERM") {
		t.Fatalf("diagnostics = %q", got)
	}
	if strings.Contains(got, "state=interrupted") {
		t.Fatalf("independent signal marked interrupted: %q", got)
	}
}

func TestProcessStateKillClassification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal classification")
	}
	for _, tc := range []struct {
		name, command string
		killed        bool
	}{
		{"normal", "exit 0", false},
		{"independent-term", "kill -TERM $$", false},
		{"kill", "kill -KILL $$", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", tc.command)
			_ = cmd.Run()
			if got := processStateKilledByPipewisp(cmd.ProcessState); got != tc.killed {
				t.Fatalf("processStateKilledByPipewisp() = %v, want %v", got, tc.killed)
			}
		})
	}
}

func TestParseVerbose(t *testing.T) {
	got, help, err := parseArgs([]string{"--verbose"})
	if err != nil || help || !got.verbose {
		t.Fatalf("parseArgs(--verbose) = %#v, help=%v, err=%v", got, help, err)
	}
	if _, _, err := parseArgs([]string{"--verbose", "--verbose"}); err == nil || !strings.Contains(err.Error(), "--verbose specified more than once") {
		t.Fatalf("duplicate --verbose error = %v", err)
	}
	for _, args := range [][]string{{"--verbose=true"}, {"--verbose="}, {"--verbose", "false"}, {"-v"}} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) succeeded, want error", args)
		}
	}
	for _, args := range [][]string{{"--help", "--verbose"}, {"--verbose", "--help"}, {"--version", "--verbose"}, {"--verbose", "--version"}} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) succeeded, want combination error", args)
		}
	}
}

func TestVerboseWithoutHooksReportsLifecycleAndPreservesStdout(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if got := runCLI([]string{"--verbose"}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() status = %d; diagnostics=%q", got, diagnostics.String())
	}
	if output.String() != "input" {
		t.Fatalf("stdout = %q, want input", output.String())
	}
	got := diagnostics.String()
	for _, want := range []string{
		"pipewisp: type=event event=ready bytes=0 elapsed_ms=0",
		"pipewisp: type=event event=first-data bytes=0 elapsed_ms=",
		"pipewisp: type=event event=shutdown reason=eof bytes=5 elapsed_ms=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostics=%q, missing %q", got, want)
		}
	}
}

func TestVerboseFirstDataReadBoundaries(t *testing.T) {
	wantErr := errors.New("read failed")
	tests := []struct {
		name          string
		input         io.Reader
		wantStatus    int
		wantOutput    string
		wantFirstData int
		wantReason    string
	}{
		{name: "empty EOF", input: strings.NewReader(""), wantStatus: 0, wantFirstData: 0, wantReason: "eof"},
		{name: "zero then data plus EOF", input: &scriptedReader{results: []scriptedRead{{}, {data: []byte("data"), err: io.EOF}}}, wantStatus: 0, wantOutput: "data", wantFirstData: 1, wantReason: "eof"},
		{name: "data plus EOF", input: &dataAndErrorReader{data: []byte("data"), err: io.EOF}, wantStatus: 0, wantOutput: "data", wantFirstData: 1, wantReason: "eof"},
		{name: "data plus error", input: &dataAndErrorReader{data: []byte("data"), err: wantErr}, wantStatus: 1, wantOutput: "data", wantFirstData: 1, wantReason: "io-error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			status := runWithOptions(options{verbose: true}, tt.input, &output, &diagnostics)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d; diagnostics=%q", status, tt.wantStatus, diagnostics.String())
			}
			if got := output.String(); got != tt.wantOutput {
				t.Fatalf("stdout = %q, want %q", got, tt.wantOutput)
			}
			if got := strings.Count(diagnostics.String(), "type=event event=first-data "); got != tt.wantFirstData {
				t.Fatalf("first-data records = %d, want %d; diagnostics=%q", got, tt.wantFirstData, diagnostics.String())
			}
			if !strings.Contains(diagnostics.String(), "type=event event=shutdown reason="+tt.wantReason+" ") {
				t.Fatalf("diagnostics=%q, missing shutdown reason %q", diagnostics.String(), tt.wantReason)
			}
		})
	}
}

func TestVerboseFirstDataPrecedesFirstOutputWrite(t *testing.T) {
	events := make(chan string, 8)
	output := &eventChannelWriter{label: "output", events: events}
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}

	if status := runWithOptions(options{verbose: true}, strings.NewReader("data"), output, diagnostics); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	var observed []string
	for len(events) > 0 {
		observed = append(observed, <-events)
	}
	firstData := eventIndex(observed, "type=event event=first-data")
	firstWrite := eventIndex(observed, "output:data")
	if firstData < 0 || firstWrite < 0 || firstData > firstWrite {
		t.Fatalf("event order = %#v, want first-data before first output", observed)
	}
}

func TestVerboseDiagnosticWriteFailuresDoNotChangeStreamOutcome(t *testing.T) {
	tests := []struct {
		name        string
		diagnostics io.Writer
	}{
		{name: "error", diagnostics: errorWriter{err: errors.New("diagnostics unavailable")}},
		{name: "short write", diagnostics: &partialWriter{max: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			if status := runWithOptions(options{verbose: true}, strings.NewReader("input"), &output, tt.diagnostics); status != 0 {
				t.Fatalf("status = %d, want 0", status)
			}
			if got := output.String(); got != "input" {
				t.Fatalf("stdout = %q, want input", got)
			}
		})
	}
}

func TestVerboseDiagnosticWriteIsSynchronous(t *testing.T) {
	diagnostics := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var output bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithOptions(options{verbose: true}, strings.NewReader("input"), &output, diagnostics)
	}()

	select {
	case <-diagnostics.entered:
	case <-time.After(time.Second):
		t.Fatal("ready diagnostic did not reach writer")
	}
	if output.Len() != 0 {
		t.Fatalf("stdout advanced while ready diagnostic was blocked: %q", output.String())
	}
	close(diagnostics.release)
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("status = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions did not finish after diagnostics release")
	}
	if got := output.String(); got != "input" {
		t.Fatalf("stdout = %q, want input", got)
	}
}

func TestVerboseObservationAndDisabledFastPath(t *testing.T) {
	t.Run("verbose observation bypasses source WriterTo", func(t *testing.T) {
		input := &writerToReader{data: []byte("input")}
		var output, diagnostics bytes.Buffer
		if status := runWithOptions(options{verbose: true}, input, &output, &diagnostics); status != 0 {
			t.Fatalf("status = %d, want 0", status)
		}
		if input.writeToCalled {
			t.Fatal("verbose first-data observation unexpectedly used source WriterTo")
		}
		if got := output.String(); got != "input" {
			t.Fatalf("stdout = %q, want input", got)
		}
	})

	t.Run("disabled retains source WriterTo", func(t *testing.T) {
		input := &writerToReader{data: []byte("input")}
		var output, diagnostics bytes.Buffer
		if status := runWithOptions(options{}, input, &output, &diagnostics); status != 0 {
			t.Fatalf("status = %d, want 0", status)
		}
		if !input.writeToCalled {
			t.Fatal("verbose-disabled copy did not retain source WriterTo")
		}
	})
}

func TestVerboseStrictReadyFailureHasNoShutdownEvent(t *testing.T) {
	var output, diagnostics bytes.Buffer
	opts := options{verbose: true, onReady: failingHookCommand("ready-failed"), onReadySet: true}
	if status := runWithOptions(opts, strings.NewReader("input"), &output, &diagnostics); status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
	if strings.Contains(diagnostics.String(), "type=event event=shutdown") {
		t.Fatalf("strict ready failure emitted shutdown: %q", diagnostics.String())
	}
}

func TestVerboseFirstDataFailureUsesReasonlessShutdown(t *testing.T) {
	var output, diagnostics bytes.Buffer
	opts := options{verbose: true, onFirstData: failingHookCommand("first-data-failed"), onFirstDataSet: true}
	if status := runWithOptions(opts, strings.NewReader("input"), &output, &diagnostics); status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
	if got := output.String(); got != "input" {
		t.Fatalf("stdout = %q, want input", got)
	}
	for _, line := range strings.Split(diagnostics.String(), "\n") {
		if strings.Contains(line, "type=event event=shutdown") && strings.Contains(line, " reason=") {
			t.Fatalf("reason-less shutdown included reason: %q", line)
		}
	}
}

func TestVerboseIdleModeReportsBothTransitionsWithOneSidedHooks(t *testing.T) {
	tests := []struct {
		name         string
		opts         options
		hookEvent    string
		hookOutput   string
		absentEvent  string
		absentOutput string
	}{
		{
			name:         "idle hook only",
			opts:         options{idle: 5 * time.Millisecond, idleSet: true, onIdle: hookOutputCommand("idle-hook"), onIdleSet: true, verbose: true},
			hookEvent:    "idle",
			hookOutput:   "idle-hook",
			absentEvent:  "resume",
			absentOutput: "resume-hook",
		},
		{
			name:         "resume hook only",
			opts:         options{idle: 5 * time.Millisecond, idleSet: true, onResume: hookOutputCommand("resume-hook"), onResumeSet: true, verbose: true},
			hookEvent:    "resume",
			hookOutput:   "resume-hook",
			absentEvent:  "idle",
			absentOutput: "idle-hook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
			var output, diagnosticBuffer bytes.Buffer
			events := make(chan string, 32)
			diagnostics := io.MultiWriter(&diagnosticBuffer, &eventChannelWriter{label: "diagnostics", events: events})
			done := make(chan int, 1)
			go func() {
				done <- runWithOptions(tt.opts, input, &output, diagnostics)
			}()

			waitForEvent(t, events, "type=event event=idle")
			input.releaseSecond()
			select {
			case status := <-done:
				if status != 0 {
					t.Fatalf("status = %d, want 0; diagnostics=%q", status, diagnosticBuffer.String())
				}
			case <-time.After(time.Second):
				t.Fatal("runWithOptions did not finish")
			}

			got := diagnosticBuffer.String()
			if strings.Count(got, "type=event event=idle ") != 1 || strings.Count(got, "type=event event=resume ") != 1 {
				t.Fatalf("diagnostics=%q, want exactly one idle and resume event", got)
			}
			if strings.Count(got, "type=hook event="+tt.hookEvent+" state=start") != 1 {
				t.Fatalf("diagnostics=%q, want one %s hook start record", got, tt.hookEvent)
			}
			if strings.Count(got, "type=hook event="+tt.hookEvent+" state=exit exit_code=0") != 1 {
				t.Fatalf("diagnostics=%q, want one %s hook terminal record", got, tt.hookEvent)
			}
			if !strings.Contains(got, tt.hookOutput) {
				t.Fatalf("diagnostics=%q, want %s hook output", got, tt.hookOutput)
			}
			if strings.Contains(got, "type=hook event="+tt.absentEvent) {
				t.Fatalf("diagnostics=%q, did not expect %s hook record", got, tt.absentEvent)
			}
			if strings.Contains(got, tt.absentOutput) {
				t.Fatalf("diagnostics=%q, did not expect %s hook output", got, tt.absentOutput)
			}
			if output.String() != "ab" {
				t.Fatalf("stdout = %q, want ab", output.String())
			}
		})
	}
}

func TestVerbosePassiveIdlePreservesCyclesBytesAndStdout(t *testing.T) {
	input := &threeStageReader{
		stages: [][]byte{[]byte("a"), []byte("b"), []byte("c"), nil},
		gates:  []chan struct{}{nil, make(chan struct{}), make(chan struct{}), nil},
	}
	events := make(chan string, 32)
	var output, diagnosticBuffer bytes.Buffer
	diagnostics := io.MultiWriter(&diagnosticBuffer, &eventChannelWriter{label: "diagnostics", events: events})
	config := options{idle: 5 * time.Millisecond, idleSet: true, verbose: true}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()

	waitForEvent(t, events, "type=event event=idle bytes=1")
	close(input.gates[1])
	waitForEvent(t, events, "type=event event=resume bytes=1")
	waitForEvent(t, events, "type=event event=idle bytes=2")
	close(input.gates[2])
	waitForEvent(t, events, "type=event event=resume bytes=2")

	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() status = %d; diagnostics=%q", status, diagnosticBuffer.String())
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}

	wantEvents := []string{
		"type=event event=ready bytes=0 elapsed_ms=0",
		"type=event event=first-data bytes=0 elapsed_ms=",
		"type=event event=idle bytes=1 elapsed_ms=",
		"type=event event=resume bytes=1 elapsed_ms=",
		"type=event event=idle bytes=2 elapsed_ms=",
		"type=event event=resume bytes=2 elapsed_ms=",
		"type=event event=shutdown reason=eof bytes=3 elapsed_ms=",
	}
	lines := strings.Split(strings.TrimSpace(diagnosticBuffer.String()), "\n")
	if len(lines) != len(wantEvents) {
		t.Fatalf("diagnostic lines = %q, want %d lifecycle records", lines, len(wantEvents))
	}
	for i, want := range wantEvents {
		if !strings.Contains(lines[i], want) {
			t.Errorf("diagnostic line %d = %q, want %q", i, lines[i], want)
		}
	}
	if strings.Contains(diagnosticBuffer.String(), "type=hook") {
		t.Fatalf("passive idle emitted hook record: %q", diagnosticBuffer.String())
	}
	if got, want := output.String(), "abc"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestVerbosePassiveIdleEmptyInput(t *testing.T) {
	var output, diagnostics bytes.Buffer
	config := options{idle: 5 * time.Millisecond, idleSet: true, verbose: true}
	if status := runWithOptions(config, strings.NewReader(""), &output, &diagnostics); status != 0 {
		t.Fatalf("runWithOptions() status = %d; diagnostics=%q", status, diagnostics.String())
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
	got := diagnostics.String()
	if !strings.Contains(got, "type=event event=ready bytes=0 elapsed_ms=0") ||
		!strings.Contains(got, "type=event event=shutdown reason=eof bytes=0 elapsed_ms=") {
		t.Fatalf("diagnostics = %q, want ready and EOF shutdown", got)
	}
	for _, event := range []string{"first-data", "idle", "resume", "type=hook"} {
		if strings.Contains(got, event) {
			t.Fatalf("diagnostics = %q, must not contain %q", got, event)
		}
	}
}

func TestVerbosePassiveIdleEOFWhileIdleDoesNotResume(t *testing.T) {
	input := &gatedEOFReader{first: []byte("a"), eofReady: make(chan struct{})}
	var output, diagnosticBuffer bytes.Buffer
	events := make(chan string, 16)
	diagnostics := io.MultiWriter(&diagnosticBuffer, &eventChannelWriter{label: "diagnostics", events: events})
	config := options{idle: 5 * time.Millisecond, idleSet: true, verbose: true}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()

	waitForEvent(t, events, "type=event event=idle bytes=1")
	input.releaseEOF()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() status = %d; diagnostics=%q", status, diagnosticBuffer.String())
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "a"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	got := diagnosticBuffer.String()
	if !strings.Contains(got, "type=event event=shutdown reason=eof bytes=1 elapsed_ms=") {
		t.Fatalf("diagnostics = %q, want EOF shutdown", got)
	}
	if strings.Contains(got, "event=resume") || strings.Contains(got, "type=hook") {
		t.Fatalf("diagnostics = %q, EOF while idle must not resume or run hooks", got)
	}
}

func TestVerboseHookSuccessAndFailureRecordsOutcome(t *testing.T) {
	var diagnostics bytes.Buffer
	r := newVerboseReporter(&diagnostics, true)
	if err := runHookWithContext("on-ready", "true", hookContext{event: "ready"}, r); err != nil {
		t.Fatal(err)
	}
	if err := runHookWithContext("on-idle", "false", hookContext{event: "idle"}, r); err == nil {
		t.Fatal("false hook unexpectedly succeeded")
	}
	got := diagnostics.String()
	if strings.Count(got, "type=hook event=ready state=start") != 1 || !strings.Contains(got, "type=hook event=ready state=exit exit_code=0") {
		t.Fatalf("success records = %q", got)
	}
	if strings.Count(got, "type=hook event=idle state=start") != 1 || !strings.Contains(got, "type=hook event=idle state=exit exit_code=1") {
		t.Fatalf("failure records = %q", got)
	}
}

func TestVerboseHookTimeoutUsesObservedDurationAndOrdering(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(&diagnostics, true)
	timeout := 20 * time.Millisecond
	err := runHookWithContextAndTracker("on-idle", hookSleepCommand(time.Second), hookContext{event: "idle"}, reporter, timeout, nil, false)
	var timeoutErr *hookTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("error = %v, want hookTimeoutError", err)
	}

	got := diagnostics.String()
	startIndex := strings.Index(got, "type=hook event=idle state=start")
	terminalIndex := strings.Index(got, "type=hook event=idle state=timeout duration_ms=")
	failureIndex := strings.Index(got, "pipewisp: on-idle hook failed:")
	if startIndex < 0 || terminalIndex < startIndex || failureIndex < terminalIndex {
		t.Fatalf("diagnostic order = %q", got)
	}
	if strings.Count(got, "type=hook event=idle state=timeout") != 1 {
		t.Fatalf("timeout terminal count in %q", got)
	}
	if duration := verboseDurationAfter(t, got, terminalIndex); duration < timeout.Milliseconds() {
		t.Fatalf("duration_ms = %d, want at least %d", duration, timeout.Milliseconds())
	}
}

func TestVerboseIgnoreHookErrorsDoesNotChangeOutcomeRecord(t *testing.T) {
	var diagnostics bytes.Buffer
	reporter := newVerboseReporter(&diagnostics, true)
	if err := runHookWithContextAndTracker("on-ready", failingHookCommand("failed"), hookContext{event: "ready"}, reporter, 0, nil, true); err != nil {
		t.Fatalf("ignored hook error = %v", err)
	}
	got := diagnostics.String()
	if !strings.Contains(got, "type=hook event=ready state=exit exit_code=7 duration_ms=") {
		t.Fatalf("diagnostics = %q", got)
	}
	if !strings.Contains(got, "pipewisp: on-ready hook failed:") {
		t.Fatalf("diagnostics = %q, missing existing failure diagnostic", got)
	}
}

func verboseDurationAfter(t *testing.T, diagnostic string, recordIndex int) int64 {
	t.Helper()
	const field = "duration_ms="
	start := strings.Index(diagnostic[recordIndex:], field)
	if start < 0 {
		t.Fatalf("diagnostic = %q, missing duration_ms", diagnostic)
	}
	start += recordIndex + len(field)
	end := start
	for end < len(diagnostic) && diagnostic[end] >= '0' && diagnostic[end] <= '9' {
		end++
	}
	duration, err := strconv.ParseInt(diagnostic[start:end], 10, 64)
	if err != nil {
		t.Fatalf("parse duration from %q: %v", diagnostic[start:end], err)
	}
	return duration
}
