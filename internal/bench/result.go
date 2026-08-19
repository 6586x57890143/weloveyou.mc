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
	Mods     int // as reported by Fabric; provenance for the pack workload
	Elapsed  time.Duration
	Startup  time.Duration
	PeakRSS  float64 // bytes
	PeakCPU  float64 // percent, relative to one core: 200 means both cores busy
	MSPTMed  float64 // spark: median tick duration, ms
	MSPTP95  float64 // spark: 95th-percentile tick duration, ms
	TPS      float64 // spark: ticks per second, 20 is healthy
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
	Heap     string // the heap it ran with, so a row is self-describing
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
func (res Result) PeakCPU() float64 {
	return Median(res.collect(func(r Run) float64 { return r.PeakCPU }))
}

// MSPT reports tick duration across repeats. Throughput says how fast the
// server generated chunks; this says whether it stuttered doing it, which is
// the half a player actually notices.
func (res Result) MSPTMed() float64 {
	return Median(res.collect(func(r Run) float64 { return r.MSPTMed }))
}
func (res Result) MSPTP95() float64 {
	return Median(res.collect(func(r Run) float64 { return r.MSPTP95 }))
}
func (res Result) TPS() float64 {
	return Median(res.collect(func(r Run) float64 { return r.TPS }))
}
func (res Result) Startup() float64 {
	return Median(res.collect(func(r Run) float64 { return r.Startup.Seconds() }))
}

// Mods is the mod count the runs loaded. It is provenance, not a measurement:
// the pack is a skeleton today and will gain gameplay mods, so a pack-workload
// number is only comparable to another taken against a similar-sized pack.
// Recording it means a future reader can see that for themselves rather than
// comparing two rows that were never comparable.
func (res Result) Mods() int {
	for _, r := range res.Runs {
		if r.Mods > 0 {
			return r.Mods
		}
	}
	return 0
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
