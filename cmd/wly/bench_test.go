package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"weloveyou-mc/internal/bench"
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

func TestWriteReportsWritesBothOutputs(t *testing.T) {
	// This runs after every profile so a killed sweep keeps the rows it earned,
	// which makes it the piece most worth having a test on: a silent failure
	// here loses exactly the data the incremental write exists to protect.
	dir := t.TempDir()
	md := filepath.Join(dir, "B.md")
	js := filepath.Join(dir, "B.json")

	results := []bench.Result{{
		Profile:  bench.Baseline,
		Heap:     "6G",
		Workload: bench.WorkloadPack,
		Runs:     []bench.Run{{Chunks: 120, Mods: 143, Elapsed: 2 * time.Second, TPS: 19.8}},
	}}
	if err := writeReports(results, "host", md, js); err != nil {
		t.Fatalf("writeReports() = %v", err)
	}

	table, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("no table written: %v", err)
	}
	if !strings.Contains(string(table), bench.Baseline) {
		t.Errorf("table has no rows:\n%s", table)
	}

	raw, err := os.ReadFile(js)
	if err != nil {
		t.Fatalf("no json written: %v", err)
	}
	var doc struct {
		Host      string                     `json:"host"`
		Workloads map[string]json.RawMessage `json:"workloads"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json is not valid: %v", err)
	}
	if doc.Host != "host" {
		t.Errorf("host = %q", doc.Host)
	}
	if _, ok := doc.Workloads["pack"]; !ok {
		t.Errorf("pack workload missing from json:\n%s", raw)
	}
}

func TestWriteReportsSkipsJSONWhenNotWanted(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "B.md")
	if err := writeReports(nil, "h", md, ""); err != nil {
		t.Fatalf("writeReports() = %v", err)
	}
	if _, err := os.Stat(md); err != nil {
		t.Errorf("table should still be written: %v", err)
	}
}

func TestWriteReportsReportsAnUnwritablePath(t *testing.T) {
	// A directory that does not exist stands in for any write failure. The
	// caller downgrades this to a warning mid-sweep, so it has to be an error
	// here or the sweep would carry on believing it had saved something.
	missing := filepath.Join(t.TempDir(), "nope", "B.md")
	if err := writeReports(nil, "h", missing, ""); err == nil {
		t.Error("writing into a missing directory should fail")
	}
}
