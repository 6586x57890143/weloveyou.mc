package bench

import (
	"strings"
	"testing"
	"time"
)

// Real lscpu output from the A1 bench box, and from an A2 before conversion.
// The pair is the point: two OCPUs on A2 is four threads, on A1 it is two.
const (
	lscpuA1 = `Architecture:                            aarch64
CPU op-mode(s):                          32-bit, 64-bit
CPU(s):                                  2
Thread(s) per core:                      1
Core(s) per socket:                      2
Model name:                              Neoverse-N1
BogoMIPS:                                50.00`

	lscpuA2 = `Architecture:                            aarch64
CPU(s):                                  4
Thread(s) per core:                      2
Core(s) per socket:                      2
Model name:                              AmpereOne`
)

func TestParseLscpu(t *testing.T) {
	h := ParseLscpu(lscpuA1)
	if h.CPUs != 2 || h.ThreadsPerCore != 1 {
		t.Errorf("A1 parsed as %d cpus / %d threads per core", h.CPUs, h.ThreadsPerCore)
	}
	if h.Model != "Neoverse-N1" || h.Arch != "aarch64" {
		t.Errorf("A1 model/arch = %q / %q", h.Model, h.Arch)
	}
}

func TestHardwareTellsA1FromA2(t *testing.T) {
	// The failure this exists to prevent: a box that never converted from A2 to
	// A1 measured four threads instead of two, and its numbers went into the
	// same table as the real ones.
	a1, a2 := ParseLscpu(lscpuA1), ParseLscpu(lscpuA2)
	if a1.Comparable(a2) {
		t.Error("A1 and A2 must not be treated as the same machine")
	}
	if !a1.Comparable(ParseLscpu(lscpuA1)) {
		t.Error("identical hardware should compare equal")
	}
}

func TestParseMemTotal(t *testing.T) {
	if got := ParseMemTotal("MemTotal:       12216208 kB\nMemFree: 100 kB"); got != 11929 {
		t.Errorf("ParseMemTotal = %d MB, want 11929", got)
	}
	if got := ParseMemTotal("nothing useful"); got != 0 {
		t.Errorf("ParseMemTotal(garbage) = %d, want 0", got)
	}
	if got := ParseMemTotal("MemTotal:       99999999999999999999 kB"); got != 0 {
		t.Errorf("an unparseable size should be 0, got %d", got)
	}
}

func TestHardwareString(t *testing.T) {
	h := Hardware{Model: "Neoverse-N1", CPUs: 2, ThreadsPerCore: 1, MemoryMB: 11929, Arch: "aarch64"}
	got := h.String()
	for _, want := range []string{"Neoverse-N1", "2 vCPU", "12 GB", "aarch64"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
	// Threads per core only earns its space when it is not 1.
	if strings.Contains(got, "threads/core") {
		t.Errorf("String() = %q, should not mention threads/core when it is 1", got)
	}
	smt := Hardware{Model: "AmpereOne", CPUs: 4, ThreadsPerCore: 2}
	if !strings.Contains(smt.String(), "2 threads/core") {
		t.Errorf("SMT should be visible: %q", smt.String())
	}
	if got := (Hardware{}).String(); got != "unknown" {
		t.Errorf("empty hardware = %q, want unknown", got)
	}
}

func TestHardwareSpreadFindsMixedTables(t *testing.T) {
	a1, a2 := ParseLscpu(lscpuA1), ParseLscpu(lscpuA2)
	same := []Result{{Hardware: a1}, {Hardware: a1}}
	if n := len(HardwareSpread(same)); n != 1 {
		t.Errorf("one machine reported as %d", n)
	}
	mixed := []Result{{Hardware: a1}, {Hardware: a2}}
	if n := len(HardwareSpread(mixed)); n != 2 {
		t.Errorf("a sharded sweep across two machines reported as %d", n)
	}
	// Results with no snapshot at all are skipped rather than counted as a
	// distinct machine, so old data does not look like a mixed table.
	if n := len(HardwareSpread([]Result{{}, {Hardware: a1}})); n != 1 {
		t.Errorf("unknown hardware should not count as its own machine, got %d", n)
	}
}

func TestRenderWarnsWhenHardwareIsMixed(t *testing.T) {
	a1, a2 := ParseLscpu(lscpuA1), ParseLscpu(lscpuA2)
	one := Render([]Result{{Profile: Baseline, Workload: WorkloadPack, Hardware: a1,
		Runs: []Run{{Chunks: 1, Elapsed: 1}}}}, "h", zeroTime())
	if !strings.Contains(one, "measured on: Neoverse-N1") {
		t.Errorf("single machine should be named:\n%s", one)
	}
	mixed := Render([]Result{
		{Profile: Baseline, Workload: WorkloadPack, Hardware: a1, Runs: []Run{{Chunks: 1, Elapsed: 1}}},
		{Profile: "other", Workload: WorkloadPack, Hardware: a2, Runs: []Run{{Chunks: 1, Elapsed: 1}}},
	}, "h", zeroTime())
	if !strings.Contains(mixed, "more than one machine") {
		t.Errorf("a mixed table must say so where the numbers are:\n%s", mixed)
	}
}

func zeroTime() time.Time { return time.Unix(0, 0) }
