package pipewisp

// This file tracks the lifecycle start time and successfully written bytes.

import (
	"io"
	"sync/atomic"
	"time"
)

type lifecycleState struct {
	started time.Time
	bytes   atomic.Int64
	events  *eventEmitter
}

func newLifecycleState() *lifecycleState {
	return &lifecycleState{started: time.Now()}
}

func (state *lifecycleState) snapshot(event, reason string) hookContext {
	durationMilliseconds := int64(time.Since(state.started) / time.Millisecond)
	if event == "ready" {
		// Readiness is the lifecycle origin, so its snapshot is deliberately
		// stable even if process startup takes a measurable amount of time.
		durationMilliseconds = 0
	}
	return hookContext{
		event:                event,
		reason:               reason,
		bytes:                state.bytes.Load(),
		durationMilliseconds: durationMilliseconds,
	}
}

func (state *lifecycleState) emit(event string) {
	if state.events != nil {
		state.events.emit(event)
	}
}

func (state *lifecycleState) writer(out io.Writer) io.Writer {
	return &countingWriter{out: out, state: state}
}

type countingWriter struct {
	out   io.Writer
	state *lifecycleState
}

// io.Copy must pass through Write so snapshots include completed writes even
// while the input remains open. Delegating to the underlying ReaderFrom would
// defer counting until it returns.
func (writer *countingWriter) Write(p []byte) (int, error) {
	n, err := writer.out.Write(p)
	// A Writer contract violation is reported by the caller. Do not expose an
	// invalid count as successfully written output in the hook context.
	if n >= 0 && n <= len(p) {
		writer.state.bytes.Add(int64(n))
	}
	return n, err
}
