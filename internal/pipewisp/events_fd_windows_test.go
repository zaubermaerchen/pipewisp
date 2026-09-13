//go:build windows

package pipewisp

// This file tests Windows event handle ownership and inheritance flags.

import (
	"bytes"
	"os"
	"syscall"
	"testing"
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

var getHandleInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("GetHandleInformation")

func windowsEventHandleFlags(handle windows.Handle) (uint32, error) {
	var flags uint32
	result, _, err := getHandleInformation.Call(uintptr(handle), uintptr(unsafe.Pointer(&flags)))
	if result == 0 {
		return 0, err
	}
	return flags, nil
}
