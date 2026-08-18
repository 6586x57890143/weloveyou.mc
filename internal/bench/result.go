package bench

import "time"

// Workload identifies what was measured. A profile that wins on vanilla
// worldgen and loses on the real pack is a finding, not noise, which is why
// both are recorded rather than averaged together.
type Workload string

const (
	// WorkloadVanilla is the control: stock worldgen, no content mods.
	WorkloadVanilla Workload = "vanilla"
	// WorkloadPack is the same run against the pack we actually ship.
	WorkloadPack Workload = "pack"
)

// Run is one execution of one profile.
type Run struct {
	Chunks   int
	Elapsed  time.Duration
	Startup  time.Duration
	PeakRSS  float64 // bytes
	GCPauses []float64
}

// ChunksPerSec is the throughput figure the worldgen workloads exist to produce.
func (r Run) ChunksPerSec() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Chunks) / r.Elapsed.Seconds()
}

// Result aggregates the repeats of one profile on one workload.
type Result struct {
	Profile  string
	Workload Workload
	Runs     []Run
	Dropped  []string // flags the JVM refused during preflight
}

func (res Result) collect(f func(Run) float64) []float64 {
	out := make([]float64, 0, len(res.Runs))
	for _, r := range res.Runs {
		out = append(out, f(r))
	}
	return out
}

func (res Result) ChunksPerSec() float64 { return Median(res.collect(Run.ChunksPerSec)) }
func (res Result) PeakRSS() float64 {
	return Median(res.collect(func(r Run) float64 { return r.PeakRSS }))
}
func (res Result) Startup() float64 {
	return Median(res.collect(func(r Run) float64 { return r.Startup.Seconds() }))
}

// GCPause returns a percentile across every pause of every repeat. Pooling is
// deliberate: pause distribution is a property of the configuration, and three
// short runs describe it better than three separate summaries would.
func (res Result) GCPause(p float64) float64 {
	var all []float64
	for _, r := range res.Runs {
		all = append(all, r.GCPauses...)
	}
	return Percentile(all, p)
}
