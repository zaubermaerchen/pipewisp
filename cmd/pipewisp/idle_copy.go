package main

// This file coordinates one-at-a-time reads and idle/resume transitions.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const idleReadBufferSize = 32 * 1024

type idleReadResult struct {
	data []byte
	err  error
}

type idleReadPump struct {
	in          io.Reader
	requests    chan struct{}
	readStarted chan struct{}
	results     chan idleReadResult
	stop        chan struct{}
	stopOnce    sync.Once
}

func newIdleReadPump(in io.Reader) *idleReadPump {
	pump := &idleReadPump{
		in:          in,
		requests:    make(chan struct{}, 1),
		readStarted: make(chan struct{}),
		// The pump waits for a request before every Read, so this slot can
		// hold only the current result and never provides read-ahead.
		results: make(chan idleReadResult, 1),
		stop:    make(chan struct{}),
	}
	go pump.read()
	return pump
}

func (pump *idleReadPump) read() {
	buffer := make([]byte, idleReadBufferSize)
	for {
		select {
		case <-pump.requests:
		case <-pump.stop:
			return
		}

		// Keep the pump from entering Read until the coordinator has observed
		// the handshake and started the active timer for this read.
		select {
		case pump.readStarted <- struct{}{}:
		case <-pump.stop:
			return
		}
		n, err := pump.in.Read(buffer)
		result := idleReadResult{data: buffer[:n], err: err}
		select {
		case pump.results <- result:
		case <-pump.stop:
			return
		}
		if err != nil {
			return
		}
	}
}

func (pump *idleReadPump) request() {
	select {
	case pump.requests <- struct{}{}:
	case <-pump.stop:
	}
}

func (pump *idleReadPump) stopReading(closeReader bool) {
	pump.stopOnce.Do(func() {
		if closeReader {
			closeInput(pump.in)
		}
		close(pump.stop)
	})
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
	opts          options
	diagnostics   io.Writer
	tracker       *signalTracker
	state         *lifecycleState
	pump          *idleReadPump
	writePump     *idleWritePump
	timer         *time.Timer
	timerC        <-chan time.Time
	active        bool
	idle          bool
	requested     bool
	writeDone     <-chan error
	pendingErr    error
	firstDataSeen bool
	done          completion
}

func runIdleCopy(opts options, in io.Reader, out io.Writer, diagnostics io.Writer, tracker *signalTracker) completion {
	state := newLifecycleState()
	return runIdleCopyWithState(opts, in, state.writer(out), diagnostics, tracker, state)
}

func runIdleCopyWithState(opts options, in io.Reader, out io.Writer, diagnostics io.Writer, tracker *signalTracker, state *lifecycleState) completion {
	runner := &idleCopyRunner{
		opts:        opts,
		diagnostics: diagnostics,
		tracker:     tracker,
		state:       state,
		pump:        newIdleReadPump(in),
		writePump:   newIdleWritePump(out),
		timer:       time.NewTimer(opts.idle),
	}
	runner.stopTimer()
	defer func() {
		runner.stopTimer()
		runner.pump.stopReading(false)
		runner.writePump.stopWriting()
	}()

	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return runner.done
	}
	runner.requestRead()

	for {
		select {
		case sig := <-tracker.signals:
			if sig == nil {
				continue
			}
			runner.rememberSignal(sig)
			runner.abortForSignal(tracker.firstSignal())
			return runner.done
		case <-runner.pump.readStarted:
			if !runner.handleReadStarted() {
				return runner.done
			}
		case result := <-runner.pump.results:
			if !runner.handleRead(result) {
				return runner.done
			}
		case err := <-runner.writeDone:
			if !runner.handleWriteDone(err) {
				return runner.done
			}
		case <-runner.timerC:
			runner.timerC = nil
			// A result already published by the pump is an observed read and
			// must be handled before declaring the stream idle. In particular,
			// this preserves an observed EOF over a timer ready at the same time.
			select {
			case result := <-runner.pump.results:
				if !runner.handleRead(result) {
					return runner.done
				}
				if len(result.data) == 0 && result.err == nil {
					if !runner.handleIdle() {
						return runner.done
					}
				}
			default:
				if !runner.handleIdle() {
					return runner.done
				}
			}
		}
	}
}

func (runner *idleCopyRunner) requestRead() {
	runner.pump.request()
	runner.requested = true
}

func (runner *idleCopyRunner) handleReadStarted() bool {
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	if runner.requested && runner.active && !runner.idle && runner.timerC == nil {
		runner.startTimer()
	}
	return true
}

func (runner *idleCopyRunner) handleRead(result idleReadResult) bool {
	runner.requested = false
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}

	if len(result.data) > 0 {
		firstDataPending := runner.opts.onFirstDataSet && !runner.firstDataSeen
		if firstDataPending {
			// Run this before any resume hook so the initial data transition is
			// always first-data -> write. A failure still permits this chunk to
			// be written, but prevents all subsequent reads.
			if !runner.handleFirstData() {
				return false
			}
		}
		if runner.idle && !firstDataPending {
			// A resume hook is part of the transition into active mode. A
			// failed hook does not roll back the transition or discard data.
			if runner.opts.onResumeSet {
				if sig := runner.pollSignal(); sig != nil {
					runner.abortForSignal(sig)
					return false
				}
				if err := runHookWithContextAndTracker("on-resume", runner.opts.onResume, runner.state.snapshot("resume", ""), runner.diagnostics, runner.opts.hookTimeout, runner.tracker); err != nil {
					runner.done.resumeErr = err
				}
				if sig := runner.pollSignal(); sig != nil {
					runner.abortForSignal(sig)
					return false
				}
			}
			runner.idle = false
		}

		runner.stopTimer()
		runner.pendingErr = result.err
		runner.writeDone = runner.writePump.results
		if !runner.writePump.request(result.data) {
			runner.writeDone = nil
			if sig := runner.pollSignal(); sig != nil {
				runner.abortForSignal(sig)
			} else {
				runner.stopAfterCopyError()
			}
			return false
		}
		return true
	} else if result.err != nil {
		runner.recordReadError(result.err)
		runner.stopAfterCopyError()
		return false
	}

	runner.requestRead()
	return true
}

func (runner *idleCopyRunner) handleWriteDone(err error) bool {
	runner.writeDone = nil
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	if err != nil {
		runner.done.copyErr = err
		runner.stopAfterCopyError()
		return false
	}
	if runner.pendingErr != nil {
		runner.recordReadError(runner.pendingErr)
		runner.stopAfterCopyError()
		return false
	}
	if runner.done.firstDataHookFailed {
		// The chunk that triggered the first-data hook was already written, but
		// no later read is allowed after a hook failure.
		runner.stopAfterCopyError()
		return false
	}
	runner.pendingErr = nil
	runner.active = true
	runner.requestRead()
	return true
}

func (runner *idleCopyRunner) handleFirstData() bool {
	runner.firstDataSeen = true
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}
	if err := runHookWithContextAndTracker("on-first-data", runner.opts.onFirstData, runner.state.snapshot("first-data", ""), runner.diagnostics, runner.opts.hookTimeout, runner.tracker); err != nil {
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
	runner.active = false
	runner.idle = true
	if runner.opts.onIdleSet {
		if sig := runner.pollSignal(); sig != nil {
			runner.abortForSignal(sig)
			return false
		}
		if err := runHookWithContextAndTracker("on-idle", runner.opts.onIdle, runner.state.snapshot("idle", ""), runner.diagnostics, runner.opts.hookTimeout, runner.tracker); err != nil {
			runner.done.idleErr = err
		}
		if sig := runner.pollSignal(); sig != nil {
			runner.abortForSignal(sig)
			return false
		}
	}
	if !runner.requested {
		runner.requestRead()
	}
	return true
}

func (runner *idleCopyRunner) startTimer() {
	if !runner.active || runner.idle || runner.timerC != nil {
		return
	}
	runner.timer.Reset(runner.opts.idle)
	runner.timerC = runner.timer.C
}

func (runner *idleCopyRunner) stopTimer() {
	if runner.timer == nil {
		return
	}
	if !runner.timer.Stop() {
		select {
		case <-runner.timer.C:
		default:
		}
	}
	runner.timerC = nil
}

func (runner *idleCopyRunner) recordReadError(err error) {
	if !errors.Is(err, io.EOF) {
		runner.done.copyErr = err
	}
}

func (runner *idleCopyRunner) stopAfterCopyError() {
	runner.stopTimer()
	runner.pump.stopReading(false)
	runner.writePump.stopWriting()
}

func (runner *idleCopyRunner) rememberSignal(sig os.Signal) {
	runner.tracker.remember(sig)
}

func (runner *idleCopyRunner) pollSignal() os.Signal {
	return runner.tracker.poll()
}

func (runner *idleCopyRunner) abortForSignal(sig os.Signal) {
	runner.done.signal = sig
	runner.stopTimer()
	runner.pump.stopReading(true)
	runner.writePump.stopWriting()
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
