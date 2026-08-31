//go:build !windows

package main

// This file runs bounded Unix subprocess tests for signals, cleanup, and EPIPE.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const subprocessTimeout = 60 * time.Second

func TestSubprocessSignalsRunShutdownOnce(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			ready := filepath.Join(directory, "ready.marker")
			marker := filepath.Join(directory, "shutdown.marker")
			onCommand := "printf ready > " + unixQuote(ready)
			shutdownCommand := "printf x >> " + unixQuote(marker)

			cmd := exec.Command(binary, "--on-ready", onCommand, "--on-shutdown", shutdownCommand)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("StdinPipe() error = %v", err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer stdin.Close()

			waitForMarker(t, ready)
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stderr = %q", got, tt.status, stderr.String())
			}
			contents, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("ReadFile(marker) error = %v; stderr = %q", err, stderr.String())
			}
			if got, want := string(contents), "x"; got != want {
				t.Fatalf("shutdown marker = %q, want exactly one invocation %q", got, want)
			}
		})
	}
}

func TestSubprocessSignalStatusWinsShutdownFailure(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			ready := filepath.Join(t.TempDir(), "ready.marker")
			onCommand := "printf ready > " + unixQuote(ready)
			cmd := exec.Command(binary, "--on-ready", onCommand, "--on-shutdown", "exit 7")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("StdinPipe() error = %v", err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer stdin.Close()

			waitForMarker(t, ready)
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stderr = %q", got, tt.status, stderr.String())
			}
			if !strings.Contains(stderr.String(), "pipewisp: on-shutdown hook failed:") {
				t.Fatalf("stderr = %q, want shutdown failure diagnostic", stderr.String())
			}
		})
	}
}

func TestSubprocessSIGHUPDuringActiveCopyPreservesWrittenBytes(t *testing.T) {
	binary := buildPipewispBinary(t)
	marker := filepath.Join(t.TempDir(), "shutdown.marker")
	cmd := exec.Command(binary, "--on-shutdown", "printf x >> "+unixQuote(marker))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	inputDone := make(chan error, 1)
	defer func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = stdout.Close()
			_ = cmd.Wait()
		}
	}()

	const (
		payloadSize = 8 << 20
		prefixSize  = 4 << 10
	)
	payload := bytes.Repeat([]byte("pipewisp-active-copy-"), payloadSize/len("pipewisp-active-copy-")+1)[:payloadSize]
	go func() {
		_, writeErr := stdin.Write(payload)
		inputDone <- writeErr
	}()

	prefix := make([]byte, prefixSize)
	prefixRead := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(stdout, prefix)
		prefixRead <- readErr
	}()
	select {
	case err := <-prefixRead:
		if err != nil {
			t.Fatalf("ReadFull(stdout) error = %v", err)
		}
	case <-time.After(subprocessTimeout):
		_ = cmd.Process.Kill()
		_ = stdout.Close()
		_ = stdin.Close()
		<-inputDone
		_ = cmd.Wait()
		t.Fatal("stdout prefix was not produced before timeout")
	}
	select {
	case err := <-inputDone:
		t.Fatalf("input writer completed before signal: %v", err)
	default:
	}
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("Signal(SIGHUP) error = %v", err)
	}

	var output bytes.Buffer
	output.Write(prefix)
	if _, err := io.Copy(&output, stdout); err != nil {
		t.Fatalf("io.Copy(stdout) error = %v", err)
	}
	if status := processExitCode(t, cmd); status != 129 {
		t.Fatalf("process exit code = %d, want 129; stdout bytes = %d; stderr = %q", status, output.Len(), stderr.String())
	}
	select {
	case err := <-inputDone:
		if err == nil {
			t.Fatal("input writer completed successfully after interrupted copy")
		}
	case <-time.After(subprocessTimeout):
		t.Fatal("input writer did not finish after SIGHUP")
	}
	got := output.Bytes()
	if len(got) == 0 {
		t.Fatal("stdout is empty, want successfully written prefix")
	}
	if len(got) >= len(payload) {
		t.Fatalf("stdout bytes = %d, want less than input payload %d after in-flight signal", len(got), len(payload))
	}
	if !bytes.Equal(got, payload[:len(got)]) {
		t.Fatalf("stdout is not an input prefix: got first bytes %q, want %q", got[:min(len(got), 32)], payload[:min(len(got), 32)])
	}
	if markerContents := readMarker(t, marker); markerContents != "x" {
		t.Fatalf("shutdown marker = %q, want exactly one invocation", markerContents)
	}
}

func TestSubprocessSIGHUPPublishesShutdownContext(t *testing.T) {
	binary := buildPipewispBinary(t)
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready.marker")
	contextMarker := filepath.Join(directory, "context.marker")
	onReady := "printf ready > " + unixQuote(ready)
	onShutdown := "printf '%s:%s' \"$PIPEWISP_EVENT\" \"$PIPEWISP_REASON\" > " + unixQuote(contextMarker)
	cmd := exec.Command(binary, "--verbose", "--on-ready", onReady, "--on-shutdown", onShutdown)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer stdin.Close()

	waitForMarker(t, ready)
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("Signal(SIGHUP) error = %v", err)
	}
	if status := processExitCode(t, cmd); status != 129 {
		t.Fatalf("process exit code = %d, want 129; stderr = %q", status, stderr.String())
	}
	if got, want := readMarker(t, contextMarker), "shutdown:signal"; got != want {
		t.Fatalf("shutdown context = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "type=event event=shutdown reason=signal") {
		t.Fatalf("stderr = %q, want verbose signal shutdown event", stderr.String())
	}
}

func TestSubprocessSignalDuringReadySkipsCopy(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			ready := filepath.Join(directory, "ready.started")
			done := filepath.Join(directory, "ready.done")
			shutdownMarker := filepath.Join(directory, "shutdown.marker")
			onCommand := "printf started > " + unixQuote(ready) + "; sleep 1; printf done > " + unixQuote(done)
			shutdownCommand := "printf x >> " + unixQuote(shutdownMarker)

			cmd := exec.Command(binary, "--hook-timeout", "5s", "--on-ready", onCommand, "--on-shutdown", shutdownCommand)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			waitForMarker(t, ready)
			signaledAt := time.Now()
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stderr = %q", got, tt.status, stderr.String())
			}
			if elapsed := time.Since(signaledAt); elapsed >= 2*time.Second {
				t.Fatalf("signal cleanup took %s, want less than configured hook timeout", elapsed)
			}
			if strings.Contains(stderr.String(), "timed out") {
				t.Fatalf("stderr = %q, signal interruption must not be reported as timeout", stderr.String())
			}
			if got, want := stdout.String(), ""; got != want {
				t.Fatalf("stdout = %q, want copy skipped", got)
			}
			if _, err := os.Stat(done); !os.IsNotExist(err) {
				t.Fatalf("ready completion marker exists after signal: err = %v", err)
			}
			if got, want := readMarker(t, shutdownMarker), "x"; got != want {
				t.Fatalf("shutdown marker = %q, want exactly one invocation %q", got, want)
			}
		})
	}
}

func TestSubprocessSignalDuringReadyFailureRunsShutdownOnce(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			ready := filepath.Join(directory, "ready.started")
			shutdownMarker := filepath.Join(directory, "shutdown.marker")
			onCommand := "printf started > " + unixQuote(ready) + "; sleep 1; exit 7"
			shutdownCommand := "printf x >> " + unixQuote(shutdownMarker)

			cmd := exec.Command(binary, "--on-ready", onCommand, "--on-shutdown", shutdownCommand)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			waitForMarker(t, ready)
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stderr = %q", got, tt.status, stderr.String())
			}
			if got, want := readMarker(t, shutdownMarker), "x"; got != want {
				t.Fatalf("shutdown marker = %q, want exactly one invocation %q", got, want)
			}
		})
	}
}

func TestSubprocessSignalDuringFirstDataHookWaitsBeforeShutdown(t *testing.T) {
	for _, tt := range []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	} {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			started := filepath.Join(directory, "first.started")
			done := filepath.Join(directory, "first.done")
			events := filepath.Join(directory, "events")
			onFirstDataCommand := "printf started > " + unixQuote(started) + "; sleep 1; printf done > " + unixQuote(done) + "; printf first >> " + unixQuote(events)
			shutdownCommand := "printf shutdown >> " + unixQuote(events)

			cmd := exec.Command(binary, "--on-first-data", onFirstDataCommand, "--on-shutdown", shutdownCommand)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("StdinPipe() error = %v", err)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer stdin.Close()

			if _, err := stdin.Write([]byte("input")); err != nil {
				t.Fatalf("stdin.Write() error = %v", err)
			}
			waitForMarker(t, started)
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stdout = %q; stderr = %q", got, tt.status, stdout.String(), stderr.String())
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

func TestSubprocessVerboseSignalWaitsForFirstDataDiagnosticsBeforeShutdownEvent(t *testing.T) {
	for _, tt := range []struct {
		name      string
		signal    os.Signal
		status    int
		canonical string
	}{
		{name: "terminate", signal: syscall.SIGTERM, status: 143, canonical: "SIGTERM"},
		{name: "hangup", signal: syscall.SIGHUP, status: 129, canonical: "SIGHUP"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			started := filepath.Join(directory, "first.started")
			onFirstDataCommand := "printf hook-output; printf started > " + unixQuote(started) + "; sleep 1; exit 7"

			cmd := exec.Command(binary, "--verbose", "--on-first-data", onFirstDataCommand, "--on-shutdown", "true")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("StdinPipe() error = %v", err)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer stdin.Close()

			if _, err := stdin.Write([]byte("input")); err != nil {
				t.Fatalf("stdin.Write() error = %v", err)
			}
			waitForMarker(t, started)
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stdout = %q; stderr = %q", got, tt.status, stdout.String(), stderr.String())
			}
			got := stderr.String()
			hookOutput := strings.Index(got, "hook-output")
			firstDataTerminal := strings.Index(got, "type=hook event=first-data state=interrupted signal="+tt.canonical)
			firstDataFailure := strings.Index(got, "pipewisp: on-first-data hook failed:")
			shutdownEvent := strings.Index(got, "type=event event=shutdown reason=signal")
			if hookOutput < 0 || firstDataTerminal < 0 || firstDataFailure < 0 || shutdownEvent < 0 {
				t.Fatalf("stderr = %q, missing first-data terminal/failure or shutdown event", got)
			}
			if hookOutput > shutdownEvent || firstDataTerminal > shutdownEvent || firstDataFailure > shutdownEvent {
				t.Fatalf("stderr = %q, first-data diagnostics must precede shutdown event", got)
			}
		})
	}
}

func TestSubprocessSignalDuringShutdownAfterEOF(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
		{name: "hangup", signal: syscall.SIGHUP, status: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			started := filepath.Join(directory, "shutdown.started")
			done := filepath.Join(directory, "shutdown.done")
			shutdownCommand := "printf started >> " + unixQuote(started) + "; sleep 1; printf done > " + unixQuote(done)

			cmd := exec.Command(binary, "--on-shutdown", shutdownCommand)
			cmd.Stdin = strings.NewReader("")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			waitForMarker(t, started)
			if err := cmd.Process.Signal(tt.signal); err != nil {
				t.Fatalf("Signal(%v) error = %v", tt.signal, err)
			}

			if got := processExitCode(t, cmd); got != tt.status {
				t.Fatalf("process exit code = %d, want %d; stderr = %q", got, tt.status, stderr.String())
			}
			if got, want := readMarker(t, started), "started"; got != want {
				t.Fatalf("shutdown start marker = %q, want exactly one invocation %q", got, want)
			}
			if _, err := os.Stat(done); !os.IsNotExist(err) {
				t.Fatalf("shutdown completion marker exists after signal: err = %v", err)
			}
		})
	}
}

func TestSubprocessShutdownTimeoutPreservesEOFReason(t *testing.T) {
	binary := buildPipewispBinary(t)
	directory := t.TempDir()
	reasonMarker := filepath.Join(directory, "reason.marker")
	shutdownCommand := "printf '%s' \"$PIPEWISP_REASON\" > " + unixQuote(reasonMarker) + "; sleep 1"

	cmd := exec.Command(binary, "--hook-timeout", "20ms", "--on-shutdown", shutdownCommand)
	cmd.Stdin = strings.NewReader("")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	startedAt := time.Now()
	if got := processExitCode(t, cmd); got != 1 {
		t.Fatalf("process exit code = %d, want 1; stderr = %q", got, stderr.String())
	}
	if elapsed := time.Since(startedAt); elapsed >= 2*time.Second {
		t.Fatalf("timed-out shutdown hook took %s, want less than 2s", elapsed)
	}
	if got, want := readMarker(t, reasonMarker), "eof"; got != want {
		t.Fatalf("shutdown reason marker = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, "pipewisp: on-shutdown hook failed:") || !strings.Contains(got, "timed out") {
		t.Fatalf("stderr = %q, want shutdown timeout diagnostic", got)
	}
}

func TestSubprocessHookSIGPIPEUsesDefaultDisposition(t *testing.T) {
	binary := buildPipewispBinary(t)
	cmd := exec.Command(binary, "--on-ready", "kill -PIPE $$; echo survived")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := processExitCode(t, cmd); got == 0 {
		t.Fatalf("process exit code = %d, want hook failure; stderr = %q", got, stderr.String())
	}
	if strings.Contains(stdout.String(), "survived") || strings.Contains(stderr.String(), "survived") {
		t.Fatalf("hook continued after SIGPIPE: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestSubprocessBrokenPipeIsSuccessfulAndSilent(t *testing.T) {
	binary := buildPipewispBinary(t)
	marker := filepath.Join(t.TempDir(), "shutdown.marker")
	shutdownCommand := "printf x >> " + unixQuote(marker)
	cmd := exec.Command(binary, "--on-shutdown", shutdownCommand)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_ = stdout.Close()

	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := stdin.Write(bytes.Repeat([]byte("x"), 4<<20))
		_ = stdin.Close()
		writeDone <- writeErr
	}()

	if got := processExitCode(t, cmd); got != 0 {
		t.Fatalf("process exit code = %d, want 0; stderr = %q", got, stderr.String())
	}
	select {
	case <-writeDone:
	case <-time.After(subprocessTimeout):
		t.Fatal("stdin writer did not finish before timeout")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, broken pipe should be silent", stderr.String())
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile(marker) error = %v", err)
	}
	if got, want := string(contents), "x"; got != want {
		t.Fatalf("shutdown marker = %q, want exactly one invocation %q", got, want)
	}
}

func waitForMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(subprocessTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("marker %q was not created before timeout", path)
		}
	}
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(contents)
}

func buildPipewispBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "pipewisp")
	ctx, cancel := context.WithTimeout(context.Background(), subprocessTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	command.Dir = filepath.Dir(sourceFile(t))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build error = %v; output = %s", err, output)
	}
	return binary
}

func sourceFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return file
}

func processExitCode(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		t.Fatalf("Wait() error = %v", err)
		return -1
	case <-time.After(subprocessTimeout):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("process did not exit before timeout")
		return -1
	}
}
