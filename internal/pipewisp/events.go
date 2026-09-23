package pipewisp

// This file emits the optional machine-readable lifecycle event stream.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type lifecycleEventRecord struct {
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
}

type sideEffectEventRecord struct {
	Event     string `json:"event"`
	Type      string `json:"type"`
	Trigger   string `json:"trigger"`
	Command   string `json:"command"`
	Executed  bool   `json:"executed"`
	Timestamp string `json:"timestamp"`
	Reason    string `json:"reason,omitempty"`
}

type eventEmitter struct {
	file        *os.File
	diagnostics io.Writer
	mu          sync.Mutex
	disabled    bool
	closed      bool
}

func newEventEmitter(fd int, diagnostics io.Writer) *eventEmitter {
	file, err := duplicateEventFile(fd)
	if err != nil {
		reportDiagnostic(diagnostics, fmt.Errorf("events disabled: duplicate file descriptor %d: %w", fd, err))
		return nil
	}
	return &eventEmitter{file: file, diagnostics: diagnostics}
}

func (emitter *eventEmitter) emit(event string) {
	emitter.emitRecord(lifecycleEventRecord{
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (emitter *eventEmitter) emitSideEffect(trigger, command string, executed bool, reason string) {
	emitter.emitRecord(sideEffectEventRecord{
		Event:     "side-effect",
		Type:      "execute-command",
		Trigger:   trigger,
		Command:   command,
		Executed:  executed,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Reason:    reason,
	})
}

func (emitter *eventEmitter) emitRecord(record any) {
	if emitter == nil {
		return
	}

	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if emitter.disabled || emitter.closed {
		return
	}

	line, err := json.Marshal(record)
	if err == nil {
		line = append(line, '\n')
		var n int
		n, err = writeEvent(emitter.file, line)
		if err == nil && n != len(line) {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		emitter.disabled = true
		writer, prefix := emitter.diagnostics, verbosePrefix
		// Only os.File supports concurrent writes here; keep arbitrary writers serialized.
		switch diagnostics := writer.(type) {
		case *synchronizedWriter:
			if _, ok := diagnostics.out.(*os.File); ok {
				writer = diagnostics.out
			}
		case *verboseReporter:
			prefix = diagnostics.prefix
			if _, ok := diagnostics.out.(*os.File); ok {
				writer = diagnostics.out
			}
		}
		go func() { _, _ = fmt.Fprintf(writer, "%sevents disabled: %v\n", prefix, err) }()
	}
}

func (emitter *eventEmitter) close() {
	if emitter == nil {
		return
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if emitter.closed {
		return
	}
	emitter.closed = true
	_ = emitter.file.Close()
}
