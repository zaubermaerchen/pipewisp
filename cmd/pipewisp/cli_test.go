package main

// This file tests valid option combinations and rejected CLI arguments.

import (
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want options
	}{
		{
			name: "no hooks",
			args: nil,
			want: options{},
		},
		{
			name: "both hooks",
			args: []string{"--on-ready", "prepare", "--on-shutdown", "cleanup"},
			want: options{onReady: "prepare", onReadySet: true, onShutdown: "cleanup", onShutdownSet: true},
		},
		{
			name: "first-data hook separated",
			args: []string{"--on-first-data", "observe"},
			want: options{onFirstData: "observe", onFirstDataSet: true},
		},
		{
			name: "equals form",
			args: []string{"--on-ready=prepare", "--on-first-data=observe", "--on-shutdown=cleanup"},
			want: options{onReady: "prepare", onReadySet: true, onFirstData: "observe", onFirstDataSet: true, onShutdown: "cleanup", onShutdownSet: true},
		},
		{
			name: "hook timeout separated",
			args: []string{"--hook-timeout", "250ms"},
			want: options{hookTimeout: 250 * time.Millisecond, hookTimeoutSet: true},
		},
		{
			name: "hook timeout equals",
			args: []string{"--hook-timeout=2s"},
			want: options{hookTimeout: 2 * time.Second, hookTimeoutSet: true},
		},
		{
			name: "all lifecycle options",
			args: []string{"--on-ready", "prepare", "--on-first-data", "observe", "--on-shutdown", "cleanup", "--idle", "25ms", "--on-idle", "idle", "--on-resume", "resume"},
			want: options{
				onReady:        "prepare",
				onReadySet:     true,
				onFirstData:    "observe",
				onFirstDataSet: true,
				onShutdown:     "cleanup",
				onShutdownSet:  true,
				idle:           25 * time.Millisecond,
				idleSet:        true,
				onIdle:         "idle",
				onIdleSet:      true,
				onResume:       "resume",
				onResumeSet:    true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, help, err := parseArgs(tt.args)
			if err != nil {
				t.Fatalf("parseArgs() error = %v", err)
			}
			if help {
				t.Fatal("parseArgs() help = true, want false")
			}
			if got != tt.want {
				t.Fatalf("parseArgs() options = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			got, help, err := parseArgs([]string{arg})
			if err != nil {
				t.Fatalf("parseArgs() error = %v", err)
			}
			if !help {
				t.Fatal("parseArgs() help = false, want true")
			}
			if got != (options{}) {
				t.Fatalf("parseArgs() options = %#v, want empty options", got)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	got, help, err := parseArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if help {
		t.Fatal("parseArgs() help = true, want false")
	}
	if !got.showVersion {
		t.Fatalf("parseArgs() showVersion = false, want true: %#v", got)
	}
}

func TestParseRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "duplicate on separated",
			args:    []string{"--on-ready", "first", "--on-ready", "second"},
			message: "--on-ready specified more than once",
		},
		{
			name:    "duplicate on equals",
			args:    []string{"--on-ready", "first", "--on-ready=second"},
			message: "--on-ready specified more than once",
		},
		{
			name:    "duplicate shutdown",
			args:    []string{"--on-shutdown=first", "--on-shutdown", "second"},
			message: "--on-shutdown specified more than once",
		},
		{
			name:    "duplicate first-data separated",
			args:    []string{"--on-first-data", "first", "--on-first-data", "second"},
			message: "--on-first-data specified more than once",
		},
		{
			name:    "duplicate first-data equals",
			args:    []string{"--on-first-data", "first", "--on-first-data=second"},
			message: "--on-first-data specified more than once",
		},
		{
			name:    "duplicate hook timeout separated",
			args:    []string{"--hook-timeout", "1s", "--hook-timeout", "2s"},
			message: "--hook-timeout specified more than once",
		},
		{
			name:    "duplicate hook timeout equals",
			args:    []string{"--hook-timeout=1s", "--hook-timeout=2s"},
			message: "--hook-timeout specified more than once",
		},
		{
			name:    "missing ready value",
			args:    []string{"--on-ready"},
			message: "missing value for --on-ready",
		},
		{
			name:    "missing ready value before another option",
			args:    []string{"--on-ready", "--on-shutdown", "cleanup"},
			message: "missing value for --on-ready",
		},
		{
			name:    "missing shutdown value before another option",
			args:    []string{"--on-shutdown", "--on-ready", "prepare"},
			message: "missing value for --on-shutdown",
		},
		{
			name:    "missing first-data value",
			args:    []string{"--on-first-data"},
			message: "missing value for --on-first-data",
		},
		{
			name:    "missing first-data value before another option",
			args:    []string{"--on-first-data", "--on-ready", "prepare"},
			message: "missing value for --on-first-data",
		},
		{
			name:    "missing hook timeout value",
			args:    []string{"--hook-timeout"},
			message: "missing value for --hook-timeout",
		},
		{
			name:    "missing hook timeout value before another option",
			args:    []string{"--hook-timeout", "--on-ready", "prepare"},
			message: "missing value for --hook-timeout",
		},
		{
			name:    "empty hook timeout equals value",
			args:    []string{"--hook-timeout="},
			message: "empty duration for --hook-timeout",
		},
		{
			name:    "invalid hook timeout",
			args:    []string{"--hook-timeout", "later"},
			message: "invalid duration for --hook-timeout",
		},
		{
			name:    "zero hook timeout",
			args:    []string{"--hook-timeout=0s"},
			message: "--hook-timeout must be greater than zero",
		},
		{
			name:    "negative hook timeout",
			args:    []string{"--hook-timeout", "-1ms"},
			message: "--hook-timeout must be greater than zero",
		},
		{
			name:    "empty on equals value",
			args:    []string{"--on-ready="},
			message: "empty command for --on-ready",
		},
		{
			name:    "empty ready separated value",
			args:    []string{"--on-ready", ""},
			message: "empty command for --on-ready",
		},
		{
			name:    "empty shutdown separated value",
			args:    []string{"--on-shutdown", ""},
			message: "empty command for --on-shutdown",
		},
		{
			name:    "empty shutdown equals value",
			args:    []string{"--on-shutdown="},
			message: "empty command for --on-shutdown",
		},
		{
			name:    "empty first-data equals value",
			args:    []string{"--on-first-data="},
			message: "empty command for --on-first-data",
		},
		{
			name:    "whitespace first-data separated value",
			args:    []string{"--on-first-data", " \t"},
			message: "empty command for --on-first-data",
		},
		{
			name:    "whitespace ready separated value",
			args:    []string{"--on-ready", " \t"},
			message: "empty command for --on-ready",
		},
		{
			name:    "old ready option rejected",
			args:    []string{"--on", "prepare"},
			message: "unknown option --on",
		},
		{
			name:    "old ready equals option rejected",
			args:    []string{"--on=prepare"},
			message: "unknown option --on=prepare",
		},
		{
			name:    "old shutdown option rejected",
			args:    []string{"--off", "cleanup"},
			message: "unknown option --off",
		},
		{
			name:    "old shutdown equals option rejected",
			args:    []string{"--off=cleanup"},
			message: "unknown option --off=cleanup",
		},
		{
			name:    "unknown long option",
			args:    []string{"--unknown"},
			message: "unknown option --unknown",
		},
		{
			name:    "unknown short option",
			args:    []string{"-x"},
			message: "unknown option -x",
		},
		{
			name:    "positional argument",
			args:    []string{"input.txt"},
			message: "unexpected positional argument input.txt",
		},
		{
			name:    "help with another argument",
			args:    []string{"--help", "--on-ready", "prepare"},
			message: "--help cannot be combined",
		},
		{
			name:    "version with another argument",
			args:    []string{"--version", "--on-ready", "prepare"},
			message: "--version cannot be combined",
		},
		{
			name:    "short version option is unsupported",
			args:    []string{"-v"},
			message: "unknown option -v",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, help, err := parseArgs(tt.args)
			if err == nil {
				t.Fatal("parseArgs() error = nil, want error")
			}
			if help {
				t.Fatal("parseArgs() help = true, want false")
			}
			if !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("parseArgs() error = %q, want substring %q", err, tt.message)
			}
		})
	}
}
