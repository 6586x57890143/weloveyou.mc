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
		Host:          hostLine(results, host),
		Generated:     when.UTC().Format(time.RFC3339),
		Repeats:       maxRepeats(results),
		NoiseFloorPct: NoiseFloor,
		Seed:          BenchSeed,
		Commit:        firstCommit(results),
		Pack:          firstPack(results),
		Radius:        firstRadius(results),
		Load:          firstLoad(results),
		Workloads:     map[string]jsonWorkload{},
	}
	for _, w := range AllWorkloads {
		rows := filter(results, w)
		if len(rows) == 0 {
			continue
		}
		sp := SpecFor(w)
		base := baselineOf(rows, sp.Metric)
		wl := jsonWorkload{
			Title: workloadTitle(w), Mods: modsOf(rows),
			Metric: sp.Metric.Label, Lower: sp.Metric.Lower, Worldgen: sp.Worldgen(),
		}
		for _, r := range rows {
			cps := r.ChunksPerSec()
			wl.Rows = append(wl.Rows, jsonRow{
				Primary:  sp.Metric.Of(r),
				Watchdog: watchdogged(r),
				// Failed says the profile produced no run at all. Without it
				// every number below is a zero and the site renders a
				// catastrophic score for something that never ran.
				Failed:       r.Failed(),
				Profile:      r.Profile,
				Heap:         r.Heap,
				Hardware:     r.Hardware,
				ChunksPerSec: cps,
				PeakCPUPct:   r.PeakCPU(),
				GCPauseP95Ms: r.GCPause(95),
				MSPTMedianMs: r.MSPTMed(),
				MSPTP95Ms:    r.MSPTP95(),
				TPS:          r.TPS(),
				VsBaseline:   delta(base, sp.Metric.Of(r), sp.Metric),
				PeakRSSBytes: r.PeakRSS(),
				StartupSec:   r.Startup(),
				GCPauseP99Ms: r.GCPause(99),
				Repeats:      len(r.Runs),
				Attempted:    r.Attempted,
				Image:        r.Image,
				Java:         r.Java,
				JVMArgs:      r.JVMArgs,
				Dropped:      r.Dropped,
			})
		}
		doc.Workloads[string(w)] = wl
		doc.Order = append(doc.Order, string(w))
	}
	return json.MarshalIndent(doc, "", "  ")
}

type jsonDoc struct {
	Host          string                  `json:"host"`
	Generated     string                  `json:"generated"`
	Repeats       int                     `json:"repeats_per_profile"`
	NoiseFloorPct float64                 `json:"noise_floor"`
	Commit        string                  `json:"commit,omitempty"`
	Pack          Pack                    `json:"pack,omitempty"`
	Seed          string                  `json:"seed"`
	Radius        int                     `json:"radius,omitempty"`
	Load          float64                 `json:"load,omitempty"`
	Workloads     map[string]jsonWorkload `json:"workloads"`
	// Order is the render order. A JSON object is unordered and Go marshals map
	// keys alphabetically, so without this the site listed workload E before
	// workload B and the control stopped being the first thing anyone read.
	Order []string `json:"workload_order"`
}

type jsonWorkload struct {
	Title string `json:"title"`
	Mods  int    `json:"mods_loaded"`
	// Metric is the column this workload is read by, and Lower which direction
	// is good. The site renders from this file, so the direction has to travel
	// with the data rather than being reimplemented in the page generator.
	Metric   string    `json:"primary_metric"`
	Lower    bool      `json:"primary_lower_is_better"`
	Worldgen bool      `json:"worldgen"`
	Rows     []jsonRow `json:"rows"`
}

type jsonRow struct {
	Profile string `json:"profile"`
	// Primary is the value of this workload's primary metric, duplicated out of
	// whichever column holds it so a consumer need not know which one that is.
	Primary  float64 `json:"primary_value"`
	Watchdog bool    `json:"watchdog,omitempty"`
	// Failed says the profile produced no run at all. Without it every number
	// below is a zero, and the site renders a catastrophic score for something
	// that never ran.
	Failed       bool     `json:"failed,omitempty"`
	Heap         string   `json:"heap,omitempty"`
	Hardware     Hardware `json:"hardware"`
	PeakCPUPct   float64  `json:"peak_cpu_percent"`
	GCPauseP95Ms float64  `json:"gc_pause_p95_ms"`
	MSPTMedianMs float64  `json:"mspt_median_ms"`
	MSPTP95Ms    float64  `json:"mspt_p95_ms"`
	TPS          float64  `json:"tps"`
	ChunksPerSec float64  `json:"chunks_per_sec"`
	VsBaseline   string   `json:"vs_baseline"`
	PeakRSSBytes float64  `json:"peak_rss_bytes"`
	StartupSec   float64  `json:"startup_sec"`
	GCPauseP99Ms float64  `json:"gc_pause_p99_ms"`
	// Repeats is how many runs FINISHED; Attempted how many were asked for.
	// Publishing only the first made a row where two of three crashed look
	// like a deliberate single run.
	Repeats   int      `json:"repeats"`
	Attempted int      `json:"attempted,omitempty"`
	Image     string   `json:"image,omitempty"`
	Java      string   `json:"java,omitempty"`
	JVMArgs   []string `json:"jvm_args,omitempty"`
	Dropped   []string `json:"dropped_flags,omitempty"`
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

// The settings a sweep ran with, read off the results rather than threaded
// through every renderer. Every shard of one sweep carries the same values.
func firstCommit(rs []Result) string {
	for _, r := range rs {
		if r.Commit != "" {
			return r.Commit
		}
	}
	return ""
}

func firstRadius(rs []Result) int {
	for _, r := range rs {
		if r.Radius > 0 {
			return r.Radius
		}
	}
	return 0
}

func firstLoad(rs []Result) float64 {
	for _, r := range rs {
		if r.Load > 0 {
			return r.Load
		}
	}
	return 0
}

// firstPack is the pack the sweep resolved. Every shard of one sweep resolves
// it once, so any non-empty one describes the whole sweep - and if they
// disagree, the validator is what catches it.
func firstPack(rs []Result) Pack {
	for _, r := range rs {
		if r.Pack.Known() {
			return r.Pack
		}
	}
	return Pack{}
}
