package main

// This file resolves the reported build version and renders --version output.

import (
	"fmt"
	"io"
	"runtime/debug"
)

// version is populated for release builds with -ldflags -X main.version=....
var version string

func resolveVersion(linkerVersion string, buildInfo *debug.BuildInfo, buildInfoOK bool) string {
	if linkerVersion != "" {
		return linkerVersion
	}
	if buildInfoOK && buildInfo != nil && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" && !hasVCSRevision(buildInfo) {
		return buildInfo.Main.Version
	}
	return "devel"
}

func hasVCSRevision(buildInfo *debug.BuildInfo) bool {
	if buildInfo == nil {
		return false
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return true
		}
	}
	return false
}

func currentVersion() string {
	buildInfo, ok := debug.ReadBuildInfo()
	return resolveVersion(version, buildInfo, ok)
}

func printVersion(out io.Writer) {
	_, _ = fmt.Fprintf(out, "pipewisp %s\n", currentVersion())
}
