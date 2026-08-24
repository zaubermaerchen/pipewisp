package main

// This file delays passthrough of the first non-empty read until its hook completes.

import (
	"errors"
	"io"
	"sync"
)

type firstDataReader struct {
	reader   io.Reader
	hook     func(hookContext) error
	event    func() hookContext
	finished chan struct{}
	mu       sync.Mutex
	running  bool
	canceled bool
	done     bool
	failed   bool
}

type firstDataHookError struct {
	err     error
	readErr error
}

func (e *firstDataHookError) Error() string {
	return e.err.Error()
}

func (e *firstDataHookError) Unwrap() error {
	return e.err
}

var errFirstDataCanceled = errors.New("first-data hook canceled")

func (r *firstDataReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n == 0 {
		return n, err
	}
	r.mu.Lock()
	if r.canceled {
		r.mu.Unlock()
		return 0, errFirstDataCanceled
	}
	if r.done {
		r.mu.Unlock()
		return n, err
	}
	r.done = true
	r.running = true
	r.mu.Unlock()

	context := hookContext{event: "first-data"}
	if r.event != nil {
		context = r.event()
	}
	hookErr := error(nil)
	if r.hook != nil {
		hookErr = r.hook(context)
	}
	r.mu.Lock()
	r.running = false
	r.failed = hookErr != nil
	if r.finished != nil {
		close(r.finished)
	}
	canceled := r.canceled
	r.mu.Unlock()
	if canceled {
		return 0, errFirstDataCanceled
	}
	if hookErr != nil {
		// Returning data with the hook error lets io.Copy preserve the bytes that
		// triggered the lifecycle event before it stops reading more input.
		var readErr error
		if err != io.EOF {
			readErr = err
		}
		return n, &firstDataHookError{err: hookErr, readErr: readErr}
	}
	return n, err
}

func (r *firstDataReader) cancel() bool {
	r.mu.Lock()
	r.canceled = true
	running := r.running
	r.mu.Unlock()
	return running
}

func (r *firstDataReader) hookFailed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}
