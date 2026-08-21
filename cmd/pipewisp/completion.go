package main

import (
	"io"
	"os"
)

type completion struct {
	copyErr error
	signal  os.Signal
}

type completionKind uint8

const (
	completionSuccess completionKind = iota
	completionCopyError
	completionBrokenPipe
	completionSignal
)

func selectCompletion(copyDone <-chan error, signals <-chan os.Signal) completion {
	select {
	case sig := <-signals:
		return completion{signal: sig}
	case err := <-copyDone:
		// Prefer a signal that is already observable when copy completes.
		select {
		case sig := <-signals:
			return completion{signal: sig}
		default:
			return completion{copyErr: err}
		}
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
	kind := classifyCompletion(done)
	status := 0
	switch kind {
	case completionCopyError:
		reportDiagnostic(diagnostics, done.copyErr)
		status = 1
	case completionSignal:
		status = signalExitCode(done.signal)
	}

	if runOff != nil {
		if err := runOff(); err != nil {
			if kind != completionSignal {
				status = 1
			}
		}
	}
	return status
}
