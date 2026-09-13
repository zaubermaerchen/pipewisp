//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package pipewisp

// This file duplicates Unix event descriptors and performs nonblocking writes.

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func setEventDescriptorNonInheritable(fd int) error {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		return err
	}
	_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC)
	return err
}

func duplicateEventFile(fd int) (*os.File, error) {
	// dup shares the open-file description with the caller. Save its status
	// flags before wrapping the duplicate because os.NewFile may make a pipe
	// nonblocking while registering it with the runtime poller.
	originalFlags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	ownedFD, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	closeOwnedFD := true
	defer func() {
		if closeOwnedFD {
			_ = unix.Close(ownedFD)
		}
	}()

	// Hooks are started with the normal exec descriptor set. CLOEXEC keeps the
	// event stream private to pipewisp while the caller retains ownership.
	if err := setEventDescriptorNonInheritable(ownedFD); err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(ownedFD), "pipewisp events")
	if file == nil {
		return nil, fmt.Errorf("invalid duplicated file descriptor %d", ownedFD)
	}
	if _, err := unix.FcntlInt(uintptr(ownedFD), unix.F_SETFL, originalFlags); err != nil {
		closeOwnedFD = false
		_ = file.Close()
		return nil, fmt.Errorf("restore event descriptor flags: %w", err)
	}
	closeOwnedFD = false
	return file, nil
}

func writeEvent(file *os.File, data []byte) (int, error) {
	connection, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n int
	var writeErr error
	if err := connection.Control(func(raw uintptr) {
		fd := int(raw)
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil {
			writeErr = err
			return
		}
		// dup shares the open-file description with the caller. Toggle the
		// shared status flag only for this raw write, then restore it before
		// returning so the caller's descriptor keeps its original mode.
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
			writeErr = err
			return
		}
		n, writeErr = unix.Write(fd, data)
		if _, restoreErr := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags); restoreErr != nil {
			if writeErr == nil {
				writeErr = restoreErr
			}
		}
	}); err != nil {
		return 0, err
	}
	return n, writeErr
}
