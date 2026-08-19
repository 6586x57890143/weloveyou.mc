package bench

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRenderJSONCarriesWhatThePageNeeds(t *testing.T) {
	res := []Result{
		{Profile: Baseline, Workload: WorkloadPack,
			Runs: []Run{{Chunks: 100, Mods: 87, Elapsed: time.Second, Startup: 3 * time.Second, PeakRSS: 1 << 30, GCPauses: []float64{5, 9}}}},
		{Profile: "j25-g1-coh", Workload: WorkloadPack, Dropped: []string{"-XX:Gone"},
			Runs: []Run{{Chunks: 200, Mods: 87, Elapsed: time.Second}}},
	}
	raw, err := RenderJSON(res, "weloveyou-bench", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("RenderJSON() = %v", err)
	}

	var doc struct {
		Host      string `json:"host"`
		Generated string `json:"generated"`
		Repeats   int    `json:"repeats_per_profile"`
		Workloads map[string]struct {
			Mods int `json:"mods_loaded"`
			Rows []struct {
				Profile      string   `json:"profile"`
				ChunksPerSec float64  `json:"chunks_per_sec"`
				VsBaseline   string   `json:"vs_baseline"`
				Dropped      []string `json:"dropped_flags"`
			} `json:"rows"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.Host != "weloveyou-bench" || doc.Repeats != 1 {
		t.Errorf("host/repeats wrong: %+v", doc)
	}
	pack, ok := doc.Workloads["pack"]
	if !ok {
		t.Fatalf("no pack workload in %s", raw)
	}
	// The mod count is provenance and must survive into the published page,
	// otherwise the site invites the comparison the report warns against.
	if pack.Mods != 87 {
		t.Errorf("mods_loaded = %d, want 87", pack.Mods)
	}
	if len(pack.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(pack.Rows))
	}
	// The baseline comparison is computed here, next to Render, rather than
	// left for whatever draws the page to reinvent.
	var coh = pack.Rows[1]
	if coh.Profile != "j25-g1-coh" || coh.ChunksPerSec != 200 {
		t.Errorf("row = %+v", coh)
	}
	if coh.VsBaseline == "" {
		t.Error("vs_baseline is empty; the page would have to recompute it")
	}
	if len(coh.Dropped) != 1 || coh.Dropped[0] != "-XX:Gone" {
		t.Errorf("dropped flags lost: %+v", coh.Dropped)
	}
}

func TestRenderJSONOmitsWorkloadsThatDidNotRun(t *testing.T) {
	res := []Result{{Profile: Baseline, Workload: WorkloadVanilla,
		Runs: []Run{{Chunks: 10, Elapsed: time.Second}}}}
	raw, err := RenderJSON(res, "h", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Workloads map[string]json.RawMessage `json:"workloads"`
	}
	_ = json.Unmarshal(raw, &doc)
	if _, ok := doc.Workloads["pack"]; ok {
		t.Error("a workload that never ran should not appear as an empty section")
	}
	if _, ok := doc.Workloads["vanilla"]; !ok {
		t.Error("the workload that did run is missing")
	}
}

func TestMaxRepeatsReportsWhatLandedNotWhatWasAsked(t *testing.T) {
	// Runs fail. The page should say how many measurements it actually has.
	res := []Result{
		{Profile: "a", Workload: WorkloadVanilla, Runs: []Run{{}, {}}},
		{Profile: "b", Workload: WorkloadVanilla, Runs: []Run{{}}},
	}
	if got := maxRepeats(res); got != 2 {
		t.Errorf("maxRepeats = %d, want 2", got)
	}
	if got := maxRepeats(nil); got != 0 {
		t.Errorf("maxRepeats(nil) = %d, want 0", got)
	}
}
