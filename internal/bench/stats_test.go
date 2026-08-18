package bench

import (
	"math"
	"testing"
)

func TestPercentile(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	tests := []struct {
		p, want float64
	}{
		{0, 1}, {50, 5.5}, {100, 10},
		{-5, 1},   // clamped
		{150, 10}, // clamped
	}
	for _, tt := range tests {
		if got := Percentile(xs, tt.p); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("Percentile(p=%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
}

func TestPercentileEdgeCases(t *testing.T) {
	if got := Percentile(nil, 50); got != 0 {
		t.Errorf("Percentile(nil) = %v, want 0", got)
	}
	if got := Percentile([]float64{42}, 99); got != 42 {
		t.Errorf("Percentile of one sample = %v, want 42", got)
	}
}

func TestPercentileDoesNotMutateInput(t *testing.T) {
	// The caller's slice is shared across metrics; sorting it in place would
	// silently corrupt whatever reads it next.
	xs := []float64{3, 1, 2}
	Percentile(xs, 50)
	if xs[0] != 3 || xs[1] != 1 || xs[2] != 2 {
		t.Fatalf("input was reordered: %v", xs)
	}
}

func TestMedianResistsAnOutlier(t *testing.T) {
	// The reason medians are used at all: one noisy neighbour on a shared VM
	// must not move the reported number.
	clean := Median([]float64{100, 101, 99})
	noisy := Median([]float64{100, 101, 12})
	if math.Abs(clean-noisy) > 1.5 {
		t.Errorf("an outlier moved the median from %v to %v", clean, noisy)
	}
}

func TestRelChangeAndSignificance(t *testing.T) {
	tests := []struct {
		name            string
		base, val       float64
		wantSignificant bool
	}{
		{"well above the floor", 100, 110, true},
		{"well below it", 100, 102, false},
		{"exactly at the floor counts", 100, 105, true},
		{"negative change of the same size counts", 100, 95, true},
		{"no baseline is never significant", 0, 50, false},
		{"identical", 100, 100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Significant(tt.base, tt.val); got != tt.wantSignificant {
				t.Errorf("Significant(%v, %v) = %v, want %v", tt.base, tt.val, got, tt.wantSignificant)
			}
		})
	}
	if got := RelChange(0, 10); got != 0 {
		t.Errorf("RelChange with no baseline = %v, want 0", got)
	}
	if got := RelChange(100, 110); math.Abs(got-0.1) > 1e-9 {
		t.Errorf("RelChange(100,110) = %v, want 0.1", got)
	}
}

func TestRunChunksPerSec(t *testing.T) {
	if got := (Run{Chunks: 1000, Elapsed: 100 * 1e9}).ChunksPerSec(); math.Abs(got-10) > 1e-9 {
		t.Errorf("ChunksPerSec = %v, want 10", got)
	}
	// A run that recorded no time must not divide by zero.
	if got := (Run{Chunks: 1000}).ChunksPerSec(); got != 0 {
		t.Errorf("ChunksPerSec with no elapsed time = %v, want 0", got)
	}
}

func TestResultAggregatesMedians(t *testing.T) {
	sec := float64(1e9)
	res := Result{
		Profile:  "p",
		Workload: WorkloadPack,
		Runs: []Run{
			{Chunks: 100, Elapsed: 10 * 1e9, PeakRSS: 1e9, Startup: 5 * 1e9, GCPauses: []float64{1, 2}},
			{Chunks: 110, Elapsed: 10 * 1e9, PeakRSS: 2e9, Startup: 7 * 1e9, GCPauses: []float64{3, 100}},
			{Chunks: 120, Elapsed: 10 * 1e9, PeakRSS: 3e9, Startup: 6 * 1e9, GCPauses: []float64{4}},
		},
	}
	if got := res.ChunksPerSec(); math.Abs(got-11) > 1e-9 {
		t.Errorf("ChunksPerSec = %v, want 11", got)
	}
	if got := res.PeakRSS(); got != 2e9 {
		t.Errorf("PeakRSS = %v, want 2e9", got)
	}
	if got := res.Startup(); math.Abs(got-6) > 1e-9 {
		t.Errorf("Startup = %v, want 6", got)
	}
	// Pauses pool across repeats, so the tail is described by every run.
	if got := res.GCPause(100); got != 100 {
		t.Errorf("GCPause(100) = %v, want 100", got)
	}
	_ = sec
}

func TestResultWithNoRuns(t *testing.T) {
	var res Result
	if res.ChunksPerSec() != 0 || res.PeakRSS() != 0 || res.Startup() != 0 || res.GCPause(99) != 0 {
		t.Error("an empty result should report zeroes rather than panic")
	}
}
