package bench

import (
	"strings"
	"testing"
	"time"
)

// good is a small sweep with nothing wrong with it.
func good() []Result {
	pk := Pack{Version: "0.1.6", IndexHash: "aaaa", Minecraft: "1.21.1", Fabric: "0.19.3"}
	hw := Hardware{Model: "Neoverse-N1", CPUs: 2, ThreadsPerCore: 1}
	run := Run{Chunks: 100, Elapsed: 10 * time.Second, Mods: 143, MSPTP95: 8, TPS: 20}
	return []Result{
		{Profile: Baseline, Workload: WorkloadPack, Pack: pk, Hardware: hw,
			Attempted: 1, Runs: []Run{run}},
		{Profile: "other", Workload: WorkloadPack, Pack: pk, Hardware: hw,
			Attempted: 1, Runs: []Run{run}},
	}
}

func TestValidateAcceptsAHealthySweep(t *testing.T) {
	if bad := Validate(good()); len(bad) != 0 {
		t.Errorf("a healthy sweep should validate, got %v", bad)
	}
}

func TestValidateBlocksWhatMakesTheWholeSweepMisleading(t *testing.T) {
	tests := []struct {
		name string
		fix  func([]Result) []Result
		want string
	}{
		{
			// One row out of seventy-five succeeding used to be enough.
			"nothing measured",
			func(rs []Result) []Result {
				for i := range rs {
					rs[i].Runs = nil
				}
				return rs
			},
			"no run produced a measurement",
		},
		{
			// Every `vs base` on the page is then computed against zero.
			"the baseline failed",
			func(rs []Result) []Result { rs[0].Runs = nil; return rs },
			"computed against zero",
		},
		{
			// The channel was republished mid-sweep: early and late rows
			// measured different content.
			"the pack changed mid-sweep",
			func(rs []Result) []Result { rs[1].Pack.IndexHash = "bbbb"; return rs },
			"republished mid-sweep",
		},
		{
			"mixed hardware",
			func(rs []Result) []Result {
				rs[1].Hardware = Hardware{Model: "AmpereOne", CPUs: 4, ThreadsPerCore: 2}
				return rs
			},
			"different machines",
		},
		{
			// Above the ceiling means the parser matched the wrong line.
			"impossible TPS",
			func(rs []Result) []Result { rs[1].Runs[0].TPS = 41.3; return rs },
			"above the ceiling",
		},
		{
			"negative duration",
			func(rs []Result) []Result { rs[1].Runs[0].MSPTP95 = -1; return rs },
			"negative tick duration",
		},
		{
			// It reported numbers, but not for the pack it claims to measure.
			"the pack did not install",
			func(rs []Result) []Result { rs[1].Runs[0].Mods = 0; return rs },
			"the pack did not install",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := Validate(tt.fix(good()))
			if len(bad) == 0 {
				t.Fatalf("%s should have been rejected", tt.name)
			}
			if !strings.Contains(strings.Join(bad, "\n"), tt.want) {
				t.Errorf("problems were %v, want one mentioning %q", bad, tt.want)
			}
		})
	}
}

func TestValidateStillPublishesAFailedProfile(t *testing.T) {
	// A profile that crashed is a finding, renders as FAILED, and must reach
	// the page. Only a failed BASELINE is different in kind. This is the line
	// the whole gate turns on, so it gets its own test.
	rs := good()
	rs = append(rs, Result{Profile: "j25-shenandoah-gen", Workload: WorkloadPack,
		Pack: rs[0].Pack, Hardware: rs[0].Hardware, Attempted: 3})
	if bad := Validate(rs); len(bad) != 0 {
		t.Errorf("one failed profile must not block a sweep, got %v", bad)
	}
}

func TestValidateRejectsNothingAtAll(t *testing.T) {
	if bad := Validate(nil); len(bad) == 0 {
		t.Error("an empty sweep is not publishable")
	}
}

func TestValidateIgnoresAFailedBaselineWhenItIsTheOnlyRow(t *testing.T) {
	// A single-profile calibration run has nothing to compare against, so a
	// missing baseline comparison is not a problem with the sweep.
	rs := []Result{{Profile: Baseline, Workload: WorkloadExplore,
		Pack: Pack{IndexHash: "a"}, Attempted: 1,
		Runs: []Run{{MSPTP95: 65.2, TPS: 19.97, Mods: 143}}}}
	if bad := Validate(rs); len(bad) != 0 {
		t.Errorf("a one-row sweep should validate, got %v", bad)
	}
}

func TestValidateCatchesTheRemainingImpossibilities(t *testing.T) {
	// A negative duration or chunk count means a parser matched the wrong line,
	// which makes every figure derived from that run suspect.
	for _, tc := range []struct {
		name string
		fix  func(*Run)
		want string
	}{
		{"negative elapsed", func(r *Run) { r.Elapsed = -time.Second }, "negative elapsed"},
		{"negative chunks", func(r *Run) { r.Chunks = -5 }, "negative chunk count"},
		{"negative median", func(r *Run) { r.MSPTMed = -0.5 }, "negative tick duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := good()
			tc.fix(&rs[1].Runs[0])
			bad := Validate(rs)
			if !strings.Contains(strings.Join(bad, "\n"), tc.want) {
				t.Errorf("problems were %v, want one mentioning %q", bad, tc.want)
			}
		})
	}
}

func TestValidateDoesNotDemandModsOfTheControl(t *testing.T) {
	// The vanilla control loads no pack, so a zero mod count there is normal
	// rather than proof the pack failed to install.
	rs := []Result{{Profile: Baseline, Workload: WorkloadVanilla,
		Hardware: Hardware{Model: "Neoverse-N1", CPUs: 2}, Attempted: 1,
		Runs: []Run{{Chunks: 10, Elapsed: time.Second, TPS: 20}}}}
	if bad := Validate(rs); len(bad) != 0 {
		t.Errorf("the control should validate without mods, got %v", bad)
	}
}
