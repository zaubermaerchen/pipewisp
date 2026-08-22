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
	// Subscribe before on so a signal during activation is retained for final status selection.
	tracker, stopSignals := subscribePassthroughSignals()
	defer stopSignals()

	if opts.onSet {
		if err := runHook("on", opts.on, diagnostics); err != nil {
			if sig := tracker.poll(); sig != nil {
				return signalExitCode(sig)
			}
			return 1
		}
	}

	var done completion
	if sig := tracker.poll(); sig != nil {
		// Activation completed under interruption; skip copying but still proceed to optional cleanup.
		done = completion{signal: sig}
	} else if opts.idleSet {
		done = runIdleCopy(opts, in, out, diagnostics, tracker)
	} else {
		copyDone := make(chan error, 1)
		go func() {
			_, err := io.Copy(out, in)
			copyDone <- err
		}()
		done = tracker.waitForCopy(copyDone)
	}
	if done.signal != nil {
		closeInput(in)
	}

	var runOff func() error
	if opts.offSet {
		runOff = func() error {
			return runHook("off", opts.off, diagnostics)
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

func reportDiagnostic(diagnostics io.Writer, err error) {
	_, _ = fmt.Fprintf(diagnostics, "pipewisp: %v\n", err)
}
