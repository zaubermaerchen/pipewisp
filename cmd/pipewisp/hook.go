package main

// This file executes lifecycle hooks with isolated input and diagnostic output.

import (
	"fmt"
	"io"
)

func runHook(name, command string, diagnostics io.Writer) error {
	if err := executeHook(command, diagnostics); err != nil {
		reportDiagnostic(diagnostics, fmt.Errorf("%s hook failed: %w", name, err))
		return err
	}
	return nil
}

func executeHook(command string, diagnostics io.Writer) error {
	hook := newShellCommand(command)
	// Hooks must not consume bytes that belong to the passthrough stream.
	hook.Stdin = nil
	hook.Stdout = diagnostics
	hook.Stderr = diagnostics
	return hook.Run()
}
