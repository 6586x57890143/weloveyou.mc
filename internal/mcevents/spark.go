package mcevents

import (
	"regexp"
	"strconv"
	"strings"
)

// spark's answer to `spark tps`, read off the log rather than the RCON reply.
//
// THE REPLY NEVER COMES BACK OVER RCON, and that is not a quirk to work around
// later, it is the whole reason this file exists. spark answers on its own
// worker thread, so the RCON call returns an empty string and the numbers appear
// in latest.log a moment later. Captured from the live server on 2026-08-25:
//
//	[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡] TPS from last 5s, 10s, 1m, 5m, 15m:
//	[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  20.0, 20.0, 20.0, *20.0, *20.0
//	[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]
//	[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡] Tick durations (min/med/95%ile/max ms) from last 10s, 1m:
//	[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  0.6/0.7/1.0/1.6;  0.6/0.8/1.0/2.8
//
// Each values line is matched on its own SHAPE rather than on the heading above
// it, so no state has to be carried between lines. A parser that remembered
// "the last heading said TPS" would mis-attribute every time a line was dropped,
// interleaved with another thread's output, or split across a log rotation, and
// the log bridge sees all three.
//
// The asterisk means spark has not been running long enough to fill that window
// and the figure is an estimate. Those are read as values anyway; what matters
// is that the '*' does not make the number unparseable.

var (
	// Five comma-separated figures, each optionally starred. Anchored on the
	// spark thread name so another mod printing five numbers cannot be mistaken
	// for tick health.
	reSparkTPS = regexp.MustCompile(
		`^\[[^\]]+\] \[spark[^\]]*\]: \[.\]\s+(\*?[\d.]+(?:,\s*\*?[\d.]+){4})\s*$`)

	// Two groups of min/med/95%ile/max, separated by a semicolon: the 10s
	// window then the 1m window.
	reSparkTick = regexp.MustCompile(
		`^\[[^\]]+\] \[spark[^\]]*\]: \[.\]\s+([\d.]+/[\d.]+/[\d.]+/[\d.]+);\s+([\d.]+/[\d.]+/[\d.]+/[\d.]+)\s*$`)
)

// Tick is what spark reported. Only one of the two halves arrives per line, so
// the flags say which figures are real.
type Tick struct {
	// TPS over the last 5s, 10s, 1m, 5m and 15m.
	TPS    []float64
	HasTPS bool

	// MSPT95 is the 95th percentile tick duration over the last minute, in
	// milliseconds. p95 rather than TPS is what the tick workloads are read by:
	// TPS is capped at 20, so a server that is merely struggling still reports
	// 20 while p95 has already moved.
	MSPT95  float64
	HasMSPT bool
}

// ParseSpark reads one line of spark output, if that is what it is.
func ParseSpark(line string) (Tick, bool) {
	line = strings.TrimRight(line, "\r\n")

	if m := reSparkTPS.FindStringSubmatch(line); m != nil {
		var t Tick
		for _, f := range strings.Split(m[1], ",") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(
				strings.TrimSpace(f), "*")), 64)
			if err != nil {
				return Tick{}, false
			}
			t.TPS = append(t.TPS, v)
		}
		t.HasTPS = true
		return t, true
	}

	if m := reSparkTick.FindStringSubmatch(line); m != nil {
		// The one-minute group, because the board refreshes every minute and a
		// ten-second window would make it jump around for no reason a reader
		// could act on.
		parts := strings.Split(m[2], "/")
		v, err := strconv.ParseFloat(parts[2], 64) // min/med/95%ile/max
		if err != nil {
			return Tick{}, false
		}
		return Tick{MSPT95: v, HasMSPT: true}, true
	}

	return Tick{}, false
}

// TPS1m is the one-minute figure, which is the one worth showing: the 5s window
// swings on a single chunk load and the 15m one hides an outage that started
// two minutes ago.
func (t Tick) TPS1m() (float64, bool) {
	if !t.HasTPS || len(t.TPS) < 3 {
		return 0, false
	}
	return t.TPS[2], true
}
