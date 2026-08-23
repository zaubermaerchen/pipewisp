package main

// This file executes lifecycle hooks with isolated input and diagnostic output.

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const (
	hookEnvEvent                = "PIPEWISP_EVENT"
	hookEnvReason               = "PIPEWISP_REASON"
	hookEnvBytes                = "PIPEWISP_BYTES"
	hookEnvDurationMilliseconds = "PIPEWISP_DURATION_MILLISECONDS"
)

// hookContext is the immutable snapshot exposed to one lifecycle hook.
// durationMilliseconds is captured before the hook process starts, so time
// spent inside a hook is visible only to later lifecycle events.
type hookContext struct {
	event                string
	reason               string
	bytes                int64
	durationMilliseconds int64
}

func runHook(name, command string, diagnostics io.Writer) error {
	context := hookContext{event: lifecycleEventForHookName(name)}
	if err := runHookWithContext(name, command, context, diagnostics); err != nil {
		return err
	}
	return nil
}

func runHookWithContext(name, command string, context hookContext, diagnostics io.Writer) error {
	if err := executeHook(command, context, diagnostics); err != nil {
		reportDiagnostic(diagnostics, fmt.Errorf("%s hook failed: %w", name, err))
		return err
	}
	return nil
}

func executeHook(command string, context hookContext, diagnostics io.Writer) error {
	hook := newShellCommand(command)
	// Hooks must not consume bytes that belong to the passthrough stream.
	hook.Stdin = nil
	hook.Stdout = diagnostics
	hook.Stderr = diagnostics
	hook.Env = hookEnvironment(context)
	return hook.Run()
}

func lifecycleEventForHookName(name string) string {
	switch name {
	case "on-first-data":
		return "first-data"
	case "on-idle":
		return "idle"
	case "on-resume":
		return "resume"
	default:
		return name
	}
}

func hookEnvironment(context hookContext) []string {
	parent := os.Environ()
	environment := make([]string, 0, len(parent)+4)
	for _, entry := range parent {
		key := entry
		if separator := strings.IndexByte(entry, '='); separator >= 0 {
			key = entry[:separator]
		}
		if isOwnedHookEnvironmentKey(key) {
			continue
		}
		environment = append(environment, entry)
	}

	environment = append(environment,
		hookEnvEvent+"="+context.event,
		hookEnvBytes+"="+strconv.FormatInt(context.bytes, 10),
		hookEnvDurationMilliseconds+"="+strconv.FormatInt(context.durationMilliseconds, 10),
	)
	if context.event == "off" && context.reason != "" {
		environment = append(environment, hookEnvReason+"="+context.reason)
	}
	return environment
}

func isOwnedHookEnvironmentKey(key string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(key, hookEnvEvent) ||
			strings.EqualFold(key, hookEnvReason) ||
			strings.EqualFold(key, hookEnvBytes) ||
			strings.EqualFold(key, hookEnvDurationMilliseconds)
	}
	switch key {
	case hookEnvEvent, hookEnvReason, hookEnvBytes, hookEnvDurationMilliseconds:
		return true
	default:
		return false
	}
}
