//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package pipewisp

// This file skips event descriptor tests on platforms without descriptor support.

import (
	"os"
	"testing"
)

func newObservationPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	t.Skip("event descriptors are unsupported on this platform")
	return nil, nil
}

func newEventCapture(t *testing.T) (*os.File, *os.File, func()) {
	t.Helper()
	t.Skip("event descriptors are unsupported on this platform")
	return nil, nil, nil
}

func testEventFD(t *testing.T, _ *os.File) uintptr {
	t.Helper()
	t.Skip("event descriptors are unsupported on this platform")
	return 0
}
