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
	in       io.Reader
	requests chan struct{}
	results  chan idleReadResult
	stop     chan struct{}
	stopOnce sync.Once
}

func newIdleReadPump(in io.Reader) *idleReadPump {
	pump := &idleReadPump{
		in:       in,
		requests: make(chan struct{}, 1),
		// The pump waits for a request before every Read, so this slot can
		// hold only the current result and never provides read-ahead.
		results: make(chan idleReadResult, 1),
		stop:    make(chan struct{}),
	}
	go pump.read()
	return pump
}

func (pump *idleReadPump) read() {
	for {
		select {
		case <-pump.requests:
		case <-pump.stop:
			return
		}

		buffer := make([]byte, idleReadBufferSize)
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

type idleCopyRunner struct {
	opts        options
	out         io.Writer
	diagnostics io.Writer
	tracker     *signalTracker
	pump        *idleReadPump
	timer       *time.Timer
	timerC      <-chan time.Time
	active      bool
	idle        bool
	requested   bool
	done        completion
}

func runIdleCopy(opts options, in io.Reader, out io.Writer, diagnostics io.Writer, tracker *signalTracker) completion {
	runner := &idleCopyRunner{
		opts:        opts,
		out:         out,
		diagnostics: diagnostics,
		tracker:     tracker,
		pump:        newIdleReadPump(in),
		timer:       time.NewTimer(opts.idle),
	}
	runner.stopTimer()
	defer func() {
		runner.stopTimer()
		runner.pump.stopReading(false)
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
			runner.abortForSignal(tracker.first)
			return runner.done
		case result := <-runner.pump.results:
			if !runner.handleRead(result) {
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

func (runner *idleCopyRunner) handleRead(result idleReadResult) bool {
	runner.requested = false
	if sig := runner.pollSignal(); sig != nil {
		runner.abortForSignal(sig)
		return false
	}

	if len(result.data) > 0 {
		if runner.idle {
			// A resume hook is part of the transition into active mode. A
			// failed hook does not roll back the transition or discard data.
			if runner.opts.onResumeSet {
				if sig := runner.pollSignal(); sig != nil {
					runner.abortForSignal(sig)
					return false
				}
				if err := runHook("resume", runner.opts.onResume, runner.diagnostics); err != nil {
					runner.done.resumeErr = err
				}
				if sig := runner.pollSignal(); sig != nil {
					runner.abortForSignal(sig)
					return false
				}
			}
			runner.idle = false
		}

		if err := writeIdleChunk(runner.out, result.data); err != nil {
			runner.done.copyErr = err
			runner.stopAfterCopyError()
			return false
		}
		if sig := runner.pollSignal(); sig != nil {
			runner.abortForSignal(sig)
			return false
		}

		if result.err != nil {
			runner.recordReadError(result.err)
			runner.stopAfterCopyError()
			return false
		}

		runner.active = true
		runner.resetTimer()
	} else if result.err != nil {
		runner.recordReadError(result.err)
		runner.stopAfterCopyError()
		return false
	}

	runner.requestRead()
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
		if err := runHook("idle", runner.opts.onIdle, runner.diagnostics); err != nil {
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

func (runner *idleCopyRunner) resetTimer() {
	if !runner.active {
		return
	}
	if !runner.timer.Stop() {
		select {
		case <-runner.timer.C:
		default:
		}
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
}

func (runner *idleCopyRunner) rememberSignal(sig os.Signal) {
	if runner.tracker.first == nil && sig != nil {
		runner.tracker.first = sig
	}
}

func (runner *idleCopyRunner) pollSignal() os.Signal {
	return runner.tracker.poll()
}

func (runner *idleCopyRunner) abortForSignal(sig os.Signal) {
	runner.done.signal = sig
	runner.stopTimer()
	runner.pump.stopReading(true)
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
