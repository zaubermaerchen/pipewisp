package main

// This file tracks the lifecycle start time and successfully written bytes.

import (
	"io"
	"sync/atomic"
	"time"
)

type lifecycleState struct {
	started time.Time
	bytes   atomic.Int64
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

func (state *lifecycleState) writer(out io.Writer) io.Writer {
	return &countingWriter{out: out, state: state}
}

type countingWriter struct {
	out   io.Writer
	state *lifecycleState
}

func (writer *countingWriter) Write(p []byte) (int, error) {
	n, err := writer.out.Write(p)
	// A Writer contract violation is reported by the caller. Do not expose an
	// invalid count as successfully written output in the hook context.
	if n >= 0 && n <= len(p) {
		writer.state.bytes.Add(int64(n))
	}
	return n, err
}

func (writer *countingWriter) ReadFrom(src io.Reader) (int64, error) {
	if readerFrom, ok := writer.out.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(src)
		if n >= 0 {
			writer.state.bytes.Add(n)
		}
		return n, err
	}

	// Hide this wrapper's ReaderFrom method so io.Copy cannot call back into
	// this method. The returned count covers all writes made by the fallback,
	// allowing one atomic update without double-counting individual Write calls.
	n, err := io.Copy(writerOnly{out: writer.out}, src)
	if n >= 0 {
		writer.state.bytes.Add(n)
	}
	return n, err
}

type writerOnly struct {
	out io.Writer
}

func (writer writerOnly) Write(p []byte) (int, error) {
	return writer.out.Write(p)
}
