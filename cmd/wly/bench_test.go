package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProfiles(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "jvm-profiles.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoProfiles = `
[profiles.baseline-j21]
image = "itzg/minecraft-server:java21"
aikar = true
[profiles.j25-g1-coh]
image = "itzg/minecraft-server:java25"
aikar = true
xx = ["-XX:+UseCompactObjectHeaders"]
`

// The failures worth testing here are the ones that happen before any container
// starts, because those are the ones a person hits at 2am with a typo.
func TestBenchCmdRejectsBadInput(t *testing.T) {
	good := writeProfiles(t, twoProfiles)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing profiles file", []string{"--profiles", filepath.Join(t.TempDir(), "nope.toml")}, "reading profiles"},
		{"unparseable profiles", []string{"--profiles", writeProfiles(t, "not toml {{")}, "parsing profiles"},
		{"unknown workload", []string{"--profiles", good, "--workload", "sideways"}, "unknown workload"},
		{"unknown profile", []string{"--profiles", good, "--only", "ghost"}, "no enabled profile named ghost"},
		{"bad flag", []string{"--profiles", good, "--nonsense"}, "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			err := benchCmd(tt.args, &out)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("benchCmd(%v) = %v, want an error containing %q", tt.args, err, tt.want)
			}
		})
	}
}

// Without a docker daemon every probe fails, which would make every flag look
// refused and produce a full report that means nothing. It must refuse instead.
func TestBenchCmdRefusesToRunWithoutAWorkingProbe(t *testing.T) {
	var out strings.Builder
	err := benchCmd([]string{"--profiles", writeProfiles(t, twoProfiles), "--dry-run"}, &out)
	if err == nil {
		t.Skip("a docker daemon is available here, so the unprobeable path cannot be exercised")
	}
	if !strings.Contains(err.Error(), "cannot probe") {
		t.Fatalf("err = %v, want it to name the probe as the problem", err)
	}
}
