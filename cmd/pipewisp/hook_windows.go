//go:build windows

package main

// This file selects cmd.exe and defines the Windows hook process-tree boundary.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const hookJobTerminationExitCode uint32 = 1

type hookBoundary struct {
	job             windows.Handle
	rootExitedFirst bool
}

func newShellCommand(command string) *exec.Cmd {
	hook := exec.Command("cmd.exe", "/C", command)
	hook.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	return hook
}

func newHookBoundary() (*hookBoundary, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	// Ordinary descendants stay in the job, while a command that explicitly
	// requests Windows breakaway retains the documented deliberate escape hatch.
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &hookBoundary{job: job}, nil
}

func (boundary *hookBoundary) start(hook *exec.Cmd) error {
	if err := hook.Start(); err != nil {
		return err
	}

	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(hook.Process.Pid))
	if err != nil {
		boundary.cleanupStartedHook(hook, false)
		return err
	}
	assignErr := windows.AssignProcessToJobObject(boundary.job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		boundary.cleanupStartedHook(hook, false)
		return assignErr
	}

	if err := resumeHookPrimaryThread(uint32(hook.Process.Pid)); err != nil {
		boundary.cleanupStartedHook(hook, true)
		return err
	}
	return nil
}

func (boundary *hookBoundary) cleanupStartedHook(hook *exec.Cmd, assigned bool) {
	if hook.Process == nil {
		return
	}
	if assigned {
		if err := windows.TerminateJobObject(boundary.job, hookJobTerminationExitCode); err != nil {
			_ = hook.Process.Kill()
		}
	} else {
		_ = hook.Process.Kill()
	}
	_ = hook.Wait()
}

func (boundary *hookBoundary) stop(hook *exec.Cmd) error {
	if boundary.job == 0 {
		return os.ErrProcessDone
	}
	exited, observationErr := hookProcessExited(hook)
	boundary.rootExitedFirst = exited
	if err := windows.TerminateJobObject(boundary.job, hookJobTerminationExitCode); err != nil {
		// Direct termination keeps reap and output drain bounded, but the Job
		// failure is retained because descendant cleanup is no longer guaranteed.
		return errors.Join(observationErr, err, hook.Process.Kill())
	}
	return observationErr
}

func (boundary *hookBoundary) killedRoot(*os.ProcessState) bool {
	return !boundary.rootExitedFirst
}

func (boundary *hookBoundary) close() {
	if boundary.job == 0 {
		return
	}
	_ = windows.CloseHandle(boundary.job)
	boundary.job = 0
}

func resumeHookPrimaryThread(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == processID {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return errors.New("primary hook thread not found")
			}
			return err
		}
	}
}

func hookProcessExited(hook *exec.Cmd) (bool, error) {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(hook.Process.Pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return true, nil
		}
		return false, err
	}
	defer windows.CloseHandle(process)

	status, err := windows.WaitForSingleObject(process, 0)
	if err != nil {
		return false, err
	}
	switch status {
	case windows.WAIT_OBJECT_0:
		return true, nil
	case uint32(windows.WAIT_TIMEOUT):
		return false, nil
	default:
		return false, fmt.Errorf("unexpected hook process wait status %#x", status)
	}
}
