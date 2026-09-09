package pipewisp

// This file runs idle/resume hooks without blocking stream processing.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type asyncHookManager struct {
	diagnostics io.Writer
	reporter    *verboseReporter

	mu       sync.Mutex
	hooks    map[*asyncHook]struct{}
	shutdown chan struct{}
	stopped  bool
}

type asyncHook struct {
	manager   *asyncHookManager
	name      string
	event     string
	started   time.Time
	timeout   time.Duration
	execution *hookExecution
	done      chan struct{}
}

const asyncHookProcessPollInterval = 10 * time.Millisecond

func newAsyncHookManager(diagnostics io.Writer) *asyncHookManager {
	reporter := verboseForWriter(diagnostics)
	if reporter == nil {
		diagnostics = &synchronizedWriter{out: diagnostics}
	}
	return &asyncHookManager{
		diagnostics: diagnostics,
		reporter:    reporter,
		hooks:       make(map[*asyncHook]struct{}),
		shutdown:    make(chan struct{}),
	}
}

func (manager *asyncHookManager) start(name, command string, context hookContext, timeout time.Duration) {
	if manager.reporter != nil {
		manager.reporter.hookStart(context.event)
	}
	var started time.Time
	execution, err := startHookExecution(command, context, manager.diagnostics, func() {
		started = time.Now()
	})
	if err != nil {
		startErr := &hookStartError{err: err}
		if manager.reporter != nil {
			manager.reporter.hookEnd(context.event, started, startErr)
		}
		reportDiagnostic(manager.diagnostics, fmt.Errorf("%s hook failed: %w", name, startErr))
		return
	}
	hook := &asyncHook{
		manager:   manager,
		name:      name,
		event:     context.event,
		started:   started,
		timeout:   timeout,
		execution: execution,
		done:      make(chan struct{}),
	}
	manager.mu.Lock()
	if !manager.stopped {
		manager.hooks[hook] = struct{}{}
	}
	manager.mu.Unlock()
	go manager.wait(hook)
}

func (manager *asyncHookManager) wait(hook *asyncHook) {
	defer func() {
		hook.execution.close()
		manager.mu.Lock()
		delete(manager.hooks, hook)
		manager.mu.Unlock()
		close(hook.done)
	}()

	var timeoutC <-chan time.Time
	var timer *time.Timer
	if hook.timeout > 0 {
		timer = time.NewTimer(hook.timeout)
		timeoutC = timer.C
		defer timer.Stop()
	}

	var err error
	cancelled := false
	waitDone := (<-chan error)(hook.execution.waitDone)
	var waitErr error
	waitReceived := false
	directFailureReported := false
	trackingFailureReported := false
	var descendantTicker *time.Ticker
	var descendantC <-chan time.Time
	checkBoundary := func() bool {
		running, checkErr := hook.execution.boundary.hasRunningProcesses()
		if checkErr != nil {
			if !trackingFailureReported {
				reportDiagnostic(hook.manager.diagnostics, fmt.Errorf("%s hook cleanup tracking failed: %w", hook.name, checkErr))
				trackingFailureReported = true
			}
			// If process tracking is unavailable, retain ownership until the
			// timeout or shutdown can make a bounded cleanup attempt.
			return true
		}
		return running
	}
	observeWait := func(result error) bool {
		waitErr = result
		waitReceived = true
		waitDone = nil
		if result != nil && !errors.Is(result, exec.ErrWaitDelay) {
			directFailure := fmt.Errorf("%s hook failed: %w", hook.name, wrapHookProcessError(normalizeHookWaitError(result), hook.execution.hook.ProcessState))
			reportDiagnostic(hook.manager.diagnostics, directFailure)
			directFailureReported = true
		}
		return checkBoundary()
	}
	reportRecoveredWaitFailure := func(stoppedWaitErr error, stoppedWaitReceived bool) {
		if !waitReceived && stoppedWaitReceived {
			if directFailure := recoveredAsyncHookFailure(hook.name, hook.execution, stoppedWaitErr); directFailure != nil {
				reportDiagnostic(hook.manager.diagnostics, directFailure)
				directFailureReported = true
			}
		}
	}
	stopForShutdown := func() error {
		cleanupErr, stoppedWaitErr, stoppedWaitReceived := hook.execution.stopAndWait(waitErr, waitReceived)
		reportRecoveredWaitFailure(stoppedWaitErr, stoppedWaitReceived)
		return cleanupErr
	}
	finished := false
	for !finished {
		select {
		case result := <-waitDone:
			if !observeWait(result) {
				err = wrapHookProcessError(normalizeHookWaitError(waitErr), hook.execution.hook.ProcessState)
				finished = true
				continue
			}
			if descendantTicker == nil {
				descendantTicker = time.NewTicker(asyncHookProcessPollInterval)
				descendantC = descendantTicker.C
			}
		case <-descendantC:
			if !checkBoundary() {
				err = wrapHookProcessError(normalizeHookWaitError(waitErr), hook.execution.hook.ProcessState)
				finished = true
			}
		case <-timeoutC:
			select {
			case <-hook.manager.shutdown:
				err = stopForShutdown()
				cancelled = true
				finished = true
				continue
			default:
			}
			if !waitReceived {
				select {
				case result := <-waitDone:
					if !observeWait(result) {
						err = wrapHookProcessError(normalizeHookWaitError(waitErr), hook.execution.hook.ProcessState)
						finished = true
					}
				default:
				}
			} else if !checkBoundary() {
				err = wrapHookProcessError(normalizeHookWaitError(waitErr), hook.execution.hook.ProcessState)
				finished = true
			}
			if finished {
				continue
			}
			select {
			case <-hook.manager.shutdown:
				err = stopForShutdown()
				cancelled = true
				finished = true
				continue
			default:
			}
			select {
			case <-hook.manager.shutdown:
				err = stopForShutdown()
				cancelled = true
			default:
				var stoppedWaitErr error
				var stoppedWaitReceived bool
				err, stoppedWaitErr, stoppedWaitReceived = stopAsyncHookAndWait(hook.execution, hook.timeout, waitErr, waitReceived)
				reportRecoveredWaitFailure(stoppedWaitErr, stoppedWaitReceived)
			}
			finished = true
		case <-hook.manager.shutdown:
			err = stopForShutdown()
			cancelled = true
			finished = true
		}
	}
	if descendantTicker != nil {
		descendantTicker.Stop()
	}
	if hook.manager.reporter != nil {
		hook.manager.reporter.hookEnd(hook.event, hook.started, err)
	}
	reportAsyncHookBoundaryStopError(hook.manager.diagnostics, hook.name, err)
	if err != nil && !cancelled {
		var timeoutErr *hookTimeoutError
		var signalErr *hookSignalError
		var boundaryErr *hookBoundaryStopError
		if !directFailureReported || errors.As(err, &timeoutErr) || errors.As(err, &signalErr) || errors.As(err, &boundaryErr) {
			reportDiagnostic(hook.manager.diagnostics, fmt.Errorf("%s hook failed: %w", hook.name, err))
		}
	}
}

func stopAsyncHookAndWait(execution *hookExecution, timeout time.Duration, waitErr error, waitReceived bool) (error, error, bool) {
	// Timeout is an invocation outcome only while the boundary still owns a
	// process. A concurrent natural exit can empty the boundary between the
	// final check and stop, in which case it is not a timeout.
	cleanupErr, stoppedWaitErr, stoppedWaitReceived := execution.stopAndWait(waitErr, waitReceived)
	if errors.Is(cleanupErr, os.ErrProcessDone) {
		var processErr *hookProcessError
		if errors.As(cleanupErr, &processErr) {
			return processErr, stoppedWaitErr, stoppedWaitReceived
		}
		return nil, stoppedWaitErr, stoppedWaitReceived
	}
	timeoutErr := &hookTimeoutError{duration: timeout}
	if cleanupErr == nil {
		return timeoutErr, stoppedWaitErr, stoppedWaitReceived
	}
	var stopErr *hookBoundaryStopError
	if errors.As(cleanupErr, &stopErr) {
		return errors.Join(timeoutErr, stopErr), stoppedWaitErr, stoppedWaitReceived
	}
	return timeoutErr, stoppedWaitErr, stoppedWaitReceived
}

func recoveredAsyncHookFailure(name string, execution *hookExecution, waitErr error) error {
	if waitErr == nil || errors.Is(waitErr, exec.ErrWaitDelay) || execution.boundary.killedRoot(execution.hook.ProcessState) {
		return nil
	}
	return fmt.Errorf("%s hook failed: %w", name, wrapHookProcessError(normalizeHookWaitError(waitErr), execution.hook.ProcessState))
}

func reportAsyncHookBoundaryStopError(diagnostics io.Writer, name string, err error) {
	var stopErr *hookBoundaryStopError
	if errors.As(err, &stopErr) {
		reportDiagnostic(diagnostics, fmt.Errorf("%s hook cleanup failed: %w", name, stopErr))
	}
}

func (manager *asyncHookManager) stopAndWait() {
	manager.mu.Lock()
	if !manager.stopped {
		manager.stopped = true
		// Publish cancellation with stopped so a ready timeout cannot win the
		// shutdown race after the manager has begun teardown.
		close(manager.shutdown)
	}
	hooks := make([]*asyncHook, 0, len(manager.hooks))
	for hook := range manager.hooks {
		hooks = append(hooks, hook)
	}
	manager.mu.Unlock()

	for _, hook := range hooks {
		<-hook.done
	}
}

type synchronizedWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (writer *synchronizedWriter) Write(p []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.out.Write(p)
}
