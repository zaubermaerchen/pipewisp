package main

// This file executes lifecycle hooks with isolated input and diagnostic output.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
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
	return runHookWithContextAndTracker(name, command, context, diagnostics, 0, nil, false)
}

func runHookWithContextAndTracker(name, command string, context hookContext, diagnostics io.Writer, timeout time.Duration, tracker *signalTracker, ignoreErrors bool) error {
	reporter := verboseForWriter(diagnostics)
	var started time.Time
	if reporter != nil {
		reporter.hookStart(context.event)
	}
	err := executeHookWithControl(command, context, diagnostics, timeout, tracker, func() { started = time.Now() })
	if reporter != nil {
		reporter.hookEnd(context.event, started, err)
	}
	if err != nil {
		reportDiagnostic(diagnostics, fmt.Errorf("%s hook failed: %w", name, err))
		var signalErr *hookSignalError
		if ignoreErrors && !errors.As(err, &signalErr) {
			return nil
		}
		return err
	}
	return nil
}

func executeHook(command string, context hookContext, diagnostics io.Writer) error {
	return executeHookWithControl(command, context, diagnostics, 0, nil, nil)
}

type hookTimeoutError struct {
	duration time.Duration
}

func (err *hookTimeoutError) Error() string {
	return fmt.Sprintf("hook timed out after %s", err.duration)
}

type hookSignalError struct {
	signal  os.Signal
	waitErr error
}

type hookStartError struct{ err error }

func (err *hookStartError) Error() string { return err.err.Error() }
func (err *hookStartError) Unwrap() error { return err.err }

type hookProcessError struct {
	err   error
	state *os.ProcessState
}

func (err *hookProcessError) Error() string { return err.err.Error() }
func (err *hookProcessError) Unwrap() error { return err.err }

func (err *hookSignalError) Error() string {
	if err.waitErr == nil {
		return fmt.Sprintf("hook interrupted by %v", err.signal)
	}
	return fmt.Sprintf("hook interrupted by %v: %v", err.signal, err.waitErr)
}

func (err *hookSignalError) Unwrap() error {
	return err.waitErr
}

func executeHookWithControl(command string, context hookContext, diagnostics io.Writer, timeout time.Duration, tracker *signalTracker, beforeStart func()) error {
	hook := newShellCommand(command)
	// Hooks must not consume bytes that belong to the passthrough stream.
	hook.Stdin = nil
	hook.Stdout = diagnostics
	hook.Stderr = diagnostics
	// A shell can leave descendants holding inherited output descriptors after
	// it exits. Bound only that post-exit drain; normal output still drains fully
	// when the descriptors close promptly.
	hook.WaitDelay = hookWaitDelay
	hook.Env = hookEnvironment(context)
	if tracker != nil {
		// A signal accepted before this invocation belongs to the surrounding
		// lifecycle outcome, not to a hook that has not started yet. Drain it
		// before taking this invocation's observation point.
		tracker.poll()
	}
	var signalC <-chan os.Signal
	var signalDone <-chan struct{}
	var signalGeneration uint64
	if tracker != nil {
		signalGeneration, signalDone = tracker.beginHookObservation()
		signalC = tracker.signals
	}
	if beforeStart != nil {
		beforeStart()
	}
	if err := hook.Start(); err != nil {
		return &hookStartError{err: err}
	}
	waitDone := make(chan error, 1)
	go func() {
		// Cmd.Wait owns the output-copy goroutines and uses WaitDelay to bound
		// any descendant-held descriptors while still reaping the direct process.
		waitDone <- hook.Wait()
	}()

	var timeoutC <-chan time.Time
	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		timeoutC = timer.C
		defer timer.Stop()
	}

	for {
		select {
		case err := <-waitDone:
			return wrapHookProcessError(normalizeHookWaitError(err), hook.ProcessState)
		case <-timeoutC:
			if tracker != nil {
				// A signal and the timer may become ready together. Signal status
				// takes precedence when the signal was accepted during this hook.
				tracker.poll()
				if tracker.generationChanged(signalGeneration) {
					select {
					case waitErr := <-waitDone:
						return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
					default:
					}
					killErr := hook.Process.Kill()
					waitErr := <-waitDone
					if errors.Is(killErr, os.ErrProcessDone) {
						return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
					}
					if !processStateKilledByPipewisp(hook.ProcessState) {
						return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
					}
					return &hookSignalError{signal: tracker.firstSignal(), waitErr: waitErr}
				}
			}
			select {
			case waitErr := <-waitDone:
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			default:
			}
			killErr := hook.Process.Kill()
			waitErr := <-waitDone
			if errors.Is(killErr, os.ErrProcessDone) {
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			}
			if !processStateKilledByPipewisp(hook.ProcessState) {
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			}
			return &hookTimeoutError{duration: timeout}
		case sig := <-signalC:
			tracker.remember(sig)
			if sig == nil {
				continue
			}
			select {
			case waitErr := <-waitDone:
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			default:
			}
			killErr := hook.Process.Kill()
			waitErr := <-waitDone
			if errors.Is(killErr, os.ErrProcessDone) {
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			}
			if !processStateKilledByPipewisp(hook.ProcessState) {
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			}
			return &hookSignalError{signal: tracker.firstSignal(), waitErr: waitErr}
		case <-signalDone:
			if !tracker.generationChanged(signalGeneration) {
				continue
			}
			select {
			case waitErr := <-waitDone:
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			default:
			}
			killErr := hook.Process.Kill()
			waitErr := <-waitDone
			if errors.Is(killErr, os.ErrProcessDone) {
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			}
			if !processStateKilledByPipewisp(hook.ProcessState) {
				return wrapHookProcessError(normalizeHookWaitError(waitErr), hook.ProcessState)
			}
			return &hookSignalError{signal: tracker.firstSignal(), waitErr: waitErr}
		}
	}
}

const hookWaitDelay = 250 * time.Millisecond

func normalizeHookWaitError(err error) error {
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	return err
}

func wrapHookProcessError(err error, state *os.ProcessState) error {
	if err == nil {
		return nil
	}
	return &hookProcessError{err: err, state: state}
}

func lifecycleEventForHookName(name string) string {
	switch name {
	case "on-ready":
		return "ready"
	case "on-shutdown":
		return "shutdown"
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
	if context.event == "shutdown" && context.reason != "" {
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
