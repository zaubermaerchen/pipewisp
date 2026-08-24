package main

// This file coordinates copy completion, signal tracking, and final status selection.

import (
	"errors"
	"io"
	"os"
	"sync"
)

type completion struct {
	copyErr             error
	signal              os.Signal
	firstDataHookFailed bool
	idleErr             error
	resumeErr           error
}

type signalTracker struct {
	signals    <-chan os.Signal
	first      os.Signal
	mu         sync.Mutex
	notify     chan struct{}
	generation uint64
}

type completionKind uint8

const (
	completionSuccess completionKind = iota
	completionCopyError
	completionBrokenPipe
	completionSignal
)

func selectCompletion(copyDone <-chan error, signals <-chan os.Signal) completion {
	return (&signalTracker{signals: signals}).waitForCopy(copyDone)
}

func (tracker *signalTracker) poll() os.Signal {
	for {
		select {
		case sig := <-tracker.signals:
			// Keep the first signal because later signals cannot change the shell-visible status.
			tracker.remember(sig)
		default:
			return tracker.firstSignal()
		}
	}
}

func (tracker *signalTracker) remember(sig os.Signal) {
	if sig == nil {
		return
	}
	tracker.mu.Lock()
	if tracker.first == nil {
		tracker.first = sig
	}
	if tracker.notify == nil {
		tracker.notify = make(chan struct{})
	}
	previous := tracker.notify
	tracker.notify = make(chan struct{})
	tracker.generation++
	tracker.mu.Unlock()
	close(previous)
}

func (tracker *signalTracker) firstSignal() os.Signal {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.first
}

func (tracker *signalTracker) beginHookObservation() (uint64, chan struct{}) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.notify == nil {
		tracker.notify = make(chan struct{})
	}
	return tracker.generation, tracker.notify
}

func (tracker *signalTracker) generationChanged(generation uint64) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.generation != generation
}

func (tracker *signalTracker) waitForCopy(copyDone <-chan error) completion {
	if tracker.poll() != nil {
		return completion{signal: tracker.firstSignal()}
	}
	select {
	case sig := <-tracker.signals:
		tracker.remember(sig)
		return completion{signal: tracker.firstSignal()}
	case err := <-copyDone:
		// A signal delivered with copy completion must win even when both are ready.
		tracker.poll()
		return completion{copyErr: err, signal: tracker.firstSignal()}
	}
}

func classifyCompletion(done completion) completionKind {
	if done.signal != nil {
		return completionSignal
	}
	if done.copyErr == nil {
		return completionSuccess
	}
	if isBrokenPipe(done.copyErr) {
		return completionBrokenPipe
	}
	return completionCopyError
}

func completionReason(done completion) string {
	if done.signal != nil {
		return "signal"
	}
	if done.copyErr == nil {
		if done.firstDataHookFailed {
			// A first-data hook failure is not an I/O failure, and no lifecycle
			// reason among the public values describes it.
			return ""
		}
		return "eof"
	}

	var firstDataErr *firstDataHookError
	if errors.As(done.copyErr, &firstDataErr) && firstDataErr.readErr == nil {
		// The copy stopped because the hook failed. Keep io-error reserved for
		// an actual stream I/O failure.
		return ""
	}
	if isBrokenPipe(done.copyErr) {
		return "broken-pipe"
	}
	return "io-error"
}

func finishCompletion(done completion, runShutdown func() error, diagnostics io.Writer) int {
	return finishCompletionWithTracker(done, runShutdown, diagnostics, nil)
}

func finishCompletionWithTracker(done completion, runShutdown func() error, diagnostics io.Writer, tracker *signalTracker) int {
	if tracker != nil {
		if sig := tracker.poll(); sig != nil {
			done.signal = sig
		}
	}

	kind := classifyCompletion(done)
	var shutdownErr error
	switch kind {
	case completionCopyError:
		reportCopyError(diagnostics, done.copyErr)
	}

	if runShutdown != nil {
		shutdownErr = runShutdown()
	}

	if tracker != nil {
		// Cleanup is synchronous, so recheck after shutdown to catch a signal during the hook.
		if sig := tracker.poll(); sig != nil {
			done.signal = sig
		}
	}
	if classifyCompletion(done) == completionSignal {
		return signalExitCode(done.signal)
	}
	if shutdownErr != nil || kind == completionCopyError || done.firstDataHookFailed || done.idleErr != nil || done.resumeErr != nil {
		return 1
	}
	return 0
}

func reportCopyError(diagnostics io.Writer, err error) {
	var hookErr *firstDataHookError
	if errors.As(err, &hookErr) {
		// runHook already reported the hook failure, but a read can return data
		// and a separate error together; preserve that independent diagnostic.
		if hookErr.readErr != nil {
			reportDiagnostic(diagnostics, hookErr.readErr)
		}
		return
	}
	reportDiagnostic(diagnostics, err)
}
