package main

// This file verifies hook lifecycle context values and stdout byte accounting.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHookEnvironmentOverridesOwnedKeysAndPreservesParentValues(t *testing.T) {
	t.Setenv("PATH", "/test/path")
	t.Setenv("PIPEWISP_EVENT", "parent-event")
	t.Setenv("PIPEWISP_REASON", "parent-reason")
	t.Setenv("PIPEWISP_BYTES", "parent-bytes")
	t.Setenv("PIPEWISP_DURATION_MILLISECONDS", "parent-duration")
	t.Setenv("PIPEWISP_CUSTOM", "preserved")

	environment := hookEnvironment(hookContext{
		event:                "resume",
		reason:               "",
		bytes:                17,
		durationMilliseconds: 23,
	})
	values := environmentValues(environment)

	for _, key := range []string{hookEnvEvent, hookEnvBytes, hookEnvDurationMilliseconds, hookEnvReason} {
		if got := len(values[key]); got != map[string]int{hookEnvEvent: 1, hookEnvBytes: 1, hookEnvDurationMilliseconds: 1, hookEnvReason: 0}[key] {
			t.Fatalf("%s occurrences = %d, want %d; environment = %#v", key, got, map[string]int{hookEnvEvent: 1, hookEnvBytes: 1, hookEnvDurationMilliseconds: 1, hookEnvReason: 0}[key], environment)
		}
	}
	if got, want := values[hookEnvEvent][0], "resume"; got != want {
		t.Fatalf("%s = %q, want %q", hookEnvEvent, got, want)
	}
	if got, want := values[hookEnvBytes][0], "17"; got != want {
		t.Fatalf("%s = %q, want %q", hookEnvBytes, got, want)
	}
	if got, want := values[hookEnvDurationMilliseconds][0], "23"; got != want {
		t.Fatalf("%s = %q, want %q", hookEnvDurationMilliseconds, got, want)
	}
	if got, want := values["PATH"][0], "/test/path"; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	if got, want := values["PIPEWISP_CUSTOM"][0], "preserved"; got != want {
		t.Fatalf("PIPEWISP_CUSTOM = %q, want %q", got, want)
	}
}

func TestHookEnvironmentAddsShutdownReasonOnlyForShutdown(t *testing.T) {
	t.Setenv("PIPEWISP_REASON", "parent-reason")

	for _, test := range []struct {
		name    string
		context hookContext
		want    string
	}{
		{name: "shutdown", context: hookContext{event: "shutdown", reason: "eof"}, want: "eof"},
		{name: "other event", context: hookContext{event: "idle", reason: "eof"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := environmentValues(hookEnvironment(test.context))
			if test.want == "" {
				if _, ok := values[hookEnvReason]; ok {
					t.Fatalf("%s unexpectedly set: %#v", hookEnvReason, values[hookEnvReason])
				}
				return
			}
			if got := values[hookEnvReason]; len(got) != 1 || got[0] != test.want {
				t.Fatalf("%s = %#v, want [%q]", hookEnvReason, got, test.want)
			}
		})
	}
}

func TestOwnedHookEnvironmentKeyCaseHandling(t *testing.T) {
	wantCaseInsensitive := runtime.GOOS == "windows"
	for _, key := range []string{
		hookEnvEvent,
		hookEnvReason,
		hookEnvBytes,
		hookEnvDurationMilliseconds,
		strings.ToLower(hookEnvEvent),
		"PipeWisp_Bytes",
	} {
		got := isOwnedHookEnvironmentKey(key)
		if key == hookEnvEvent || key == hookEnvReason || key == hookEnvBytes || key == hookEnvDurationMilliseconds {
			if !got {
				t.Errorf("isOwnedHookEnvironmentKey(%q) = false, want true", key)
			}
			continue
		}
		if got != wantCaseInsensitive {
			t.Errorf("isOwnedHookEnvironmentKey(%q) = %v, want %v on %s", key, got, wantCaseInsensitive, runtime.GOOS)
		}
	}
}

func TestRunWithOptionsPublishesLifecycleContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell context output uses POSIX quoting")
	}

	var output bytes.Buffer
	var diagnostics bytes.Buffer
	command := "printf '%s:%s:%s:' \"$PIPEWISP_EVENT\" \"$PIPEWISP_BYTES\" \"$PIPEWISP_DURATION_MILLISECONDS\"; if [ -n \"${PIPEWISP_REASON+x}\" ]; then printf '%s' \"$PIPEWISP_REASON\"; else printf '<missing>'; fi; printf '\\n'"
	config := options{
		onReady:        "sleep 0.01; " + command,
		onReadySet:     true,
		onFirstData:    command,
		onFirstDataSet: true,
		onShutdown:     command,
		onShutdownSet:  true,
	}

	if got := runWithOptions(config, strings.NewReader("abc"), &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}
	if got, want := output.String(), "abc"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	lines := strings.Split(strings.TrimSpace(diagnostics.String()), "\n")
	if got, want := len(lines), 3; got != want {
		t.Fatalf("hook context lines = %#v, want %d lines", lines, want)
	}
	if got, want := lines[0], "ready:0:0:<missing>"; got != want {
		t.Fatalf("ready context = %q, want %q", got, want)
	}
	if got, want := lines[1], "first-data:0:"; !strings.HasPrefix(got, want) || !strings.HasSuffix(got, ":<missing>") {
		t.Fatalf("first-data context = %q, want bytes 0, nonnegative duration, and no reason", got)
	}
	if got, want := lines[2], "shutdown:3:"; !strings.HasPrefix(got, want) || !strings.HasSuffix(got, ":eof") {
		t.Fatalf("shutdown context = %q, want bytes 3, nonnegative duration, and eof reason", got)
	}
	for _, line := range lines[1:] {
		fields := strings.Split(line, ":")
		if len(fields) != 4 {
			t.Fatalf("context line = %q, want four fields", line)
		}
		if duration, err := strconv.ParseInt(fields[2], 10, 64); err != nil || duration < 0 {
			t.Fatalf("duration field in %q = %q, want nonnegative integer", line, fields[2])
		}
	}
}

func TestRunWithOptionsPublishesIdleAndResumeByteSnapshots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell context output uses POSIX quoting")
	}

	command := "printf '%s:%s:%s\\n' \"$PIPEWISP_EVENT\" \"$PIPEWISP_BYTES\" \"$PIPEWISP_DURATION_MILLISECONDS\""
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "hook", events: events}
	config := options{
		idle:          5 * time.Millisecond,
		idleSet:       true,
		onIdle:        command,
		onIdleSet:     true,
		onResume:      command,
		onResumeSet:   true,
		onShutdown:    command,
		onShutdownSet: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, io.Discard, diagnostics) }()
	waitForEvent(t, events, "hook:idle:1:")
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}

	var observed []string
	for {
		select {
		case event := <-events:
			observed = append(observed, event)
		default:
			if !containsEvent(observed, "hook:resume:1:") {
				t.Fatalf("hook events = %#v, want resume bytes snapshot of 1", observed)
			}
			if !containsEvent(observed, "hook:shutdown:2:") {
				t.Fatalf("hook events = %#v, want shutdown bytes snapshot of 2", observed)
			}
			return
		}
	}
}

func TestCompletionReasonForLifecycleOutcomes(t *testing.T) {
	tests := []struct {
		name string
		done completion
		want string
	}{
		{name: "eof", done: completion{}, want: "eof"},
		{name: "signal", done: completion{signal: os.Interrupt}, want: "signal"},
		{name: "broken pipe", done: completion{copyErr: syscall.EPIPE}, want: "broken-pipe"},
		{name: "io error", done: completion{copyErr: errors.New("read failed")}, want: "io-error"},
		{name: "first-data hook failure", done: completion{copyErr: &firstDataHookError{err: errors.New("hook failed")}, firstDataHookFailed: true}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := completionReason(test.done); got != test.want {
				t.Fatalf("completionReason() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCountingWriterCountsPartialSuccessfulWrites(t *testing.T) {
	state := newLifecycleState()
	underlying := &partialWriter{max: 2}
	writer := state.writer(underlying)

	if err := writeIdleChunk(writer, []byte("hello")); err != nil {
		t.Fatalf("writeIdleChunk() error = %v", err)
	}
	if got, want := state.bytes.Load(), int64(5); got != want {
		t.Fatalf("counted bytes = %d, want %d", got, want)
	}
	if got, want := underlying.output.String(), "hello"; got != want {
		t.Fatalf("underlying output = %q, want %q", got, want)
	}
}

func TestCountingWriterReadFromDelegatesAndCountsOnce(t *testing.T) {
	state := newLifecycleState()
	underlying := &readerFromWriter{}
	writer := state.writer(underlying)

	n, err := io.Copy(writer, readerWithoutWriterTo{strings.NewReader("reader-from")})
	if err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	if got, want := n, int64(len("reader-from")); got != want {
		t.Fatalf("io.Copy() bytes = %d, want %d", got, want)
	}
	if got, want := underlying.readFromCalls, 1; got != want {
		t.Fatalf("underlying ReadFrom calls = %d, want %d", got, want)
	}
	if got, want := state.bytes.Load(), int64(len("reader-from")); got != want {
		t.Fatalf("counted bytes = %d, want %d", got, want)
	}
	if got, want := underlying.output.String(), "reader-from"; got != want {
		t.Fatalf("underlying output = %q, want %q", got, want)
	}
}

func TestCountingWriterReadFromFallbackCountsOnce(t *testing.T) {
	state := newLifecycleState()
	underlying := &writeOnlyBuffer{}
	writer := state.writer(underlying)

	n, err := io.Copy(writer, readerWithoutWriterTo{strings.NewReader("fallback")})
	if err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	if got, want := n, int64(len("fallback")); got != want {
		t.Fatalf("io.Copy() bytes = %d, want %d", got, want)
	}
	if got, want := state.bytes.Load(), int64(len("fallback")); got != want {
		t.Fatalf("counted bytes = %d, want %d", got, want)
	}
	if got, want := underlying.output.String(), "fallback"; got != want {
		t.Fatalf("underlying output = %q, want %q", got, want)
	}
}

func TestRunWithOptionsPublishesPartialWriteErrorBytesToShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell context output uses POSIX quoting")
	}

	var diagnostics bytes.Buffer
	output := &partialErrorWriter{max: 2, err: errors.New("partial output failure")}
	config := options{
		onShutdown:    "printf '%s:%s:%s:%s\\n' \"$PIPEWISP_EVENT\" \"$PIPEWISP_BYTES\" \"$PIPEWISP_DURATION_MILLISECONDS\" \"$PIPEWISP_REASON\"",
		onShutdownSet: true,
	}

	if got := runWithOptions(config, strings.NewReader("input"), output, &diagnostics); got != 1 {
		t.Fatalf("runWithOptions() exit code = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if got, want := output.output.String(), "in"; got != want {
		t.Fatalf("partial output = %q, want %q", got, want)
	}
	if got := diagnostics.String(); !strings.Contains(got, "shutdown:2:") || !strings.HasSuffix(got, ":io-error\n") {
		t.Fatalf("shutdown context = %q, want bytes 2 and io-error reason (duration may vary from zero)", got)
	}
}

func TestShutdownContextRemainsFixedAfterLateSignal(t *testing.T) {
	state := newLifecycleState()
	state.bytes.Store(4)
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	done := completion{}
	context := snapshotShutdownContext(state, done)

	var delivered hookContext
	var diagnostics bytes.Buffer
	status := finishCompletionWithTracker(done, func() error {
		delivered = context
		state.bytes.Add(8)
		signals <- os.Interrupt
		return nil
	}, &diagnostics, tracker)
	if status != 130 {
		t.Fatalf("finishCompletionWithTracker() status = %d, want 130", status)
	}
	if delivered != context {
		t.Fatalf("shutdown context changed during late signal: delivered = %#v, snapshot = %#v", delivered, context)
	}
	if got, want := context.reason, "eof"; got != want {
		t.Fatalf("shutdown reason = %q, want %q", got, want)
	}
	if got, want := context.bytes, int64(4); got != want {
		t.Fatalf("shutdown bytes = %d, want %d", got, want)
	}
}

func TestLifecycleReadySnapshotDurationIsAlwaysZero(t *testing.T) {
	state := &lifecycleState{started: time.Now().Add(-time.Hour)}
	state.bytes.Store(7)

	context := state.snapshot("ready", "")
	if context.event != "ready" {
		t.Fatalf("ready context event = %q, want ready", context.event)
	}
	if context.bytes != 7 {
		t.Fatalf("ready context bytes = %d, want 7", context.bytes)
	}
	if context.durationMilliseconds != 0 {
		t.Fatalf("ready context duration = %d, want 0", context.durationMilliseconds)
	}
}

func TestHookContextForInvocationSnapshotsWhenVerboseDisabled(t *testing.T) {
	for _, event := range []string{"first-data", "idle", "resume"} {
		t.Run(event, func(t *testing.T) {
			state := &lifecycleState{started: time.Unix(0, 0)}
			state.bytes.Store(7)
			observed := hookContext{
				event:                event,
				bytes:                99,
				durationMilliseconds: 123,
			}

			got := hookContextForInvocation(state, event, observed, false)
			if got.event != event {
				t.Fatalf("hook context event = %q, want %s", got.event, event)
			}
			if got.bytes != 7 {
				t.Fatalf("hook context bytes = %d, want current state bytes 7", got.bytes)
			}
			if got.durationMilliseconds <= observed.durationMilliseconds {
				t.Fatalf("hook context duration = %d, want invocation-time snapshot after sentinel duration %d", got.durationMilliseconds, observed.durationMilliseconds)
			}
		})
	}
}

func TestHookContextForInvocationReusesVerboseObservation(t *testing.T) {
	for _, event := range []string{"first-data", "idle", "resume"} {
		t.Run(event, func(t *testing.T) {
			state := &lifecycleState{started: time.Unix(0, 0)}
			state.bytes.Store(7)
			observed := hookContext{
				event:                event,
				bytes:                99,
				durationMilliseconds: 123,
			}

			if got := hookContextForInvocation(state, event, observed, true); got != observed {
				t.Fatalf("hook context = %#v, want observed context %#v", got, observed)
			}
		})
	}
}

func TestLifecycleEventForHookName(t *testing.T) {
	tests := map[string]string{
		"on-ready":      "ready",
		"on-shutdown":   "shutdown",
		"on-first-data": "first-data",
		"on-idle":       "idle",
		"on-resume":     "resume",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := lifecycleEventForHookName(name); got != want {
				t.Fatalf("lifecycleEventForHookName(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

type partialWriter struct {
	max    int
	output bytes.Buffer
}

type partialErrorWriter struct {
	max    int
	err    error
	output bytes.Buffer
}

func (writer *partialErrorWriter) Write(data []byte) (int, error) {
	count := len(data)
	if count > writer.max {
		count = writer.max
	}
	_, _ = writer.output.Write(data[:count])
	return count, writer.err
}

type readerFromWriter struct {
	output        bytes.Buffer
	readFromCalls int
}

func (writer *readerFromWriter) Write(data []byte) (int, error) {
	return writer.output.Write(data)
}

func (writer *readerFromWriter) ReadFrom(reader io.Reader) (int64, error) {
	writer.readFromCalls++
	return io.Copy(&writer.output, reader)
}

type writeOnlyBuffer struct {
	output bytes.Buffer
}

func (writer *writeOnlyBuffer) Write(data []byte) (int, error) {
	return writer.output.Write(data)
}

type readerWithoutWriterTo struct {
	reader io.Reader
}

func (reader readerWithoutWriterTo) Read(data []byte) (int, error) {
	return reader.reader.Read(data)
}

func (writer *partialWriter) Write(data []byte) (int, error) {
	count := len(data)
	if count > writer.max {
		count = writer.max
	}
	_, _ = writer.output.Write(data[:count])
	return count, nil
}

func environmentValues(environment []string) map[string][]string {
	values := make(map[string][]string)
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = append(values[key], value)
		}
	}
	return values
}
