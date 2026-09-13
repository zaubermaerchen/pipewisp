package pipewisp

// This file tests event file descriptor argument parsing on every platform.

import "testing"

func TestParseEventsFDErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "missing", args: []string{"--events-fd"}},
		{name: "non numeric", args: []string{"--events-fd=three"}},
		{name: "stdin", args: []string{"--events-fd", "0"}},
		{name: "stderr", args: []string{"--events-fd", "2"}},
		{name: "duplicate", args: []string{"--events-fd=3", "--events-fd", "4"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := parseArgs(tt.args); err == nil {
				t.Fatal("parseArgs() error = nil, want error")
			}
		})
	}
}
