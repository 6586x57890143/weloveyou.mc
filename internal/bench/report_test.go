package bench

import (
	"strings"
	"testing"
	"time"
)

func run(chunks int, secs float64, rss float64, pauses ...float64) Run {
	return Run{
		Chunks: chunks, Elapsed: time.Duration(secs * float64(time.Second)),
		Startup: 10 * time.Second, PeakRSS: rss, GCPauses: pauses,
	}
}

func results() []Result {
	return []Result{
		{Profile: "j25-g1-coh", Workload: WorkloadVanilla, Runs: []Run{run(1200, 100, 3e9, 5, 9)}},
		{Profile: Baseline, Workload: WorkloadVanilla, Runs: []Run{run(1000, 100, 4e9, 6, 12)}},
		{Profile: Baseline, Workload: WorkloadPack, Runs: []Run{run(800, 100, 5e9, 8, 20)}},
		{Profile: "j25-g1-coh", Workload: WorkloadPack, Runs: []Run{run(816, 100, 4e9, 7, 18)}},
	}
}

func TestRenderStructure(t *testing.T) {
	out := Render(results(), "weloveyou-bench", time.Unix(0, 0))
	for _, want := range []string{
		"# Benchmarks",
		"Workload A - vanilla worldgen (control)",
		"Workload B - the pack we actually ship",
		"weloveyou-bench",
		"`baseline-j21`",
		"`j25-g1-coh`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q", want)
		}
	}
}

func TestBaselineRowComesFirst(t *testing.T) {
	out := Render(results(), "h", time.Unix(0, 0))
	section := out[strings.Index(out, "Workload A"):]
	base := strings.Index(section, "`"+Baseline+"`")
	other := strings.Index(section, "`j25-g1-coh`")
	if base < 0 || other < 0 || base > other {
		t.Errorf("the baseline row should lead its table (base=%d other=%d)", base, other)
	}
}

func TestDeltaHidesNoise(t *testing.T) {
	out := Render(results(), "h", time.Unix(0, 0))
	// +20% on the control is real and must be reported...
	if !strings.Contains(out, "+20.0%") {
		t.Error("a 20% gain should be reported as a number")
	}
	// ...while +2% on the pack is inside the noise floor of a shared VM and
	// must not be dressed up as a finding.
	packTable := out[strings.Index(out, "Workload B"):]
	if !strings.Contains(packTable, "| ~ |") {
		t.Errorf("a 2%% change should render as ~, got:\n%s", packTable)
	}
}

func TestDeltaFunction(t *testing.T) {
	tests := []struct {
		base, v float64
		want    string
	}{
		{100, 120, "+20.0%"},
		{100, 80, "-20.0%"},
		{100, 101, "~"},
		{0, 50, "-"},
	}
	for _, tt := range tests {
		if got := delta(tt.base, tt.v); got != tt.want {
			t.Errorf("delta(%v,%v) = %q, want %q", tt.base, tt.v, got, tt.want)
		}
	}
}

func TestDroppedFlagsAreSurfaced(t *testing.T) {
	// Silently dropping a refused flag would make two profiles look identical
	// when one of them never applied.
	rs := results()
	rs[0].Dropped = []string{"-XX:+UseNUMA"}
	out := Render(rs, "h", time.Unix(0, 0))
	if !strings.Contains(out, "refused during preflight") || !strings.Contains(out, "-XX:+UseNUMA") {
		t.Errorf("dropped flags were not surfaced:\n%s", out)
	}
}

func TestNoDroppedNoteWhenNothingDropped(t *testing.T) {
	if strings.Contains(Render(results(), "h", time.Unix(0, 0)), "refused during preflight") {
		t.Error("a clean sweep should not mention preflight")
	}
}

func TestRenderWithNoResults(t *testing.T) {
	out := Render(nil, "h", time.Unix(0, 0))
	if !strings.Contains(out, "# Benchmarks") || strings.Contains(out, "Workload A") {
		t.Errorf("empty sweep should render a header and no tables, got:\n%s", out)
	}
}

func TestRepeatSummary(t *testing.T) {
	one := []Result{{Runs: []Run{{}}}}
	if got := repeatSummary(one); got != "1 run per profile" {
		t.Errorf("repeatSummary = %q", got)
	}
	three := []Result{{Runs: []Run{{}, {}, {}}}}
	if got := repeatSummary(three); got != "3 runs per profile" {
		t.Errorf("repeatSummary = %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{3 * (1 << 30), "3.00 GiB"},
		{512 * (1 << 20), "512 MiB"},
		{900, "900 B"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBaselineOfFallsBackToFirstRow(t *testing.T) {
	// A sweep that excluded the baseline still needs something to compare to,
	// and reporting nothing would be worse than reporting relative to row one.
	rs := []Result{{Profile: "a", Workload: WorkloadPack, Runs: []Run{run(100, 10, 1e9)}}}
	if got := baselineOf(rs); got != 10 {
		t.Errorf("baselineOf = %v, want 10", got)
	}
	if got := baselineOf(nil); got != 0 {
		t.Errorf("baselineOf(nil) = %v, want 0", got)
	}
}
