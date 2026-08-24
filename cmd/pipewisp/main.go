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
	if opts.showVersion {
		printVersion(out)
		return 0
	}
	return runWithOptions(opts, in, out, diagnostics)
}

func run(in io.Reader, out io.Writer, diagnostics io.Writer) int {
	return runWithOptions(options{}, in, out, diagnostics)
}

func runWithOptions(opts options, in io.Reader, out io.Writer, diagnostics io.Writer) int {
	state := newLifecycleState()
	countedOut := state.writer(out)
	reporter := newVerboseReporter(diagnostics, opts.verbose)
	if opts.verbose {
		diagnostics = reporter
	}

	// Subscribe before ready so a signal during readiness is retained for final status selection.
	tracker, stopSignals := subscribePassthroughSignals()
	defer stopSignals()

	var done completion
	readyContext := state.snapshot("ready", "")
	reporter.event(readyContext)
	if opts.onReadySet {
		if err := runHookWithContextAndTracker("on-ready", opts.onReady, readyContext, diagnostics, opts.hookTimeout, tracker, opts.ignoreHookErrors); err != nil {
			if sig := tracker.poll(); sig != nil {
				done = completion{signal: sig}
			} else {
				return 1
			}
		}
	}

	var firstData *firstDataReader
	if done.signal != nil {
		// Readiness was interrupted; skip copying but still run cleanup.
	} else if sig := tracker.poll(); sig != nil {
		// Readiness completed under interruption; skip copying but still proceed to optional cleanup.
		done = completion{signal: sig}
	} else if opts.idleSet {
		done = runIdleCopyWithState(opts, in, countedOut, diagnostics, tracker, state)
	} else {
		copyDone := make(chan error, 1)
		input := in
		if opts.onFirstDataSet || opts.verbose {
			firstData = &firstDataReader{
				reader:   in,
				finished: make(chan struct{}),
				hook: func(context hookContext) error {
					if !opts.onFirstDataSet {
						return nil
					}
					context = hookContextForInvocation(state, "first-data", context, opts.verbose)
					return runHookWithContextAndTracker("on-first-data", opts.onFirstData, context, diagnostics, opts.hookTimeout, tracker, opts.ignoreHookErrors)
				},
			}
			if opts.verbose {
				firstData.event = func() hookContext {
					context := state.snapshot("first-data", "")
					reporter.event(context)
					return context
				}
			}
			input = firstData
		}
		go func() {
			_, err := io.Copy(countedOut, input)
			copyDone <- err
		}()
		done = tracker.waitForCopy(copyDone)
	}

	// Completion has now selected the lifecycle event and reason. Freeze the
	// public cleanup context before diagnostics, signal cancellation, or hook
	// coordination can add time or allow more writes to finish.
	shutdownContext := snapshotShutdownContext(state, done)
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
	// Keep the snapshot above at the completion-selection point, but defer its
	// record until an in-flight first-data hook has finished writing its own
	// terminal and failure diagnostics. This preserves lifecycle output order
	// without changing the context observed by the shutdown hook.
	if opts.verbose {
		reporter.event(shutdownContext)
	}
	if firstData != nil {
		done.firstDataHookFailed = firstData.hookFailed()
	}

	var runShutdown func() error
	if opts.onShutdownSet {
		runShutdown = func() error {
			return runHookWithContextAndTracker("on-shutdown", opts.onShutdown, shutdownContext, diagnostics, opts.hookTimeout, tracker, opts.ignoreHookErrors)
		}
	}
	return finishCompletionWithTracker(done, runShutdown, diagnostics, tracker)
}

func closeInput(in io.Reader) {
	// Closing an interruptible input can unblock a copy waiting to read.
	if closer, ok := in.(io.Closer); ok {
		_ = closer.Close()
	}
}

func snapshotShutdownContext(state *lifecycleState, done completion) hookContext {
	return state.snapshot("shutdown", completionReason(done))
}

func hookContextForInvocation(state *lifecycleState, event string, observed hookContext, verbose bool) hookContext {
	if verbose {
		return observed
	}
	return state.snapshot(event, "")
}

func reportDiagnostic(diagnostics io.Writer, err error) {
	_, _ = fmt.Fprintf(diagnostics, "pipewisp: %v\n", err)
}
