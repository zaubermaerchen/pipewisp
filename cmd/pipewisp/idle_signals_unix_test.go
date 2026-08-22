//go:build !windows

package main

// This file tests signal handling while an idle hook is running.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIdleSignalDuringHookSkipsNewResume(t *testing.T) {
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
	signals <- os.Interrupt

	var done completion
	select {
	case done = <-completed:
	case <-time.After(3 * time.Second):
		t.Fatal("idle copy did not finish after signal during hook")
	}
	if done.signal != os.Interrupt {
		t.Fatalf("runIdleCopy() signal = %v, want %v", done.signal, os.Interrupt)
	}
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("pipe writer did not finish")
	}
	if _, err := os.Stat(resumed); !os.IsNotExist(err) {
		t.Fatalf("resume marker exists after signal: err = %v", err)
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
