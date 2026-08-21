package main

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
	hook.Stdin = nil
	hook.Stdout = diagnostics
	hook.Stderr = diagnostics
	return hook.Run()
}
