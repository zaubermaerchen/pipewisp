//go:build windows

package pipewisp

// This file tests Windows event handle ownership, inheritance, and pipe modes.

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestEventEmitterDoesNotPassOwnedHandleToHooks(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	original := windows.Handle(eventsFile.Fd())
	if err := windows.SetHandleInformation(original, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}

	var diagnostics bytes.Buffer
	emitter := newEventEmitter(int(eventsFile.Fd()), &diagnostics)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	defer emitter.file.Close()

	owned := windows.Handle(rawEventFD(t, emitter.file))
	if owned == original {
		t.Fatalf("event handle = %v, want a duplicate of %v", owned, original)
	}
	flags, err := windowsEventHandleFlags(owned)
	if err != nil {
		t.Fatal(err)
	}
	if flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatalf("owned event handle is inheritable: flags %#x", flags)
	}
	flags, err = windowsEventHandleFlags(original)
	if err != nil {
		t.Fatal(err)
	}
	if flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatalf("borrowed event handle is inheritable: flags %#x", flags)
	}
}

func TestEventEmitterClosesOnlyOwnedHandle(t *testing.T) {
	eventsFile, err := os.CreateTemp("", "pipewisp-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = eventsFile.Close()
		_ = os.Remove(eventsFile.Name())
	}()

	original := windows.Handle(eventsFile.Fd())
	emitter := newEventEmitter(int(original), &bytes.Buffer{})
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	owned := windows.Handle(rawEventFD(t, emitter.file))
	emitter.close()

	if _, err := windowsEventHandleFlags(owned); err == nil || err != windows.ERROR_INVALID_HANDLE {
		t.Fatalf("owned handle error = %v, want ERROR_INVALID_HANDLE", err)
	}
	if _, err := eventsFile.Stat(); err != nil {
		t.Fatalf("closing emitter closed caller handle: %v", err)
	}
}

func TestEventEmitterPreservesBorrowedPipeModeOnInitialization(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()

	borrowed := windows.Handle(writeEvents.Fd())
	originalMode := windowsEventPipeMode(t, borrowed)
	emitter := newEventEmitter(int(writeEvents.Fd()), io.Discard)
	if emitter == nil {
		t.Fatal("newEventEmitter() returned nil")
	}
	defer emitter.close()

	if got := windowsEventPipeMode(t, borrowed); got != originalMode {
		t.Fatalf("borrowed event pipe mode after initialization = %#x, want %#x", got, originalMode)
	}
}

func TestEventEmitterDoesNotWaitForFullPipeAndRestoresBorrowedMode(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()

	borrowed := windows.Handle(writeEvents.Fd())
	originalMode := windowsEventPipeMode(t, borrowed)
	fillWindowsEventPipe(t, borrowed, originalMode)

	var output, diagnostics bytes.Buffer
	status := make(chan int, 1)
	go func() {
		status <- Run([]string{"--events-fd", strconv.FormatUint(uint64(writeEvents.Fd()), 10)}, strings.NewReader("input"), &output, &diagnostics)
	}()

	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("Run() status = %d, want 0; diagnostics = %q", got, diagnostics.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() waited for an event consumer that could not accept a write")
	}
	if got, want := output.String(), "input"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one event warning", diagnostics.String())
	}
	if got := windowsEventPipeMode(t, borrowed); got != originalMode {
		t.Fatalf("borrowed event pipe mode after full write = %#x, want %#x", got, originalMode)
	}
}

func windowsEventPipeMode(t *testing.T, handle windows.Handle) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetNamedPipeHandleState(handle, &mode, nil, nil, nil, nil, 0); err != nil {
		t.Fatalf("GetNamedPipeHandleState(%v): %v", handle, err)
	}
	return mode
}

func fillWindowsEventPipe(t *testing.T, handle windows.Handle, originalMode uint32) {
	t.Helper()
	nowaitMode := originalMode | windows.PIPE_NOWAIT
	if err := windows.SetNamedPipeHandleState(handle, &nowaitMode, nil, nil); err != nil {
		t.Fatalf("SetNamedPipeHandleState(%v, PIPE_NOWAIT): %v", handle, err)
	}
	defer func() {
		if err := windows.SetNamedPipeHandleState(handle, &originalMode, nil, nil); err != nil {
			t.Errorf("restore pipe mode %#x: %v", originalMode, err)
		}
	}()

	buffer := make([]byte, 4096)
	var total uint32
	var writeErr error
	for total < 16<<20 {
		var written uint32
		writeErr = windows.WriteFile(handle, buffer, &written, nil)
		total += written
		if writeErr != nil {
			break
		}
		if written == 0 {
			t.Fatal("WriteFile made no progress while filling event pipe")
		}
	}
	if writeErr == nil {
		t.Fatalf("filled %d bytes without making event pipe unavailable", total)
	}
}

var getHandleInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("GetHandleInformation")

func windowsEventHandleFlags(handle windows.Handle) (uint32, error) {
	var flags uint32
	result, _, err := getHandleInformation.Call(uintptr(handle), uintptr(unsafe.Pointer(&flags)))
	if result == 0 {
		return 0, err
	}
	return flags, nil
}
