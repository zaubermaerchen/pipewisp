package main

// This file tests idle-mode option parsing and lifecycle hook ordering.

import (
	"bytes"
	"io"
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
	waitForEvent(t, events, "idle hook failed")
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
	waitForEvent(t, events, "resume hook failed")

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
