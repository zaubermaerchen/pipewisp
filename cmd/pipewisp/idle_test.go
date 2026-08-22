package main

// This file tests idle-mode option parsing and lifecycle hook ordering.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseIdleOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want options
	}{
		{
			name: "separated",
			args: []string{"--idle", "25ms", "--on-idle", "idle", "--on-resume", "resume"},
			want: options{
				idle:        25 * time.Millisecond,
				idleSet:     true,
				onIdle:      "idle",
				onIdleSet:   true,
				onResume:    "resume",
				onResumeSet: true,
			},
		},
		{
			name: "equals",
			args: []string{"--idle=1s", "--on-idle=idle", "--on-resume=resume"},
			want: options{
				idle:        time.Second,
				idleSet:     true,
				onIdle:      "idle",
				onIdleSet:   true,
				onResume:    "resume",
				onResumeSet: true,
			},
		},
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
			if got != tt.want {
				t.Fatalf("parseArgs() options = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseRejectsInvalidIdleOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "duplicate separated", args: []string{"--idle", "1s", "--idle", "2s", "--on-idle", "idle"}, want: "--idle specified more than once"},
		{name: "duplicate equals", args: []string{"--idle=1s", "--idle=2s", "--on-idle", "idle"}, want: "--idle specified more than once"},
		{name: "duplicate on-idle", args: []string{"--idle=1s", "--on-idle", "first", "--on-idle=second"}, want: "--on-idle specified more than once"},
		{name: "duplicate on-resume", args: []string{"--idle=1s", "--on-resume=first", "--on-resume", "second"}, want: "--on-resume specified more than once"},
		{name: "missing duration", args: []string{"--idle", "--on-idle", "idle"}, want: "missing value for --idle"},
		{name: "missing idle hook", args: []string{"--idle=1s", "--on-idle"}, want: "missing value for --on-idle"},
		{name: "missing resume hook", args: []string{"--idle=1s", "--on-resume"}, want: "missing value for --on-resume"},
		{name: "empty duration", args: []string{"--idle=", "--on-idle", "idle"}, want: "empty duration for --idle"},
		{name: "whitespace duration", args: []string{"--idle", " \t", "--on-idle", "idle"}, want: "empty duration for --idle"},
		{name: "invalid duration", args: []string{"--idle=soon", "--on-idle", "idle"}, want: "invalid duration for --idle"},
		{name: "zero duration", args: []string{"--idle=0s", "--on-idle", "idle"}, want: "--idle must be greater than zero"},
		{name: "negative duration", args: []string{"--idle", "-1s", "--on-idle", "idle"}, want: "--idle must be greater than zero"},
		{name: "idle alone", args: []string{"--idle=1s"}, want: "--idle requires --on-idle or --on-resume"},
		{name: "hook without idle", args: []string{"--on-idle", "idle"}, want: "--on-idle requires --idle"},
		{name: "empty idle hook", args: []string{"--idle=1s", "--on-idle="}, want: "empty command for --on-idle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, help, err := parseArgs(tt.args)
			if err == nil {
				t.Fatal("parseArgs() error = nil, want error")
			}
			if help {
				t.Fatal("parseArgs() help = true, want false")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseArgs() error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestIdleHooksRunAroundResumeBeforeData(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onIdle:      hookOutputCommand("idle"),
		onIdleSet:   true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()

	if got := waitForEvent(t, events, "idle"); !strings.Contains(got, "idle") {
		t.Fatalf("idle diagnostic = %q, want idle hook output", got)
	}
	input.releaseSecond()

	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}

	var observed []string
	for {
		select {
		case event := <-events:
			observed = append(observed, event)
		default:
			if !containsEvent(observed, "resume") {
				t.Fatalf("hook events = %#v, want resume", observed)
			}
			return
		}
	}
}

func TestIdleHookFailureKeepsCopyingAndSetsFinalStatus(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onIdle:      failingHookCommand("idle"),
		onIdleSet:   true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	waitForEvent(t, events, "on-idle hook failed")
	input.releaseSecond()

	select {
	case status := <-done:
		if status != 1 {
			t.Fatalf("runWithOptions() exit code = %d, want 1", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
}

func TestResumeHookFailureKeepsDataAndSetsFinalStatus(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onIdle:      hookOutputCommand("idle"),
		onIdleSet:   true,
		onResume:    failingHookCommand("resume"),
		onResumeSet: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	waitForEvent(t, events, "idle")
	input.releaseSecond()
	waitForEvent(t, events, "on-resume hook failed")

	select {
	case status := <-done:
		if status != 1 {
			t.Fatalf("runWithOptions() exit code = %d, want 1", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
}

func TestIdleEOFDoesNotRunResumeHook(t *testing.T) {
	input := &gatedEOFReader{first: []byte("a"), eofReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onIdle:      hookOutputCommand("idle"),
		onIdleSet:   true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	waitForEvent(t, events, "idle")
	input.releaseEOF()

	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "a"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	for {
		select {
		case event := <-events:
			if strings.Contains(event, "resume") {
				t.Fatalf("resume hook event = %q, EOF must not trigger resume", event)
			}
		default:
			return
		}
	}
}

func TestIdleTimerExcludesStdoutWrite(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	writer := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:      5 * time.Millisecond,
		idleSet:   true,
		onIdle:    hookOutputCommand("idle"),
		onIdleSet: true,
	}

	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, writer, diagnostics) }()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("stdout write did not start")
	}
	time.Sleep(25 * time.Millisecond)
	select {
	case event := <-events:
		t.Fatalf("idle hook ran during stdout write: %q", event)
	default:
	}
	close(writer.release)
	waitForEvent(t, events, "idle")
	input.releaseSecond()

	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
}

func TestIdleSignalReturnsWhileStdoutWriteBlocked(t *testing.T) {
	signals := make(chan os.Signal, 1)
	tracker := &signalTracker{signals: signals}
	writer := &signalBlockingWriter{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	config := options{
		idle:      time.Hour,
		idleSet:   true,
		onIdle:    "true",
		onIdleSet: true,
	}
	completed := make(chan completion, 1)
	go func() {
		completed <- runIdleCopy(config, strings.NewReader("a"), writer, io.Discard, tracker)
	}()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("stdout write did not start")
	}
	signals <- os.Interrupt

	var done completion
	select {
	case done = <-completed:
	case <-time.After(250 * time.Millisecond):
		close(writer.release)
		<-writer.finished
		t.Fatal("idle copy waited for blocked stdout write after signal")
	}
	close(writer.release)
	select {
	case <-writer.finished:
	case <-time.After(time.Second):
		t.Fatal("stdout write worker did not finish after release")
	}
	if done.signal != os.Interrupt {
		t.Fatalf("runIdleCopy() signal = %v, want %v", done.signal, os.Interrupt)
	}
}

func TestIdleReadPumpSignalsReadStart(t *testing.T) {
	reader := &blockingReadStartReader{
		called:  make(chan struct{}),
		release: make(chan struct{}),
	}
	pump := newIdleReadPump(reader)
	defer pump.stopReading(false)
	pump.request()
	select {
	case <-pump.readStarted:
	case <-time.After(time.Second):
		t.Fatal("read pump did not publish read start")
	}
	close(reader.release)
	select {
	case result := <-pump.results:
		if result.err != io.EOF {
			t.Fatalf("read result error = %v, want EOF", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("read pump did not publish read result")
	}
}

func TestIdleReadPumpReusesReadBuffer(t *testing.T) {
	reader := &recordingReader{remaining: 2}
	pump := newIdleReadPump(reader)
	defer pump.stopReading(false)

	for i := 0; i < 2; i++ {
		pump.request()
		select {
		case <-pump.readStarted:
		case <-time.After(time.Second):
			t.Fatal("read pump did not publish read start")
		}
		select {
		case <-pump.results:
		case <-time.After(time.Second):
			t.Fatal("read pump did not publish read result")
		}
	}
	if len(reader.buffers) != 2 {
		t.Fatalf("read calls = %d, want 2", len(reader.buffers))
	}
	if &reader.buffers[0][0] != &reader.buffers[1][0] {
		t.Fatal("read pump allocated a new buffer for the second read")
	}
}

func TestIdleWritePumpReusesSingleWorker(t *testing.T) {
	writer := &oneByteWriter{}
	pump := newIdleWritePump(writer)
	defer pump.stopWriting()

	chunks := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	for _, chunk := range chunks {
		if !pump.request(chunk) {
			t.Fatal("idle write pump stopped before request")
		}
		select {
		case err := <-pump.results:
			if err != nil {
				t.Fatalf("idle write pump error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("idle write pump did not publish write result")
		}
	}
	if got, want := writer.output.String(), "firstsecondthird"; got != want {
		t.Fatalf("idle write output = %q, want %q", got, want)
	}
}

func TestIdleDataWithEOFIsCopiedWithoutResume(t *testing.T) {
	input := dataEOFReader{data: []byte{0x00, 0x80, 0xff}}
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	config := options{
		idle:        time.Hour,
		idleSet:     true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}

	if got := runWithOptions(config, &input, &output, &diagnostics); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if !bytes.Equal(output.Bytes(), input.data) {
		t.Fatalf("copy output = %x, want %x", output.Bytes(), input.data)
	}
	if strings.Contains(diagnostics.String(), "resume") {
		t.Fatalf("diagnostics = %q, EOF must not trigger resume", diagnostics.String())
	}
}

func TestIdleZeroLengthReadBeforeDataDoesNotStartTimer(t *testing.T) {
	input := &zeroThenGatedReader{data: []byte("data"), ready: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:      5 * time.Millisecond,
		idleSet:   true,
		onIdle:    hookOutputCommand("idle"),
		onIdleSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	time.Sleep(25 * time.Millisecond)
	select {
	case event := <-events:
		t.Fatalf("idle hook ran before first non-empty read: %q", event)
	default:
	}
	close(input.ready)
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "data"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
}

func TestIdleZeroLengthReadAfterDataDoesNotResetTimeout(t *testing.T) {
	input := &activeZeroThenGatedReader{ready: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:      10 * time.Millisecond,
		idleSet:   true,
		onIdle:    hookOutputCommand("idle"),
		onIdleSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	waitForEvent(t, events, "idle")
	close(input.ready)
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "a"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
}

func TestIdleOnlyHookRunsWithoutResumeHook(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:      5 * time.Millisecond,
		idleSet:   true,
		onIdle:    hookOutputCommand("idle"),
		onIdleSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	waitForEvent(t, events, "idle")
	input.releaseSecond()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "ab"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
	for {
		select {
		case event := <-events:
			if strings.Contains(event, "resume") {
				t.Fatalf("resume hook event = %q, on-idle-only mode must not resume", event)
			}
		default:
			return
		}
	}
}

func TestResumeOnlyHookRunsAfterIdle(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	time.Sleep(25 * time.Millisecond)
	select {
	case event := <-events:
		t.Fatalf("on-idle hook ran in resume-only mode: %q", event)
	default:
	}
	input.releaseSecond()
	waitForEvent(t, events, "resume")
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
}

func TestIdleDataArrivalResetsTimeout(t *testing.T) {
	input := &threeStageReader{
		stages: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
		gates:  []chan struct{}{nil, make(chan struct{}), make(chan struct{})},
	}
	var output bytes.Buffer
	events := make(chan string, 16)
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:      20 * time.Millisecond,
		idleSet:   true,
		onIdle:    hookOutputCommand("idle"),
		onIdleSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, &output, diagnostics) }()
	time.Sleep(5 * time.Millisecond)
	close(input.gates[1])
	time.Sleep(5 * time.Millisecond)
	select {
	case event := <-events:
		t.Fatalf("idle hook ran before reset timeout: %q", event)
	default:
	}
	waitForEvent(t, events, "idle")
	close(input.gates[2])
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
	if got, want := output.String(), "abc"; got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
}

func TestIdleMultipleCyclesRunHooksOncePerTransition(t *testing.T) {
	input := &threeStageReader{
		stages: [][]byte{[]byte("a"), []byte("b"), []byte("c"), nil},
		gates:  []chan struct{}{nil, make(chan struct{}), make(chan struct{}), nil},
	}
	events := make(chan string, 32)
	output := &eventChannelWriter{label: "output", events: events}
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onIdle:      hookOutputCommand("idle"),
		onIdleSet:   true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, output, diagnostics) }()
	waitForEvent(t, events, "idle")
	close(input.gates[1])
	waitForEvent(t, events, "resume")
	waitForEvent(t, events, "idle")
	close(input.gates[2])
	waitForEvent(t, events, "resume")
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("runWithOptions() exit code = %d, want 0", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithOptions() did not finish")
	}
}

func TestIdleResumeHookPrecedesDataWrite(t *testing.T) {
	input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
	events := make(chan string, 16)
	output := &eventChannelWriter{label: "output", events: events}
	diagnostics := &eventChannelWriter{label: "diagnostics", events: events}
	config := options{
		idle:        5 * time.Millisecond,
		idleSet:     true,
		onIdle:      hookOutputCommand("idle"),
		onIdleSet:   true,
		onResume:    hookOutputCommand("resume"),
		onResumeSet: true,
	}
	done := make(chan int, 1)
	go func() { done <- runWithOptions(config, input, output, diagnostics) }()
	waitForEvent(t, events, "idle")
	close(input.secondReady)
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
			resumeIndex := eventIndex(observed, "diagnostics:resume")
			dataIndex := eventIndex(observed, "output:b")
			if resumeIndex < 0 || dataIndex < 0 || resumeIndex > dataIndex {
				t.Fatalf("event order = %#v, want resume before data", observed)
			}
			return
		}
	}
}

func TestIdlePartialWritePreservesData(t *testing.T) {
	input := dataEOFReader{data: []byte("partial data")}
	output := &oneByteWriter{}
	config := options{idle: time.Hour, idleSet: true, onIdle: "true", onIdleSet: true}
	if got := runWithOptions(config, &input, output, io.Discard); got != 0 {
		t.Fatalf("runWithOptions() exit code = %d, want 0", got)
	}
	if got, want := output.output.String(), string(input.data); got != want {
		t.Fatalf("copy output = %q, want %q", got, want)
	}
}

func waitForEvent(t *testing.T, events <-chan string, substring string) string {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if strings.Contains(event, substring) {
				return event
			}
		case <-deadline.C:
			t.Fatalf("did not observe event containing %q", substring)
			return ""
		}
	}
}

func containsEvent(events []string, substring string) bool {
	for _, event := range events {
		if strings.Contains(event, substring) {
			return true
		}
	}
	return false
}

func eventIndex(events []string, substring string) int {
	for index, event := range events {
		if strings.Contains(event, substring) {
			return index
		}
	}
	return -1
}

type gatedReader struct {
	first       []byte
	second      []byte
	secondReady chan struct{}
	calls       int
}

func (r *gatedReader) Read(p []byte) (int, error) {
	r.calls++
	switch r.calls {
	case 1:
		return copy(p, r.first), nil
	case 2:
		<-r.secondReady
		return copy(p, r.second), nil
	default:
		return 0, io.EOF
	}
}

func (r *gatedReader) releaseSecond() {
	close(r.secondReady)
}

type gatedEOFReader struct {
	first    []byte
	eofReady chan struct{}
	calls    int
}

func (r *gatedEOFReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 1 {
		return copy(p, r.first), nil
	}
	<-r.eofReady
	return 0, io.EOF
}

func (r *gatedEOFReader) releaseEOF() {
	close(r.eofReady)
}

type eventChannelWriter struct {
	label  string
	events chan<- string
}

func (w *eventChannelWriter) Write(p []byte) (int, error) {
	w.events <- w.label + ":" + string(p)
	return len(p), nil
}

type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	select {
	case <-w.entered:
	default:
		close(w.entered)
	}
	<-w.release
	return len(p), nil
}

type signalBlockingWriter struct {
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (w *signalBlockingWriter) Write(p []byte) (int, error) {
	close(w.entered)
	defer close(w.finished)
	<-w.release
	return len(p), nil
}

type blockingReadStartReader struct {
	called  chan struct{}
	release chan struct{}
}

func (r *blockingReadStartReader) Read([]byte) (int, error) {
	close(r.called)
	<-r.release
	return 0, io.EOF
}

type recordingReader struct {
	remaining int
	buffers   [][]byte
}

func (r *recordingReader) Read(p []byte) (int, error) {
	r.buffers = append(r.buffers, p)
	if r.remaining == 1 {
		r.remaining--
		return 0, io.EOF
	}
	r.remaining--
	return 1, nil
}

type dataEOFReader struct {
	data []byte
	read bool
}

func (r *dataEOFReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	return copy(p, r.data), io.EOF
}

type zeroThenGatedReader struct {
	data  []byte
	ready chan struct{}
	calls int
}

func (r *zeroThenGatedReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 1 {
		return 0, nil
	}
	if r.calls == 2 {
		<-r.ready
		return copy(p, r.data), nil
	}
	return 0, io.EOF
}

type activeZeroThenGatedReader struct {
	ready chan struct{}
	calls int
}

func (r *activeZeroThenGatedReader) Read(p []byte) (int, error) {
	r.calls++
	switch r.calls {
	case 1:
		return copy(p, []byte("a")), nil
	case 2:
		return 0, nil
	case 3:
		<-r.ready
		return 0, io.EOF
	default:
		return 0, io.EOF
	}
}

type threeStageReader struct {
	stages [][]byte
	gates  []chan struct{}
	index  int
}

func (r *threeStageReader) Read(p []byte) (int, error) {
	if r.index >= len(r.stages) {
		return 0, io.EOF
	}
	index := r.index
	r.index++
	if gate := r.gates[index]; gate != nil {
		<-gate
	}
	if data := r.stages[index]; len(data) > 0 {
		return copy(p, data), nil
	}
	return 0, io.EOF
}

type oneByteWriter struct {
	output bytes.Buffer
}

func (w *oneByteWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	_, _ = w.output.Write(p[:1])
	return 1, nil
}
