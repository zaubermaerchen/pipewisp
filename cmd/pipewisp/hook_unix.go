//go:build !windows

package main

// This file selects /bin/sh and defines the Unix hook process-group boundary.

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type hookBoundary struct {
	rootExited bool
}

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
		boundary.rootExited = true
		return os.ErrProcessDone
	}
	// Observe whether the root process is still alive without terminating it.
	// The process group kill below is the only stop operation so descendants
	// cannot be left running while the shell exits naturally on its own.
	rootErr := hook.Process.Signal(syscall.Signal(0))
	if errors.Is(rootErr, os.ErrProcessDone) {
		boundary.rootExited = true
	}
	groupErr := syscall.Kill(-hook.Process.Pid, syscall.SIGKILL)
	return combineHookStopErrors(rootErr, groupErr)
}

func (boundary *hookBoundary) killedRoot(*os.ProcessState) bool {
	return !boundary.rootExited
}

func (*hookBoundary) close() {}

func combineHookStopErrors(rootErr, groupErr error) error {
	var stopErrors []error
	if rootErr != nil && !errors.Is(rootErr, os.ErrProcessDone) {
		stopErrors = append(stopErrors, rootErr)
	}
	if groupErr != nil && !errors.Is(groupErr, syscall.ESRCH) {
		stopErrors = append(stopErrors, groupErr)
	}
	if len(stopErrors) > 0 {
		return errors.Join(stopErrors...)
	}
	if errors.Is(rootErr, os.ErrProcessDone) {
		return os.ErrProcessDone
	}
	return nil
}
