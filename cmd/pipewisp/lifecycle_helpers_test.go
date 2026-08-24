package main

// This file builds platform-aware shell snippets used by lifecycle tests.

import (
	"runtime"
	"strings"
)

func hookOutputCommand(value string) string {
	if runtime.GOOS == "windows" {
		return "echo(" + value
	}
	return "printf %s " + unixQuote(value)
}

func hookOutputAndErrorCommand(stdout, stderr string) string {
	if runtime.GOOS == "windows" {
		return "echo(" + stdout + " & echo(" + stderr + " 1>&2"
	}
	return "printf %s " + unixQuote(stdout) + "; printf %s " + unixQuote(stderr) + " >&2"
}

func failingHookCommand(output string) string {
	if runtime.GOOS == "windows" {
		return hookOutputCommand(output) + " & exit /b 7"
	}
	return hookOutputCommand(output) + "; exit 7"
}

func shutdownContextWithoutReasonCommand() string {
	if runtime.GOOS == "windows" {
		return "if defined PIPEWISP_REASON (echo %PIPEWISP_EVENT%:%PIPEWISP_REASON%) else (echo %PIPEWISP_EVENT%:missing)"
	}
	return `if [ -n "${PIPEWISP_REASON+x}" ]; then printf '%s:%s' "$PIPEWISP_EVENT" "$PIPEWISP_REASON"; else printf '%s:missing' "$PIPEWISP_EVENT"; fi`
}

func unixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
