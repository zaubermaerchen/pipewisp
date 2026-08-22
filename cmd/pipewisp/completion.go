package main

// This file coordinates copy completion, signal tracking, and final status selection.

import (
	"errors"
	"io"
	"os"
)

type completion struct {
	copyErr             error
	signal              os.Signal
	firstDataHookFailed bool
}

type signalTracker struct {
	signals <-chan os.Signal
	first   os.Signal
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
			if tracker.first == nil && sig != nil {
				tracker.first = sig
			}
		default:
			return tracker.first
		}
	}
}

func (tracker *signalTracker) waitForCopy(copyDone <-chan error) completion {
	if tracker.poll() != nil {
		return completion{signal: tracker.first}
	}
	select {
	case sig := <-tracker.signals:
		if tracker.first == nil && sig != nil {
			tracker.first = sig
		}
		return completion{signal: tracker.first}
	case err := <-copyDone:
		// A signal delivered with copy completion must win even when both are ready.
		tracker.poll()
		return completion{copyErr: err, signal: tracker.first}
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

func finishCompletion(done completion, runOff func() error, diagnostics io.Writer) int {
	return finishCompletionWithTracker(done, runOff, diagnostics, nil)
}

func finishCompletionWithTracker(done completion, runOff func() error, diagnostics io.Writer, tracker *signalTracker) int {
	if tracker != nil {
		if sig := tracker.poll(); sig != nil {
			done.signal = sig
		}
	}

	kind := classifyCompletion(done)
	var offErr error
	switch kind {
	case completionCopyError:
		reportCopyError(diagnostics, done.copyErr)
	}

	if runOff != nil {
		offErr = runOff()
	}

	if tracker != nil {
		// Cleanup is synchronous, so recheck after off to catch a signal during the hook.
		if sig := tracker.poll(); sig != nil {
			done.signal = sig
		}
	}
	if classifyCompletion(done) == completionSignal {
		return signalExitCode(done.signal)
	}
	if offErr != nil || kind == completionCopyError || done.firstDataHookFailed {
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
