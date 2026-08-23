package main

// This file verifies version source precedence and the development fallback.

import (
	"bytes"
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name          string
		linkerValue   string
		buildInfoOK   bool
		buildVersion  string
		buildSettings []debug.BuildSetting
		want          string
	}{
		{
			name:          "linker value takes precedence",
			linkerValue:   "v0.2.0",
			buildInfoOK:   true,
			buildVersion:  "v0.0.0-20260823093759-0a06056e3945",
			buildSettings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0a06056e3945"}},
			want:          "v0.2.0",
		},
		{
			name:         "build info is second choice",
			buildInfoOK:  true,
			buildVersion: "v0.2.0",
			want:         "v0.2.0",
		},
		{
			name:         "devel build info falls back",
			buildInfoOK:  true,
			buildVersion: "(devel)",
			want:         "devel",
		},
		{
			name:          "vcs stamped pseudo-version falls back",
			buildInfoOK:   true,
			buildVersion:  "v0.2.0-0.20260823093759-0a06056e3945",
			buildSettings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0a06056e3945"}},
			want:          "devel",
		},
		{
			name:         "base-less pseudo-version falls back",
			buildInfoOK:  true,
			buildVersion: "v0.0.0-20260823093759-0a06056e3945",
			want:         "devel",
		},
		{
			name:         "release pseudo-version falls back",
			buildInfoOK:  true,
			buildVersion: "v0.2.1-0.20260823093759-0a06056e3945",
			want:         "devel",
		},
		{
			name:         "prerelease pseudo-version falls back",
			buildInfoOK:  true,
			buildVersion: "v0.2.0-rc.1.0.20260823093759-0a06056e3945",
			want:         "devel",
		},
		{
			name:         "pseudo-version with incompatible suffix falls back",
			buildInfoOK:  true,
			buildVersion: "v2.0.1-0.20260823093759-0a06056e3945+incompatible",
			want:         "devel",
		},
		{
			name:         "pseudo-version with uppercase revision falls back",
			buildInfoOK:  true,
			buildVersion: "v0.2.1-0.20260823093759-ABCDEF123456",
			want:         "devel",
		},
		{
			name:         "pseudo-version with short revision falls back",
			buildInfoOK:  true,
			buildVersion: "v0.2.1-0.20260823093759-abc123",
			want:         "devel",
		},
		{
			name:         "pseudo-version with custom metadata falls back",
			buildInfoOK:  true,
			buildVersion: "v0.2.1-0.20260823093759-abcdef+custom.meta-1",
			want:         "devel",
		},
		{
			name:         "prerelease version is adopted",
			buildInfoOK:  true,
			buildVersion: "v0.2.0-rc.1",
			want:         "v0.2.0-rc.1",
		},
		{
			name:         "incompatible release version is adopted",
			buildInfoOK:  true,
			buildVersion: "v2.0.0+incompatible",
			want:         "v2.0.0+incompatible",
		},
		{
			name:          "empty vcs revision does not block version",
			buildInfoOK:   true,
			buildVersion:  "v0.2.0",
			buildSettings: []debug.BuildSetting{{Key: "vcs.revision", Value: ""}},
			want:          "v0.2.0",
		},
		{
			name:        "missing build info falls back",
			buildInfoOK: false,
			want:        "devel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info *debug.BuildInfo
			if tt.buildInfoOK {
				info = &debug.BuildInfo{
					Main:     debug.Module{Version: tt.buildVersion},
					Settings: tt.buildSettings,
				}
			}
			if got := resolveVersion(tt.linkerValue, info, tt.buildInfoOK); got != tt.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunCLIVersionUsesLinkerVersion(t *testing.T) {
	previous := version
	version = "v0.2.0"
	t.Cleanup(func() { version = previous })

	var output bytes.Buffer
	var diagnostics bytes.Buffer
	if got := runCLI([]string{"--version"}, panicReader{}, &output, &diagnostics); got != 0 {
		t.Fatalf("runCLI() exit code = %d, want 0", got)
	}
	if got, want := output.String(), "pipewisp v0.2.0\n"; got != want {
		t.Fatalf("runCLI() output = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("runCLI() diagnostics = %q, want empty", diagnostics.String())
	}
}
