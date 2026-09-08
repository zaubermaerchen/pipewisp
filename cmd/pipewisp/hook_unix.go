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
	pgid       int
	tracked    bool
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
	if err := hook.Start(); err != nil {
		return err
	}
	boundary.pgid = hook.Process.Pid
	boundary.tracked = true
	return nil
}

func (boundary *hookBoundary) stop(hook *exec.Cmd) error {
	if hook.Process == nil {
		boundary.rootExited = true
		boundary.tracked = false
		return os.ErrProcessDone
	}
	// Observe whether the root process is still alive without terminating it.
	// The process group kill below is the only stop operation so descendants
	// cannot be left running while the shell exits naturally on its own.
	rootErr := hook.Process.Signal(syscall.Signal(0))
	if errors.Is(rootErr, os.ErrProcessDone) {
		boundary.rootExited = true
	}
	running, checkErr := boundary.hasRunningProcesses()
	if checkErr != nil {
		return combineHookStopErrors(rootErr, checkErr)
	}
	if !running {
		return combineHookStopErrors(rootErr, os.ErrProcessDone)
	}
	groupErr := syscall.Kill(-boundary.pgid, syscall.SIGKILL)
	if groupErr == nil {
		boundary.tracked = false
		// A naturally exited root can still own descendants. Once the group
		// was observed active and stopped, process cancellation is complete;
		// do not let the root's old ProcessDone result mask that outcome.
		if errors.Is(rootErr, os.ErrProcessDone) {
			return nil
		}
	}
	return combineHookStopErrors(rootErr, groupErr)
}

func (boundary *hookBoundary) hasRunningProcesses() (bool, error) {
	if !boundary.tracked || boundary.pgid <= 0 {
		return false, nil
	}
	if err := syscall.Kill(-boundary.pgid, syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			// Once the group is observed empty, forget its identifier before
			// a later shutdown/timeout can mistake a reused PGID for this hook.
			boundary.tracked = false
			return false, nil
		}
		if errors.Is(err, syscall.EPERM) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

func (boundary *hookBoundary) killedRoot(state *os.ProcessState) bool {
	return !boundary.rootExited && processStateKilledByPipewisp(state)
}

func (boundary *hookBoundary) close() {
	boundary.tracked = false
	boundary.pgid = 0
}

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
