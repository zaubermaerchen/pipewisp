package main

import (
	"runtime"
	"testing"
)

func TestNewShellCommandUsesPlatformShell(t *testing.T) {
	command := newShellCommand("echo test")

	if runtime.GOOS == "windows" {
		if got, want := command.Args, []string{"cmd.exe", "/C", "echo test"}; !sameStrings(got, want) {
			t.Fatalf("newShellCommand() args = %#v, want %#v", got, want)
		}
		return
	}

	if got, want := command.Args, []string{"/bin/sh", "-c", "echo test"}; !sameStrings(got, want) {
		t.Fatalf("newShellCommand() args = %#v, want %#v", got, want)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
