//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package pipewisp

// This file tests Unix event descriptor ownership, inheritance, and writes.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestEventEmitterDoesNotPassOwnedDescriptorToHooks(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	defer readEvents.Close()
	defer writeEvents.Close()

	// Make inheritance observable: the descriptor supplied by the caller is
	// deliberately inheritable, while the duplicate owned by pipewisp must not
	// be passed to a hook.
	originalFD := int(testEventFD(t, writeEvents))
	if _, err := unix.FcntlInt(uintptr(originalFD), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}

	var diagnostics bytes.Buffer
	emitter := newEventEmitter(originalFD, &diagnostics)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	ownedFD := rawEventFD(t, emitter.file)
	if ownedFD == uintptr(originalFD) {
		t.Fatalf("event descriptor = %d, want a duplicate of %d", ownedFD, originalFD)
	}
	defer emitter.file.Close()

	flags, err := unix.FcntlInt(ownedFD, unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("owned event descriptor flags = %#x, want FD_CLOEXEC", flags)
	}
	flags, err = unix.FcntlInt(uintptr(originalFD), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC != 0 {
		t.Fatalf("borrowed descriptor flags = %#x, want inheritable", flags)
	}
}

func TestEventEmitterClosesOnlyOwnedDescriptor(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	defer readEvents.Close()
	defer writeEvents.Close()

	originalFD := int(testEventFD(t, writeEvents))
	emitter := newEventEmitter(originalFD, io.Discard)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	ownedFD := rawEventFD(t, emitter.file)
	emitter.close()

	if _, err := unix.FcntlInt(ownedFD, unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("owned descriptor error = %v, want EBADF", err)
	}
	if _, err := writeEvents.Stat(); err != nil {
		t.Fatalf("closing emitter closed caller descriptor: %v", err)
	}
}

func TestEventEmitterWriteDoesNotWaitForFullConsumer(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	defer readEvents.Close()
	defer writeEvents.Close()
	writeFD := int(testEventFD(t, writeEvents))
	originalFlags, err := unix.FcntlInt(uintptr(writeFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Fill the pipe through a temporary nonblocking duplicate while keeping the
	// caller's descriptor blocking. A direct write through the borrowed
	// descriptor would otherwise wait forever for a consumer.
	filler, err := unix.Dup(writeFD)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(filler)
	if err := unix.SetNonblock(filler, true); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Write(filler, buffer); err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			t.Fatal(err)
		}
	}
	if _, err := unix.FcntlInt(uintptr(writeFD), unix.F_SETFL, originalFlags); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	diagnostics := newNotifyingDiagnosticWriter()
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{"--events-fd", strconv.Itoa(writeFD)}, strings.NewReader("input"), &output, diagnostics)
	}()

	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run() waited for an event consumer that could not accept a write")
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	diagnostics.waitWarning(t)
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one event warning", diagnostics.String())
	}
	if got, err := unix.FcntlInt(uintptr(writeFD), unix.F_GETFL, 0); err != nil {
		t.Fatal(err)
	} else if got&unix.O_NONBLOCK != originalFlags&unix.O_NONBLOCK {
		t.Fatalf("caller descriptor blocking mode = %#x, want %#x", got&unix.O_NONBLOCK, originalFlags&unix.O_NONBLOCK)
	}
}

func TestRunDoesNotWaitForBlockedStderrWarning(t *testing.T) {
	readEvents, writeEvents := newObservationPipe(t)
	_ = readEvents.Close()
	defer writeEvents.Close()
	readDiagnostics, writeDiagnostics, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readDiagnostics.Close()
	defer writeDiagnostics.Close()
	fd := int(writeDiagnostics.Fd())
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := unix.Write(fd, make([]byte, 4096))
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{"--events-fd", strconv.FormatUint(uint64(testEventFD(t, writeEvents)), 10)}, strings.NewReader("input"), &output, writeDiagnostics)
	}()
	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("Run() status = %d, want 0", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() waited for blocked stderr warning")
	}
	if got := output.String(); got != "input" {
		t.Fatalf("stdout = %q, want input", got)
	}
}

func TestUnixEventDescriptorRejectsBlockingAndRegularFilesBeforeInput(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "events-")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	read, write := newObservationPipe(t)
	defer read.Close()
	defer write.Close()
	if err := unix.SetNonblock(int(write.Fd()), false); err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{regular, write} {
		input := &writerToReader{data: []byte("payload")}
		var output, diagnostics bytes.Buffer
		if got := Run([]string{"--events-fd", strconv.Itoa(int(file.Fd()))}, input, &output, &diagnostics); got != 2 {
			t.Fatalf("Run(%v) = %d, want 2; diagnostics = %q", file.Name(), got, diagnostics.String())
		}
		if input.writeToCalled || output.Len() != 0 {
			t.Fatal("invalid event descriptor processed stdin")
		}
	}
}

func TestUnixEventEmitterDisablesAfterModeChangeWithoutChangingBorrowedFlags(t *testing.T) {
	read, write := newObservationPipe(t)
	defer read.Close()
	defer write.Close()
	borrowed := int(testEventFD(t, write))
	diagnostics := newNotifyingDiagnosticWriter()
	emitter := newEventEmitter(borrowed, diagnostics)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	defer emitter.close()
	if err := unix.SetNonblock(borrowed, false); err != nil {
		t.Fatal(err)
	}
	emitter.emit("ready")
	emitter.emit("shutdown")
	diagnostics.waitWarning(t)
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("warnings = %d: %q", got, diagnostics.String())
	}
	flags, err := unix.FcntlInt(uintptr(borrowed), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK != 0 {
		t.Fatal("borrowed mode changed")
	}
}

func TestUnixEventDescriptorAcceptsNonblockingSocket(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pair[0])
	defer unix.Close(pair[1])
	if err := unix.SetNonblock(pair[0], true); err != nil {
		t.Fatal(err)
	}
	if err := validateEventDescriptor(pair[0]); err != nil {
		t.Fatalf("socket rejected: %v", err)
	}
}
