package bench

import (
	"encoding/json"
	"time"
)

// RenderJSON produces the machine-readable twin of BENCHMARKS.md.
//
// It exists because the two useful moments are separated in time: results only
// exist while the sweep is running, but publishing should happen after a human
// has read the numbers and merged them. Re-parsing our own Markdown at that
// point would be fragile in the one place fragility is least visible, so the
// sweep commits the data as well as the prose and the site renders from the data.
//
// The derived figures are written out rather than the raw runs, because the
// median-of-repeats decision belongs here next to Render, not duplicated in
// whatever renders the page.
func RenderJSON(results []Result, host string, when time.Time) ([]byte, error) {
	doc := jsonDoc{
		Host:          host,
		Generated:     when.UTC().Format(time.RFC3339),
		Repeats:       maxRepeats(results),
		NoiseFloorPct: NoiseFloor,
		Workloads:     map[string]jsonWorkload{},
	}
	for _, w := range []Workload{WorkloadVanilla, WorkloadPack} {
		rows := filter(results, w)
		if len(rows) == 0 {
			continue
		}
		base := baselineOf(rows)
		wl := jsonWorkload{Title: workloadTitle(w), Mods: modsOf(rows)}
		for _, r := range rows {
			cps := r.ChunksPerSec()
			wl.Rows = append(wl.Rows, jsonRow{
				Profile:      r.Profile,
				ChunksPerSec: cps,
				VsBaseline:   delta(base, cps),
				PeakRSSBytes: r.PeakRSS(),
				StartupSec:   r.Startup(),
				GCPauseP99Ms: r.GCPause(99),
				Repeats:      len(r.Runs),
				Dropped:      r.Dropped,
			})
		}
		doc.Workloads[string(w)] = wl
	}
	return json.MarshalIndent(doc, "", "  ")
}

type jsonDoc struct {
	Host          string                  `json:"host"`
	Generated     string                  `json:"generated"`
	Repeats       int                     `json:"repeats_per_profile"`
	NoiseFloorPct float64                 `json:"noise_floor"`
	Workloads     map[string]jsonWorkload `json:"workloads"`
}

type jsonWorkload struct {
	Title string    `json:"title"`
	Mods  int       `json:"mods_loaded"`
	Rows  []jsonRow `json:"rows"`
}

type jsonRow struct {
	Profile      string   `json:"profile"`
	ChunksPerSec float64  `json:"chunks_per_sec"`
	VsBaseline   string   `json:"vs_baseline"`
	PeakRSSBytes float64  `json:"peak_rss_bytes"`
	StartupSec   float64  `json:"startup_sec"`
	GCPauseP99Ms float64  `json:"gc_pause_p99_ms"`
	Repeats      int      `json:"repeats"`
	Dropped      []string `json:"dropped_flags,omitempty"`
}

// maxRepeats reports the largest repeat count any profile achieved. A run that
// failed leaves fewer, and the page says how many actually landed rather than
// how many were asked for.
func maxRepeats(results []Result) int {
	n := 0
	for _, r := range results {
		if len(r.Runs) > n {
			n = len(r.Runs)
		}
	}
	return n
}
