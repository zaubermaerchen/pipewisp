// Package main implements the pipewisp CLI and its stream lifecycle.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runCLI(args []string, in io.Reader, out io.Writer, diagnostics io.Writer) int {
	opts, help, err := parseArgs(args)
	if err != nil {
		reportDiagnostic(diagnostics, err)
		return 1
	}
	if help {
		printUsage(out)
		return 0
	}
	return runWithOptions(opts, in, out, diagnostics)
}

func run(in io.Reader, out io.Writer, diagnostics io.Writer) int {
	return runWithOptions(options{}, in, out, diagnostics)
}

func runWithOptions(opts options, in io.Reader, out io.Writer, diagnostics io.Writer) int {
	state := newLifecycleStateWithHookPolicy(opts.ignoreHookErrors)
	countedOut := state.writer(out)

	// Subscribe before on so a signal during activation is retained for final status selection.
	tracker, stopSignals := subscribePassthroughSignals()
	defer stopSignals()

	var done completion
	if opts.onSet {
		if err := runHookWithContextAndTracker("on", opts.on, state.snapshot("on", ""), diagnostics, opts.hookTimeout, tracker); err != nil {
			if sig := tracker.poll(); sig != nil {
				done = completion{signal: sig}
			} else {
				return 1
			}
		}
	}

	var firstData *firstDataReader
	if done.signal != nil {
		// Activation was interrupted; skip copying but still run cleanup.
	} else if sig := tracker.poll(); sig != nil {
		// Activation completed under interruption; skip copying but still proceed to optional cleanup.
		done = completion{signal: sig}
	} else if opts.idleSet {
		done = runIdleCopyWithState(opts, in, countedOut, diagnostics, tracker, state)
	} else {
		copyDone := make(chan error, 1)
		input := in
		if opts.onFirstDataSet {
			firstData = &firstDataReader{
				reader:   in,
				finished: make(chan struct{}),
				hook: func() error {
					return runHookWithContextAndTracker("on-first-data", opts.onFirstData, state.snapshot("first-data", ""), diagnostics, opts.hookTimeout, tracker)
				},
			}
			input = firstData
		}
		go func() {
			_, err := io.Copy(countedOut, input)
			copyDone <- err
		}()
		done = tracker.waitForCopy(copyDone)
	}

	var offContext hookContext
	if opts.offSet {
		// Completion has now selected the lifecycle event and reason. Freeze the
		// public cleanup context before signal cancellation or hook coordination
		// can add time or allow more writes to finish.
		offContext = snapshotOffContext(state, done)
	}
	if done.signal != nil {
		if firstData != nil {
			if firstData.cancel() {
				// A signal can interrupt the wait while the first-data hook is
				// running. Wait for that hook so cleanup never overlaps it.
				<-firstData.finished
			}
		}
		closeInput(in)
	}
	if firstData != nil {
		done.firstDataHookFailed = firstData.hookFailed()
	}

	var runOff func() error
	if opts.offSet {
		runOff = func() error {
			return runHookWithContextAndTracker("off", opts.off, offContext, diagnostics, opts.hookTimeout, tracker)
		}
	}
	return finishCompletionWithTracker(done, runOff, diagnostics, tracker)
}

func closeInput(in io.Reader) {
	// Closing an interruptible input can unblock a copy waiting to read.
	if closer, ok := in.(io.Closer); ok {
		_ = closer.Close()
	}
}

func snapshotOffContext(state *lifecycleState, done completion) hookContext {
	return state.snapshot("off", completionReason(done))
}

func reportDiagnostic(diagnostics io.Writer, err error) {
	_, _ = fmt.Fprintf(diagnostics, "pipewisp: %v\n", err)
}
