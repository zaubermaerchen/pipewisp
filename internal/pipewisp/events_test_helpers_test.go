//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || windows

package pipewisp

// This file supplies nonblocking observation pipes to event tests.

import (
	"io"
	"os"
	"testing"
)

func newObservationPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := setTestEventNonblocking(write); err != nil {
		_ = read.Close()
		_ = write.Close()
		t.Fatal(err)
	}
	return read, write
}

func newEventCapture(t *testing.T) (*os.File, *os.File, func()) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "events-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	read, write := newObservationPipe(t)
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(file, read)
		done <- err
	}()
	finished := false
	finish := func() {
		t.Helper()
		if finished {
			return
		}
		finished = true
		_ = write.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		_ = read.Close()
		if _, err := file.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(finish)
	return file, write, finish
}

func testEventFD(t *testing.T, file *os.File) uintptr {
	t.Helper()
	fd := file.Fd()
	if err := setTestEventNonblocking(file); err != nil {
		t.Fatal(err)
	}
	return fd
}
