//go:build windows

package pipewisp

// This file duplicates Windows event handles and temporarily switches pipe
// writes to no-wait mode so an unavailable consumer cannot stall the pipeline.

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func setEventDescriptorNonInheritable(fd int) error {
	return windows.SetHandleInformation(windows.Handle(fd), windows.HANDLE_FLAG_INHERIT, 0)
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
		fileType, err := windows.GetFileType(handle)
		if err != nil {
			writeErr = err
			return
		}
		if fileType != windows.FILE_TYPE_PIPE {
			// Disk handles do not have a named-pipe wait mode and can be written
			// directly.
			writeErr = windows.WriteFile(handle, data, &n, nil)
			return
		}
		writeErr = writeEventPipe(handle, data, &n)
	}); err != nil {
		return 0, err
	}
	return int(n), writeErr
}

func writeEventPipe(handle windows.Handle, data []byte, n *uint32) error {
	var originalMode uint32
	if err := windows.GetNamedPipeHandleState(handle, &originalMode, nil, nil, nil, nil, 0); err != nil {
		return fmt.Errorf("get event pipe mode: %w", err)
	}

	nowaitMode := originalMode | windows.PIPE_NOWAIT
	if err := windows.SetNamedPipeHandleState(handle, &nowaitMode, nil, nil); err != nil {
		return fmt.Errorf("set event pipe no-wait mode: %w", err)
	}

	writeErr := windows.WriteFile(handle, data, n, nil)
	if err := windows.SetNamedPipeHandleState(handle, &originalMode, nil, nil); err != nil {
		if writeErr != nil {
			return fmt.Errorf("%w (restore event pipe mode: %v)", writeErr, err)
		}
		return fmt.Errorf("restore event pipe mode: %w", err)
	}
	return writeErr
}
