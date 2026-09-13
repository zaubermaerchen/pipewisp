//go:build windows

package pipewisp

// This file duplicates Windows event handles and switches pipe writes to
// no-wait mode so an unavailable consumer cannot stall the pipeline.

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

	// PIPE_NOWAIT is a per-handle wait mode. Disk handles remain ordinary
	// files, whose writes do not wait for a consumer.
	fileType, err := windows.GetFileType(owned)
	if err != nil {
		return nil, err
	}
	if fileType == windows.FILE_TYPE_PIPE {
		mode := uint32(windows.PIPE_NOWAIT)
		if err := windows.SetNamedPipeHandleState(owned, &mode, nil, nil); err != nil {
			return nil, fmt.Errorf("set event pipe no-wait mode: %w", err)
		}
	}
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
		writeErr = windows.WriteFile(windows.Handle(raw), data, &n, nil)
	}); err != nil {
		return 0, err
	}
	return int(n), writeErr
}
