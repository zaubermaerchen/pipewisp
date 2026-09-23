//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package pipewisp

// This file configures Unix observation pipes for tests.

import (
	"golang.org/x/sys/unix"
	"os"
)

func setTestEventNonblocking(file *os.File) error { return unix.SetNonblock(int(file.Fd()), true) }
