// Package buildinfo carries the version stamp the release workflow injects,
// and formats it identically for both binaries.
//
// The values are set with -ldflags -X at build time. A plain `go build` leaves
// them at their defaults, which is how a developer build identifies itself.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Overwritten via -ldflags "-X weloveyou-mc/internal/buildinfo.version=..."
var (
	version = "dev"
	commit  = ""
)

// Version reports the release version, or "dev" for an unstamped build.
func Version() string { return version }

// Commit reports the git SHA the binary was built from. It falls back to the
// revision Go embeds via VCS stamping, so a plain `go build` in a clean
// checkout still identifies its source.
func Commit() string {
	if commit != "" {
		return commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// String renders the one line every binary prints for `version`.
func String(name string) string {
	s := fmt.Sprintf("%s %s", name, Version())
	if c := Commit(); c != "" {
		if len(c) > 12 {
			c = c[:12]
		}
		s += " (" + c + ")"
	}
	return s + fmt.Sprintf(" %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
