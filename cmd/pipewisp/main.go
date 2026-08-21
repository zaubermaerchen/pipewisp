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
	if opts.onSet {
		if err := runHook("on", opts.on, diagnostics); err != nil {
			return 1
		}
	}

	_, copyErr := io.Copy(out, in)
	status := 0
	if copyErr != nil {
		reportDiagnostic(diagnostics, copyErr)
		status = 1
	}

	if opts.offSet {
		if err := runHook("off", opts.off, diagnostics); err != nil {
			status = 1
		}
	}
	return status
}

func reportDiagnostic(diagnostics io.Writer, err error) {
	_, _ = fmt.Fprintf(diagnostics, "pipewisp: %v\n", err)
}
