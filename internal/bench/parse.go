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

// ServerReady matches the line a Minecraft server prints once it is accepting
// connections, and returns its startup duration.
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
