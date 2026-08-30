//go:build !windows

package main

// This file tests signal handling while an idle hook is running.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestIdleSignalDuringHookSkipsNewResume(t *testing.T) {
	for _, tt := range []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			started := filepath.Join(directory, "idle.started")
			resumed := filepath.Join(directory, "resume.marker")
			onIdle := "printf x > " + unixQuote(started) + "; sleep 1"
			onResume := "printf x > " + unixQuote(resumed)
			signals := make(chan os.Signal, 1)
			tracker := &signalTracker{signals: signals}
			config := options{
				idle:        5 * time.Millisecond,
				idleSet:     true,
				onIdle:      onIdle,
				onIdleSet:   true,
				onResume:    onResume,
				onResumeSet: true,
			}
			reader, writer := io.Pipe()
			defer writer.Close()
			writerDone := make(chan struct{})
			go func() {
				_, _ = writer.Write([]byte("a"))
				close(writerDone)
			}()

			completed := make(chan completion, 1)
			var diagnostics bytes.Buffer
			go func() {
				completed <- runIdleCopy(config, reader, io.Discard, &diagnostics, tracker)
			}()
			waitForFile(t, started)
			signals <- tt.signal

			var done completion
			select {
			case done = <-completed:
			case <-time.After(3 * time.Second):
				t.Fatal("idle copy did not finish after signal during hook")
			}
			if done.signal != tt.signal {
				t.Fatalf("runIdleCopy() signal = %v, want %v", done.signal, tt.signal)
			}
			if got := finishCompletion(done, nil, &diagnostics); got != tt.status {
				t.Fatalf("finishCompletion() status = %d, want %d", got, tt.status)
			}
			select {
			case <-writerDone:
			case <-time.After(time.Second):
				t.Fatal("pipe writer did not finish")
			}
			if _, err := os.Stat(resumed); !os.IsNotExist(err) {
				t.Fatalf("resume marker exists after signal: err = %v", err)
			}
		})
	}
}

func TestIdleSignalDuringFirstDataHookWaitsBeforeShutdown(t *testing.T) {
	for _, tt := range []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			started := filepath.Join(directory, "first.started")
			done := filepath.Join(directory, "first.done")
			events := filepath.Join(directory, "events")
			onFirstData := "printf x > " + unixQuote(started) + "; sleep 1; printf x > " + unixQuote(done) + "; printf first >> " + unixQuote(events)
			shutdown := "printf shutdown >> " + unixQuote(events)
			signals := make(chan os.Signal, 1)
			tracker := &signalTracker{signals: signals}
			config := options{
				idle:           time.Hour,
				idleSet:        true,
				onFirstData:    onFirstData,
				onFirstDataSet: true,
			}
			reader, writer := io.Pipe()
			defer writer.Close()
			writerDone := make(chan struct{})
			go func() {
				_, _ = writer.Write([]byte("input"))
				close(writerDone)
			}()

			completed := make(chan completion, 1)
			var diagnostics bytes.Buffer
			go func() {
				completed <- runIdleCopy(config, reader, io.Discard, &diagnostics, tracker)
			}()
			waitForFile(t, started)
			signals <- tt.signal

			var doneCompletion completion
			select {
			case doneCompletion = <-completed:
			case <-time.After(3 * time.Second):
				t.Fatal("idle copy did not finish after signal during first-data hook")
			}
			if doneCompletion.signal != tt.signal {
				t.Fatalf("runIdleCopy() signal = %v, want %v", doneCompletion.signal, tt.signal)
			}
			status := finishCompletion(doneCompletion, func() error {
				return runHook("on-shutdown", shutdown, &diagnostics)
			}, &diagnostics)
			if status != tt.status {
				t.Fatalf("finishCompletion() status = %d, want %d", status, tt.status)
			}
			select {
			case <-writerDone:
			case <-time.After(time.Second):
				t.Fatal("pipe writer did not finish")
			}
			if _, err := os.Stat(done); !os.IsNotExist(err) {
				t.Fatalf("first-data completion marker exists after signal: err = %v", err)
			}
			if got, want := readMarker(t, events), "shutdown"; got != want {
				t.Fatalf("first-data/shutdown markers = %q, want %q", got, want)
			}
		})
	}
}

func TestIdleSignalDuringResumeHookPreservesPriorData(t *testing.T) {
	for _, tt := range []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			idleStarted := filepath.Join(directory, "idle.started")
			resumeStarted := filepath.Join(directory, "resume.started")
			input := &gatedReader{first: []byte("a"), second: []byte("b"), secondReady: make(chan struct{})}
			signals := make(chan os.Signal, 1)
			tracker := &signalTracker{signals: signals}
			config := options{
				idle:        5 * time.Millisecond,
				idleSet:     true,
				onIdle:      "printf x > " + unixQuote(idleStarted),
				onIdleSet:   true,
				onResume:    "printf x > " + unixQuote(resumeStarted) + "; sleep 1",
				onResumeSet: true,
			}
			var output, diagnostics bytes.Buffer
			completed := make(chan completion, 1)
			go func() {
				completed <- runIdleCopy(config, input, &output, &diagnostics, tracker)
			}()

			waitForFile(t, idleStarted)
			input.releaseSecond()
			waitForFile(t, resumeStarted)
			signals <- tt.signal

			var done completion
			select {
			case done = <-completed:
			case <-time.After(3 * time.Second):
				t.Fatal("idle copy did not finish after signal during resume hook")
			}
			if done.signal != tt.signal {
				t.Fatalf("runIdleCopy() signal = %v, want %v", done.signal, tt.signal)
			}
			if got := finishCompletion(done, nil, &diagnostics); got != tt.status {
				t.Fatalf("finishCompletion() status = %d, want %d", got, tt.status)
			}
			if got, want := output.String(), "a"; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("file %q was not created", path)
		}
	}
}
