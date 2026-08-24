package main

// This file renders optional, human-oriented lifecycle and hook diagnostics.

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type verboseReporter struct {
	out       io.Writer
	enabled   bool
	mu        sync.Mutex
	lineStart bool
}

const verbosePrefix = "pipewisp: "

func newVerboseReporter(out io.Writer, enabled bool) *verboseReporter {
	return &verboseReporter{out: out, enabled: enabled, lineStart: true}
}

func (r *verboseReporter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.out.Write(p)
	if n > 0 {
		r.lineStart = p[n-1] == '\n'
	}
	return n, err
}

func (r *verboseReporter) diagnostic(format string, args ...any) {
	if !r.enabled {
		return
	}
	line := fmt.Sprintf(verbosePrefix+format+"\n", args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lineStart {
		_, _ = io.WriteString(r.out, "\n")
	}
	_, _ = io.WriteString(r.out, line)
	r.lineStart = true
}

func (r *verboseReporter) event(context hookContext) {
	if context.event == "shutdown" && context.reason != "" {
		r.diagnostic("type=event event=%s reason=%s bytes=%d elapsed_ms=%d", context.event, context.reason, context.bytes, context.durationMilliseconds)
		return
	}
	r.diagnostic("type=event event=%s bytes=%d elapsed_ms=%d", context.event, context.bytes, context.durationMilliseconds)
}

func (r *verboseReporter) hookStart(event string) {
	r.diagnostic("type=hook event=%s state=start", event)
}

func (r *verboseReporter) hookEnd(event string, started time.Time, err error) {
	if !r.enabled {
		return
	}
	duration := int64(time.Since(started) / time.Millisecond)
	var timeoutErr *hookTimeoutError
	if errors.As(err, &timeoutErr) {
		r.diagnostic("type=hook event=%s state=timeout duration_ms=%d", event, duration)
		return
	}
	var signalErr *hookSignalError
	if errors.As(err, &signalErr) {
		r.diagnostic("type=hook event=%s state=interrupted signal=%s duration_ms=%d", event, canonicalSignal(signalErr.signal), duration)
		return
	}
	var startErr *hookStartError
	if errors.As(err, &startErr) {
		r.diagnostic("type=hook event=%s state=error phase=start duration_ms=0", event)
		return
	}
	var processErr *hookProcessError
	if errors.As(err, &processErr) && processErr.state != nil {
		if signal, ok := processStateSignal(processErr.state); ok {
			r.diagnostic("type=hook event=%s state=exit signal=%s duration_ms=%d", event, canonicalSignal(signal), duration)
			return
		}
		if processErr.state.Exited() {
			r.diagnostic("type=hook event=%s state=exit exit_code=%d duration_ms=%d", event, processErr.state.ExitCode(), duration)
			return
		}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if signal, ok := hookExitSignal(exitErr); ok {
			r.diagnostic("type=hook event=%s state=exit signal=%s duration_ms=%d", event, canonicalSignal(signal), duration)
			return
		}
		r.diagnostic("type=hook event=%s state=exit exit_code=%d duration_ms=%d", event, exitErr.ExitCode(), duration)
		return
	}
	if err != nil {
		r.diagnostic("type=hook event=%s state=exit exit_code=0 duration_ms=%d", event, duration)
		return
	}
	r.diagnostic("type=hook event=%s state=exit exit_code=0 duration_ms=%d", event, duration)
}

func verboseForWriter(w io.Writer) *verboseReporter {
	r, _ := w.(*verboseReporter)
	return r
}
