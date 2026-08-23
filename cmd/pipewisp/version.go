package main

// This file resolves the reported build version and renders --version output.

import (
	"fmt"
	"io"
	"regexp"
	"runtime/debug"
)

// version is populated for release builds with -ldflags -X main.version=....
var version string

// Go's pseudo-version grammar has no-base, post-release, and
// post-prerelease forms. Keep its revision and build metadata portions aligned
// with the toolchain while leaving ordinary semantic versions eligible.
var goPseudoVersionPattern = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func resolveVersion(linkerVersion string, buildInfo *debug.BuildInfo, buildInfoOK bool) string {
	if linkerVersion != "" {
		return linkerVersion
	}
	if buildInfoOK && buildInfo != nil && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" && !hasVCSRevision(buildInfo) && !isPseudoVersion(buildInfo.Main.Version) {
		return buildInfo.Main.Version
	}
	return "devel"
}

func isPseudoVersion(value string) bool {
	return goPseudoVersionPattern.MatchString(value)
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
