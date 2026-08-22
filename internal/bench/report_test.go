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
		m       Metric
		want    string
	}{
		{100, 120, chunkThroughput, "+20.0%"},
		{100, 80, chunkThroughput, "-20.0%"},
		{100, 101, chunkThroughput, "~"},
		{0, 50, chunkThroughput, "-"},
		// A lower-is-better metric flips the sign, so a positive number means
		// "better" in every column of the report rather than only in some.
		{100, 120, tickHealth, "-20.0%"},
		{100, 80, tickHealth, "+20.0%"},
		{100, 101, tickHealth, "~"},
	}
	for _, tt := range tests {
		if got := delta(tt.base, tt.v, tt.m); got != tt.want {
			t.Errorf("delta(%v,%v,%q) = %q, want %q", tt.base, tt.v, tt.m.Label, got, tt.want)
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
	if got := baselineOf(rs, chunkThroughput); got != 10 {
		t.Errorf("baselineOf = %v, want 10", got)
	}
	if got := baselineOf(nil, chunkThroughput); got != 0 {
		t.Errorf("baselineOf(nil) = %v, want 0", got)
	}
}

func TestRenderStatesTheModCount(t *testing.T) {
	// The pack is a skeleton and will grow. A row with no provenance invites a
	// comparison against a future pack that was never valid, so the count and
	// the caveat go in the output rather than in someone's memory.
	res := []Result{{
		Profile:  Baseline,
		Workload: WorkloadPack,
		Runs:     []Run{{Chunks: 100, Mods: 87, Elapsed: time.Second}},
	}}
	out := Render(res, "h", time.Unix(0, 0))
	if !strings.Contains(out, "87 mods loaded") {
		t.Errorf("report does not state the mod count:\n%s", out)
	}
	if !strings.Contains(out, "this one grows") {
		t.Errorf("report does not warn the pack grows:\n%s", out)
	}
}

func TestRenderOmitsModCountWhenUnknown(t *testing.T) {
	// An old log with no Fabric line should print no count rather than "0 mods".
	res := []Result{{
		Profile:  Baseline,
		Workload: WorkloadPack,
		Runs:     []Run{{Chunks: 100, Elapsed: time.Second}},
	}}
	if out := Render(res, "h", time.Unix(0, 0)); strings.Contains(out, "0 mods loaded") {
		t.Errorf("report invented a mod count:\n%s", out)
	}
}

func TestRenderDoesNotWarnAboutPackGrowthOnTheControl(t *testing.T) {
	// The control's mod count is Fabric API's nested jars plus the
	// pregenerator; it does not grow with the pack, so the caveat would be
	// wrong there. The first real sweep printed it under Workload A.
	res := []Result{{
		Profile:  Baseline,
		Workload: WorkloadVanilla,
		Runs:     []Run{{Chunks: 100, Mods: 43, Elapsed: time.Second}},
	}}
	out := Render(res, "h", time.Unix(0, 0))
	if !strings.Contains(out, "43 mods loaded") {
		t.Errorf("control should still state its count:\n%s", out)
	}
	if strings.Contains(out, "this one grows") {
		t.Errorf("the pack-growth caveat does not belong on the control:\n%s", out)
	}
}

func TestFilterSortsBaselineFirstThenAlphabetically(t *testing.T) {
	// The row everything else is measured against belongs at the top, and the
	// rest in a stable order so a diff of BENCHMARKS.md shows what moved rather
	// than what got shuffled.
	in := []Result{
		{Profile: "zzz", Workload: WorkloadPack},
		{Profile: "aaa", Workload: WorkloadPack},
		{Profile: Baseline, Workload: WorkloadPack},
		{Profile: "mmm", Workload: WorkloadVanilla},
	}
	got := filter(in, WorkloadPack)
	want := []string{Baseline, "aaa", "zzz"}
	if len(got) != len(want) {
		t.Fatalf("filter returned %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Profile != w {
			t.Errorf("row %d = %q, want %q", i, got[i].Profile, w)
		}
	}
}
