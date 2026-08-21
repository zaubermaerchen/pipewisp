package main

import (
	"runtime"
	"strings"
)

func hookOutputCommand(value string) string {
	if runtime.GOOS == "windows" {
		return "echo " + value
	}
	return "printf %s " + unixQuote(value)
}

func hookOutputAndErrorCommand(stdout, stderr string) string {
	if runtime.GOOS == "windows" {
		return "echo " + stdout + " & echo " + stderr + " 1>&2"
	}
	return "printf %s " + unixQuote(stdout) + "; printf %s " + unixQuote(stderr) + " >&2"
}

func failingHookCommand(output string) string {
	if runtime.GOOS == "windows" {
		return hookOutputCommand(output) + " & exit /b 7"
	}
	return hookOutputCommand(output) + "; exit 7"
}

func unixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
