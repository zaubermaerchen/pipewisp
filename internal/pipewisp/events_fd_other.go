//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package pipewisp

// This file disables event streams on platforms without a safe descriptor
// duplication and no-wait write primitive.

import (
	"errors"
	"os"
)

var errEventFDUnsupported = errors.New("event file descriptors are unsupported on this platform")

func setEventDescriptorNonInheritable(int) error {
	return errEventFDUnsupported
}

func duplicateEventFile(int) (*os.File, error) {
	return nil, errEventFDUnsupported
}

func writeEvent(file *os.File, data []byte) (int, error) {
	return file.Write(data)
}
