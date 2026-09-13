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

type eventEmitter struct {
	file        *os.File
	diagnostics io.Writer
	mu          sync.Mutex
	disabled    bool
	closed      bool
}

func newEventEmitter(fd int, diagnostics io.Writer) *eventEmitter {
	if fd < 3 {
		reportDiagnostic(diagnostics, fmt.Errorf("events disabled: invalid file descriptor %d", fd))
		return nil
	}
	if err := setEventDescriptorNonInheritable(fd); err != nil {
		reportDiagnostic(diagnostics, fmt.Errorf("events disabled: protect file descriptor %d: %w", fd, err))
		return nil
	}
	file, err := duplicateEventFile(fd)
	if err != nil {
		reportDiagnostic(diagnostics, fmt.Errorf("events disabled: duplicate file descriptor %d: %w", fd, err))
		return nil
	}
	return &eventEmitter{file: file, diagnostics: diagnostics}
}

func (emitter *eventEmitter) emit(event string) {
	if emitter == nil {
		return
	}

	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if emitter.disabled || emitter.closed {
		return
	}

	line, err := json.Marshal(lifecycleEventRecord{
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
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
		reportDiagnostic(emitter.diagnostics, fmt.Errorf("events disabled: %w", err))
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
