//go:build !windows

package main

// This file selects /bin/sh and defines the Unix hook process-group boundary.

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type hookBoundary struct{}

func newShellCommand(command string) *exec.Cmd {
	hook := exec.Command("/bin/sh", "-c", command)
	// A private process group gives ordinary descendants the same cancellation
	// boundary as the shell without affecting processes outside this hook.
	hook.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return hook
}

func newHookBoundary() (*hookBoundary, error) {
	return &hookBoundary{}, nil
}

func (boundary *hookBoundary) start(hook *exec.Cmd) error {
	return hook.Start()
}

func (boundary *hookBoundary) stop(hook *exec.Cmd) error {
	if hook.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-hook.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		// Reap remains bounded even if group termination fails; retain the
		// boundary failure so descendants are never reported as fully stopped.
		return errors.Join(err, hook.Process.Kill())
	}
	return nil
}

func (*hookBoundary) killedRoot(state *os.ProcessState) bool {
	return processStateKilledByPipewisp(state)
}

func (*hookBoundary) close() {}
