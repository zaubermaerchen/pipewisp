//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package pipewisp

// This file tests Unix event descriptor ownership, inheritance, and writes.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestEventEmitterDoesNotPassOwnedDescriptorToHooks(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	// Make inheritance observable: the descriptor supplied by the caller is
	// deliberately inheritable, while the duplicate owned by pipewisp must not
	// be passed to a hook.
	originalFD := int(eventsFile.Fd())
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
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("borrowed descriptor flags = %#x, want FD_CLOEXEC", flags)
	}
}

func TestEventEmitterDoesNotLeakBorrowedDescriptorToHooks(t *testing.T) {
	if os.Getenv("PIPEWISP_EVENT_FD_HELPER") == "1" {
		var output, diagnostics bytes.Buffer
		command := "printf polluted >&3 2>/dev/null || :"
		if status := Run([]string{"--events-fd", "3", "--on-ready", command}, strings.NewReader(""), &output, &diagnostics); status != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", status, diagnostics.String())
		}
		return
	}

	eventsFile, err := os.CreateTemp("", "pipewisp-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	command := exec.Command(os.Args[0], "-test.run=^TestEventEmitterDoesNotLeakBorrowedDescriptorToHooks$")
	command.Env = append(os.Environ(), "PIPEWISP_EVENT_FD_HELPER=1")
	command.ExtraFiles = []*os.File{eventsFile}
	var helperOutput, helperDiagnostics bytes.Buffer
	command.Stdout = &helperOutput
	command.Stderr = &helperDiagnostics
	if err := command.Run(); err != nil {
		t.Fatalf("hook isolation helper failed: %v\nstdout=%q\nstderr=%q", err, helperOutput.String(), helperDiagnostics.String())
	}

	if _, err := eventsFile.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(eventsFile)
	var records []lifecycleEventRecord
	for {
		var record lifecycleEventRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("event stream contains hook output: %v", err)
		}
		records = append(records, record)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("event count = %d, want %d: %#v", got, want, records)
	}
}

func TestEventEmitterClosesOnlyOwnedDescriptor(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	originalFD := int(eventsFile.Fd())
	emitter := newEventEmitter(originalFD, io.Discard)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	ownedFD := rawEventFD(t, emitter.file)
	emitter.close()

	if _, err := unix.FcntlInt(ownedFD, unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("owned descriptor error = %v, want EBADF", err)
	}
	if _, err := eventsFile.Stat(); err != nil {
		t.Fatalf("closing emitter closed caller descriptor: %v", err)
	}
}

func TestEventEmitterWriteDoesNotWaitForFullConsumer(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()
	writeFD := int(writeEvents.Fd())
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
	var output, diagnostics bytes.Buffer
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{"--events-fd", strconv.Itoa(writeFD)}, strings.NewReader("input"), &output, &diagnostics)
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
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one event warning", diagnostics.String())
	}
	if got, err := unix.FcntlInt(uintptr(writeFD), unix.F_GETFL, 0); err != nil {
		t.Fatal(err)
	} else if got&unix.O_NONBLOCK != originalFlags&unix.O_NONBLOCK {
		t.Fatalf("caller descriptor blocking mode = %#x, want %#x", got&unix.O_NONBLOCK, originalFlags&unix.O_NONBLOCK)
	}
}
