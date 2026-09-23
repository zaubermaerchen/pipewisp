//go:build windows

package pipewisp

// This file validates Windows no-wait pipe handles and duplicates them for event writes.

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validateEventDescriptor(fd int) error {
	handle := windows.Handle(fd)
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return err
	}
	if fileType != windows.FILE_TYPE_PIPE {
		return fmt.Errorf("handle must be a pipe")
	}
	return validateEventPipeMode(handle)
}

func validateEventPipeMode(handle windows.Handle) error {
	var mode uint32
	if err := windows.GetNamedPipeHandleState(handle, &mode, nil, nil, nil, nil, 0); err != nil {
		return fmt.Errorf("verify event pipe mode: %w", err)
	}
	if mode&windows.PIPE_NOWAIT == 0 {
		return fmt.Errorf("event pipe must have PIPE_NOWAIT set")
	}
	return nil
}

func duplicateEventFile(fd int) (*os.File, error) {
	var owned windows.Handle
	if err := windows.DuplicateHandle(
		windows.CurrentProcess(),
		windows.Handle(fd),
		windows.CurrentProcess(),
		&owned,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	closeOwnedHandle := true
	defer func() {
		if closeOwnedHandle {
			_ = windows.CloseHandle(owned)
		}
	}()

	file := os.NewFile(uintptr(owned), "pipewisp events")
	if file == nil {
		return nil, fmt.Errorf("invalid duplicated event handle %v", owned)
	}
	closeOwnedHandle = false
	return file, nil
}

func writeEvent(file *os.File, data []byte) (int, error) {
	connection, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n uint32
	var writeErr error
	if err := connection.Control(func(raw uintptr) {
		handle := windows.Handle(raw)
		if err := validateEventPipeMode(handle); err != nil {
			writeErr = err
			return
		}
		writeErr = windows.WriteFile(handle, data, &n, nil)
	}); err != nil {
		return 0, err
	}
	return int(n), writeErr
}
