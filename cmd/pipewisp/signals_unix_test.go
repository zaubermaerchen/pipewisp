//go:build !windows

package main

// This file runs bounded Unix subprocess tests for signals, cleanup, and EPIPE.

import (
	"bytes"
	"context"
	"errors"
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

func TestSubprocessSignalsRunOffOnce(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			ready := filepath.Join(directory, "ready.marker")
			marker := filepath.Join(directory, "off.marker")
			onCommand := "printf ready > " + unixQuote(ready)
			offCommand := "printf x >> " + unixQuote(marker)

			cmd := exec.Command(binary, "--on", onCommand, "--off", offCommand)
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
				t.Fatalf("off marker = %q, want exactly one invocation %q", got, want)
			}
		})
	}
}

func TestSubprocessSignalStatusWinsOffFailure(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			ready := filepath.Join(t.TempDir(), "ready.marker")
			onCommand := "printf ready > " + unixQuote(ready)
			cmd := exec.Command(binary, "--on", onCommand, "--off", "exit 7")
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
			if !strings.Contains(stderr.String(), "off hook failed") {
				t.Fatalf("stderr = %q, want off failure diagnostic", stderr.String())
			}
		})
	}
}

func TestSubprocessSignalDuringOnSkipsCopy(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			ready := filepath.Join(directory, "on.started")
			done := filepath.Join(directory, "on.done")
			offMarker := filepath.Join(directory, "off.marker")
			onCommand := "printf started > " + unixQuote(ready) + "; sleep 1; printf done > " + unixQuote(done)
			offCommand := "printf x >> " + unixQuote(offMarker)

			cmd := exec.Command(binary, "--on", onCommand, "--off", offCommand)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
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
			if got, want := stdout.String(), ""; got != want {
				t.Fatalf("stdout = %q, want copy skipped", got)
			}
			if got, want := readMarker(t, done), "done"; got != want {
				t.Fatalf("on completion marker = %q, want %q", got, want)
			}
			if got, want := readMarker(t, offMarker), "x"; got != want {
				t.Fatalf("off marker = %q, want exactly one invocation %q", got, want)
			}
		})
	}
}

func TestSubprocessSignalDuringOnFailureSkipsOff(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			ready := filepath.Join(directory, "on.started")
			offMarker := filepath.Join(directory, "off.marker")
			onCommand := "printf started > " + unixQuote(ready) + "; sleep 1; exit 7"
			offCommand := "printf x >> " + unixQuote(offMarker)

			cmd := exec.Command(binary, "--on", onCommand, "--off", offCommand)
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
			if _, err := os.Stat(offMarker); !os.IsNotExist(err) {
				t.Fatalf("off marker exists after on failure: err = %v", err)
			}
		})
	}
}

func TestSubprocessSignalDuringOffAfterEOF(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
		status int
	}{
		{name: "interrupt", signal: os.Interrupt, status: 130},
		{name: "terminate", signal: syscall.SIGTERM, status: 143},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildPipewispBinary(t)
			directory := t.TempDir()
			started := filepath.Join(directory, "off.started")
			done := filepath.Join(directory, "off.done")
			offCommand := "printf started > " + unixQuote(started) + "; sleep 1; printf done > " + unixQuote(done)

			cmd := exec.Command(binary, "--off", offCommand)
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
			if got, want := readMarker(t, done), "done"; got != want {
				t.Fatalf("off completion marker = %q, want %q", got, want)
			}
		})
	}
}

func TestSubprocessHookSIGPIPEUsesDefaultDisposition(t *testing.T) {
	binary := buildPipewispBinary(t)
	cmd := exec.Command(binary, "--on", "kill -PIPE $$; echo survived")
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
	marker := filepath.Join(t.TempDir(), "off.marker")
	offCommand := "printf x >> " + unixQuote(marker)
	cmd := exec.Command(binary, "--off", offCommand)
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
		t.Fatalf("off marker = %q, want exactly one invocation %q", got, want)
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
