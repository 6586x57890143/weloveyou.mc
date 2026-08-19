package bench

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Chunky reports progress as it pregenerates. Parsing the chunk count and
// timing it ourselves is steadier than trusting a rate the mod prints, which
// is an instantaneous figure and swings with disk cache.
var chunkyProcessed = regexp.MustCompile(`Processed:?\s+([\d,]+)\s+chunks`)

// ParseChunkyChunks returns the chunk count on a Chunky progress line, and
// whether the line was one.
func ParseChunkyChunks(line string) (int, bool) {
	m := chunkyProcessed.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// -Xlog:gc lines end with the pause duration:
//
//	[12.345s][info][gc] GC(7) Pause Young (Normal) 512M->128M(6144M) 12.345ms
//
// Used in place of JFR: the pause distribution is the only thing the sweep
// needs from the GC, and a log line needs no extra tooling on the box or in
// the image.
var gcPause = regexp.MustCompile(`\bPause\b.*?([\d.]+)ms\s*$`)

// ParseGCPause returns a GC pause in milliseconds, and whether the line was one.
func ParseGCPause(line string) (float64, bool) {
	m := gcPause.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return 0, false
	}
	ms, err := strconv.ParseFloat(m[1], 64)
	if err != nil || ms < 0 {
		return 0, false
	}
	return ms, true
}

// docker stats prints memory as "1.234GiB / 11.6GiB".
var memUsage = regexp.MustCompile(`^\s*([\d.]+)\s*([KMG]i?B)`)

var unitBytes = map[string]float64{
	"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30,
}

// ParseMemUsage converts the first figure of a docker stats MemUsage column to
// bytes.
func ParseMemUsage(s string) (float64, bool) {
	m := memUsage.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	mult, ok := unitBytes[m[2]]
	if !ok {
		return 0, false
	}
	return v * mult, true
}

var cpuPerc = regexp.MustCompile(`([\d.]+)\s*%`)

// ParseCPUPerc converts a docker stats CPUPerc column to a percentage.
//
// Docker reports this relative to a single core, so a saturated two-core box
// reads ~200%. That is the number worth recording: it says whether a profile
// actually used the machine or left a core idle, which on a two-core box is the
// difference between a collector that helps and one that starves the tick loop.
func ParseCPUPerc(s string) (float64, bool) {
	m := cpuPerc.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// spark answers `/spark tps` on a worker thread rather than to the caller, so
// RCON returns nothing and the numbers arrive in the server log instead. That is
// convenient: the harness already reads the log, so no new channel is needed.
//
// The reply is two lines, a header then values:
//
//	[⚡] Tick durations (min/med/95%ile/max ms) from last 10s, 1m:
//	[⚡]  0.2/0.2/0.3/0.4;  0.2/0.2/0.3/4.6
//
// Matching the values line by shape rather than tracking the header keeps this
// stateless. The two groups are the 10s and 1m windows; the 1m one is taken,
// being the steadier of the two over a multi-minute pregeneration.
var sparkTicks = regexp.MustCompile(
	`([\d.]+)/([\d.]+)/([\d.]+)/([\d.]+);\s+([\d.]+)/([\d.]+)/([\d.]+)/([\d.]+)`)

// ParseSparkTicks returns median and 95th-percentile tick duration in
// milliseconds, from spark's 1-minute window.
//
// MSPT is the metric that corresponds to what a player feels. Throughput says
// how fast chunks were generated; this says whether the server stuttered doing it.
func ParseSparkTicks(line string) (median, p95 float64, ok bool) {
	m := sparkTicks.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	med, err1 := strconv.ParseFloat(m[6], 64)
	p, err2 := strconv.ParseFloat(m[7], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return med, p, true
}

// spark's TPS line: "20.0, 20.0, *20.0, *20.0, *20.0" for 5s/10s/1m/5m/15m. The
// asterisk marks a window shorter than its label, which is normal early in a run.
var sparkTPS = regexp.MustCompile(
	`\*?([\d.]+),\s*\*?([\d.]+),\s*\*?([\d.]+),\s*\*?([\d.]+),\s*\*?([\d.]+)`)

// ParseSparkTPS returns the 1-minute TPS figure.
func ParseSparkTPS(line string) (float64, bool) {
	m := sparkTPS.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[3], 64)
	if err != nil || v > 20.01 {
		// Above 20 is not a tick rate; it is some other comma-separated line
		// that happened to have five numbers in it.
		return 0, false
	}
	return v, true
}

// ServerReady matches the line a Minecraft server prints once it is accepting
// connections, and returns its startup duration.
// Fabric announces its mod count at startup. It is recorded because it is the
// only provenance marker the log gives for WHAT was benchmarked: the pack is a
// skeleton today and will grow, so a pack-workload number is only comparable to
// another taken against a similar pack. Without this, a future reader sees two
// rows and no reason to distrust the comparison.
var loadedMods = regexp.MustCompile(`Loading (\d+) mods`)

// ParseLoadedMods returns the mod count from a Fabric startup line.
func ParseLoadedMods(line string) (int, bool) {
	m := loadedMods.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

var doneLine = regexp.MustCompile(`Done \(([\d.]+)s\)`)

// ParseServerReady returns the startup duration and whether the line was the
// server announcing it is up.
func ParseServerReady(line string) (time.Duration, bool) {
	m := doneLine.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	secs, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}
