package bench

import (
	"math"
	"sort"
)

// Median of a sample. Medians rather than means throughout: a benchmark on a
// cloud VM occasionally catches a noisy neighbour, and one bad run should not
// move the number.
func Median(xs []float64) float64 { return Percentile(xs, 50) }

// Percentile using linear interpolation between the closest ranks.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[len(s)-1]
	}
	pos := (p / 100) * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return s[lo]
	}
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

// NoiseFloor is the relative change below which two results are called equal.
//
// A shared cloud VM moves by a few percent on its own, so a smaller difference
// says more about the neighbours than about the flags. Chasing it produces
// confident nonsense.
const NoiseFloor = 0.05

// RelChange is the signed relative change from baseline to value. Positive
// means larger, which is better for throughput and worse for memory — the
// caller knows which way is up for its metric.
func RelChange(baseline, value float64) float64 {
	if baseline == 0 {
		return 0
	}
	return (value - baseline) / baseline
}

// Significant reports whether a change clears the noise floor.
func Significant(baseline, value float64) bool {
	return math.Abs(RelChange(baseline, value)) >= NoiseFloor
}
