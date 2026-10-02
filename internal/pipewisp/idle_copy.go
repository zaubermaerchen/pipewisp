package pipewisp

// This file adapts idle observation to pipewisp hooks, interruption, and writes.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/zaubermaerchen/khsier"
)

// Observe has no cancellation or callback-error API. These guards stop new I/O
// after interruption or a failed first-data hook without closing borrowed input
// on ordinary completion. A blocked Read may outlive the run, as before.
var errIdleStopped = errors.New("idle observation stopped")

type idleObservedStream struct {
	in                  io.Reader
	writer              *idleWritePump
	tracker             *signalTracker
	firstDataHookFailed bool
	readErr             error
	writeErr            error
}

func (stream *idleObservedStream) Read(data []byte) (int, error) {
	select {
	case <-stream.writer.stop:
		return 0, errIdleStopped
	default:
	}
	if stream.tracker.poll() != nil {
		return 0, errIdleStopped
	}
	n, err := stream.in.Read(data)
	stream.readErr = err
	return n, err
}

func (stream *idleObservedStream) Write(data []byte) (int, error) {
	if stream.tracker.poll() != nil {
		return 0, errIdleStopped
	}
	select {
	case <-stream.writer.stop:
		return 0, errIdleStopped
	default:
	}
	if !stream.writer.request(data) {
		return 0, errIdleStopped
	}
	select {
	case err := <-stream.writer.results:
		stream.writeErr = err
		if err != nil {
			return 0, err
		}
		if stream.firstDataHookFailed {
			// Preserve an independent error returned with the first chunk, while
			// stopping Observe before another read or idle window can begin.
			if stream.readErr != nil && !errors.Is(stream.readErr, io.EOF) {
				return len(data), stream.readErr
			}
			return len(data), errIdleStopped
		}
		return len(data), nil
	case <-stream.writer.stop:
		return 0, errIdleStopped
	}
}

// Observe guarantees errors.Is matching; its wrapping must not change pipewisp's
// diagnostic text. A write failure takes precedence over an accompanying read error.
func (stream *idleObservedStream) copyError(err error) error {
	if stream.writeErr != nil && errors.Is(err, stream.writeErr) {
		return stream.writeErr
	}
	if stream.readErr != nil && errors.Is(err, stream.readErr) {
		return stream.readErr
	}
	return err
}

type idleWritePump struct {
	// Both channels have one slot: the runner submits one chunk and waits for
	// its result before requesting another read, so this worker never writes
	// ahead of the coordinator.
	out      io.Writer
	requests chan []byte
	results  chan error
	stop     chan struct{}
	stopOnce sync.Once
}

func newIdleWritePump(out io.Writer) *idleWritePump {
	pump := &idleWritePump{
		out:      out,
		requests: make(chan []byte, 1),
		results:  make(chan error, 1),
		stop:     make(chan struct{}),
	}
	go pump.write()
	return pump
}

func (pump *idleWritePump) write() {
	for {
		// Check stop before selecting a request so a signal does not start a
		// queued write that the coordinator has already abandoned.
		select {
		case <-pump.stop:
			return
		default:
		}

		var data []byte
		select {
		case data = <-pump.requests:
		case <-pump.stop:
			return
		}
		err := writeIdleChunk(pump.out, data)

		// A Write cannot be interrupted through io.Writer. Once it returns,
		// stop without publishing a result if the runner has already exited.
		select {
		case <-pump.stop:
			return
		default:
		}
		select {
		case pump.results <- err:
		case <-pump.stop:
			return
		}
	}
}

func (pump *idleWritePump) request(data []byte) bool {
	select {
	case pump.requests <- data:
		return true
	case <-pump.stop:
		return false
	}
}

func (pump *idleWritePump) stopWriting() {
	pump.stopOnce.Do(func() {
		close(pump.stop)
	})
}

type idleCopyRunner struct {
	opts         options
	diagnostics  io.Writer
	tracker      *signalTracker
	state        *lifecycleState
	stream       *idleObservedStream
	eventContext hookContext
	asyncHooks   *asyncHookManager
	done         completion
}

func runIdleCopy(opts options, in io.Reader, out io.Writer, diagnostics io.Writer, tracker *signalTracker) completion {
	state := newLifecycleState()
	return runIdleCopyWithState(opts, in, state.writer(out), diagnostics, tracker, state)
}

func runIdleCopyWithState(opts options, in io.Reader, out io.Writer, diagnostics io.Writer, tracker *signalTracker, state *lifecycleState) completion {
	asyncHooks := newAsyncHookManager(diagnostics)
	ownedEvents := false
	if state.events == nil && opts.eventsFDSet {
		state.events = newEventEmitter(opts.eventsFD, asyncHooks.diagnostics)
		ownedEvents = state.events != nil
	}
	if ownedEvents {
		defer state.events.close()
	}
	asyncHooks.events = state.events
	asyncHooks.dryRun = opts.dryRun
	done := runIdleCopyWithAsyncManager(opts, in, out, diagnostics, tracker, state, asyncHooks)
	asyncHooks.stopAndWait()
	return done
}

func runIdleCopyWithAsyncManager(opts options, in io.Reader, out io.Writer, diagnostics io.Writer, tracker *signalTracker, state *lifecycleState, asyncHooks *asyncHookManager) completion {
	asyncHooks.events = state.events
	asyncHooks.dryRun = opts.dryRun
	if asyncHooks.reporter == nil {
		diagnostics = asyncHooks.diagnostics
	}
	writer := newIdleWritePump(out)
	defer writer.stopWriting()
	stream := &idleObservedStream{in: in, writer: writer, tracker: tracker}
	runner := &idleCopyRunner{
		opts: opts, diagnostics: diagnostics, tracker: tracker, state: state,
		stream: stream, asyncHooks: asyncHooks,
	}
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return runner.done
	}
	events := make(chan khsier.Event)
	handled := make(chan struct{})
	copyDone := make(chan error, 1)
	go func() {
		copyDone <- khsier.Observe(stream, stream, khsier.Options{Idle: opts.idle}, func(event khsier.Event) {
			// Keep lifecycle state and hook execution on the coordinator. Observe must
			// wait until the transition finishes before forwarding its pending chunk.
			select {
			case events <- event:
			case <-writer.stop:
				return
			}
			select {
			case <-handled:
			case <-writer.stop:
			}
		})
	}()
	for {
		select {
		case sig := <-tracker.signals:
			if sig == nil {
				continue
			}
			tracker.remember(sig)
			runner.abortForSignal(tracker.firstSignal())
			return runner.done
		case event := <-events:
			if sig := runner.pollSignal(); sig != nil {
				runner.abortForSignal(sig)
				return runner.done
			}
			if !runner.handleEvent(event) {
				return runner.done
			}
			handled <- struct{}{}
		case err := <-copyDone:
			if sig := runner.pollSignal(); sig != nil {
				runner.abortForSignal(sig)
			} else if !errors.Is(err, errIdleStopped) {
				runner.done.copyErr = stream.copyError(err)
			}
			return runner.done
		}
	}
}

func (runner *idleCopyRunner) handleEvent(event khsier.Event) bool {
	switch event.Kind {
	case khsier.EventBOS:
		if runner.opts.verbose || runner.state.events != nil {
			runner.eventContext = runner.state.snapshot("first-data", "")
			if runner.opts.verbose {
				if reporter := verboseForWriter(runner.diagnostics); reporter != nil {
					reporter.event(runner.eventContext)
				}
			}
			if runner.state.events != nil {
				runner.state.emit(runner.eventContext.event)
			}
		}
		if runner.opts.onFirstDataSet {
			if !runner.handleFirstData() {
				return false
			}
			runner.stream.firstDataHookFailed = runner.done.firstDataHookFailed
		}
	case khsier.EventIdle:
		return runner.handleIdle()
	case khsier.EventResume:
		var resumeContext hookContext
		if runner.opts.verbose || runner.state.events != nil {
			resumeContext = runner.state.snapshot("resume", "")
			if runner.opts.verbose {
				if reporter := verboseForWriter(runner.diagnostics); reporter != nil {
					reporter.event(resumeContext)
				}
			}
			if runner.state.events != nil {
				runner.state.emit(resumeContext.event)
			}
		}
		if runner.opts.onResumeAsyncSet {
			resumeContext = hookContextForInvocation(runner.state, "resume", resumeContext, runner.opts.verbose)
			runner.asyncHooks.start("on-resume", runner.opts.onResumeAsync, resumeContext, runner.opts.hookTimeout)
		} else if runner.opts.onResumeSet {
			if sig := runner.pollSignal(); sig != nil {
				runner.abortForSignal(sig)
				return false
			}
			resumeContext = hookContextForInvocation(runner.state, "resume", resumeContext, runner.opts.verbose)
			if err := runHookWithContextAndTrackerAndSideEffect("on-resume", runner.opts.onResume, resumeContext, runner.diagnostics, runner.opts.hookTimeout, runner.tracker, runner.opts.ignoreHookErrors, runner.state.events, runner.opts.dryRun); err != nil {
				runner.done.resumeErr = err
			}
		}
		if sig := runner.pollSignal(); sig != nil {
			runner.abortForSignal(sig)
			return false
		}
	}
	// EOS is internal observation only; pipewisp selects its shutdown transition
	// from completion after Observe returns.
	return true
}

func (runner *idleCopyRunner) handleFirstData() bool {
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	context := hookContextForInvocation(runner.state, "first-data", runner.eventContext, runner.opts.verbose)
	if err := runHookWithContextAndTrackerAndSideEffect("on-first-data", runner.opts.onFirstData, context, runner.diagnostics, runner.opts.hookTimeout, runner.tracker, runner.opts.ignoreHookErrors, runner.state.events, runner.opts.dryRun); err != nil {
		runner.done.firstDataHookFailed = true
	}
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	return true
}

func (runner *idleCopyRunner) handleIdle() bool {
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	var idleContext hookContext
	if runner.opts.verbose || runner.state.events != nil {
		idleContext = runner.state.snapshot("idle", "")
		if runner.opts.verbose {
			reporter := verboseForWriter(runner.diagnostics)
			if reporter != nil {
				reporter.event(idleContext)
			}
		}
		if runner.state.events != nil {
			runner.state.emit(idleContext.event)
		}
	}
	if runner.opts.onIdleAsyncSet {
		idleContext = hookContextForInvocation(runner.state, "idle", idleContext, runner.opts.verbose)
		runner.asyncHooks.start("on-idle", runner.opts.onIdleAsync, idleContext, runner.opts.hookTimeout)
	} else if runner.opts.onIdleSet {
		if sig := runner.pollSignal(); sig != nil {
			runner.abortForSignal(sig)
			return false
		}
		idleContext = hookContextForInvocation(runner.state, "idle", idleContext, runner.opts.verbose)
		if err := runHookWithContextAndTrackerAndSideEffect("on-idle", runner.opts.onIdle, idleContext, runner.diagnostics, runner.opts.hookTimeout, runner.tracker, runner.opts.ignoreHookErrors, runner.state.events, runner.opts.dryRun); err != nil {
			runner.done.idleErr = err
		}
	}
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	return true
}

func (runner *idleCopyRunner) pollSignal() os.Signal {
	return runner.tracker.poll()
}

func (runner *idleCopyRunner) abortForSignal(sig os.Signal) {
	runner.done.signal = sig
	runner.stream.writer.stopWriting()
	closeInput(runner.stream.in)
}

func writeIdleChunk(out io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := out.Write(data)
		if n < 0 || n > len(data) {
			return fmt.Errorf("invalid write count %d", n)
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
