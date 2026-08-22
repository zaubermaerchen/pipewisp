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
			args: []string{"--on", "prepare", "--off", "cleanup"},
			want: options{on: "prepare", onSet: true, off: "cleanup", offSet: true},
		},
		{
			name: "first-data hook separated",
			args: []string{"--on-first-data", "observe"},
			want: options{onFirstData: "observe", onFirstDataSet: true},
		},
		{
			name: "equals form",
			args: []string{"--on=prepare", "--on-first-data=observe", "--off=cleanup"},
			want: options{on: "prepare", onSet: true, onFirstData: "observe", onFirstDataSet: true, off: "cleanup", offSet: true},
		},
		{
			name: "all lifecycle options",
			args: []string{"--on", "prepare", "--on-first-data", "observe", "--off", "cleanup", "--idle", "25ms", "--on-idle", "idle", "--on-resume", "resume"},
			want: options{
				on:             "prepare",
				onSet:          true,
				onFirstData:    "observe",
				onFirstDataSet: true,
				off:            "cleanup",
				offSet:         true,
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

func TestParseRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "duplicate on separated",
			args:    []string{"--on", "first", "--on", "second"},
			message: "--on specified more than once",
		},
		{
			name:    "duplicate on equals",
			args:    []string{"--on", "first", "--on=second"},
			message: "--on specified more than once",
		},
		{
			name:    "duplicate off",
			args:    []string{"--off=first", "--off", "second"},
			message: "--off specified more than once",
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
			name:    "missing on value",
			args:    []string{"--on"},
			message: "missing value for --on",
		},
		{
			name:    "missing off value before another option",
			args:    []string{"--off", "--on", "prepare"},
			message: "missing value for --off",
		},
		{
			name:    "missing first-data value",
			args:    []string{"--on-first-data"},
			message: "missing value for --on-first-data",
		},
		{
			name:    "missing first-data value before another option",
			args:    []string{"--on-first-data", "--on", "prepare"},
			message: "missing value for --on-first-data",
		},
		{
			name:    "empty on equals value",
			args:    []string{"--on="},
			message: "empty command for --on",
		},
		{
			name:    "empty off separated value",
			args:    []string{"--off", ""},
			message: "empty command for --off",
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
			name:    "whitespace on separated value",
			args:    []string{"--on", " \t"},
			message: "empty command for --on",
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
			args:    []string{"--help", "--on", "prepare"},
			message: "--help cannot be combined",
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
