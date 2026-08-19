package bench

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
)

// Container is the slice of Docker the sweep needs. An interface so the run
// logic - which is where the mistakes live - can be tested against a replayed
// log instead of a real server.
type Container interface {
	Start(name, image string, env []string) error
	Logs(name string) (io.ReadCloser, error)
	Exec(name string, args ...string) error
	// Stats returns one docker stats line carrying CPU and memory together, so
	// a sample is one call rather than two racing ones.
	Stats(name string) (string, error)
	Remove(name string) error
}

// BenchSeed is fixed so every profile generates the same terrain. Comparing
// runs over different worlds would measure the seed, not the flags.
const BenchSeed = "weloveyou-bench"

// PackURL is the published stable channel, used by the pack workload.
const PackURL = "https://weloveyou-pack.pages.dev/pack/stable/pack.toml"

// ChunkyURL is the pregenerator and the sweep's throughput instrument.
//
// Both URLs are the exact ones pack/stable ships, and must stay in step with it:
// the pack repo's CI verifies every download URL resolves, which is the only
// thing standing between a rotted pin and a sweep that measures nothing. The
// first live run died on a stale Chunky version that had been replaced on the
// CDN — same jar name, different version hash — and the failure arrived as
// "server never reported ready", which names the symptom and not the cause.
const ChunkyURL = "https://cdn.modrinth.com/data/fALzjamp/versions/RVFHfo1D/Chunky-Fabric-1.4.23.jar"

// SparkURL is the tick instrument, and it is pinned deliberately.
//
// 1.10.109 (2024-09-26) is the newest build declaring 1.21.1 — spark moved on to
// >=26.1 and is not coming back. jvm-profiles.toml recorded spark as unusable
// because its bundled async-profiler segfaulted the JVM on Java 25/aarch64 in
// restart loops. That was true and the cause was findable: spark runs a
// BACKGROUND profiler on servers by default, which engages async-profiler at
// boot. Disabling it (see SparkOpts) leaves `/spark tps` working, because tick
// timing comes from spark's own hook and never touches the native agent.
// Verified booting on the real aarch64 bench box: Done (1.670s), 45 mods.
//
// If flame graphs are ever wanted here, the bundled async-profiler is 2.9 from
// 2022; current spark ships 4.5, and the aarch64 .so exports every JNI symbol
// 2.9 did. Swapping just that file is a possible follow-up, not a need.
const SparkURL = "https://cdn.modrinth.com/data/l6YH9Als/versions/cALUj9l1/spark-1.10.109-fabric.jar"

// SparkOpts keeps spark from starting async-profiler at boot. Without this the
// JVM dies with SIGSEGV in VMThread::nativeThreadId on Java 25/aarch64.
var SparkOpts = []string{"-Dspark.backgroundProfiler=false"}

// FabricAPIURL is Chunky's hard dependency. The vanilla control is still the
// control: this is a library and the pregenerator, not content — but without it
// the server refuses to start with "requires any version of fabric, which is
// missing", which is the same class of bug deps-check.py exists to catch in the
// pack repo and which nothing here was checking.
const FabricAPIURL = "https://cdn.modrinth.com/data/P7dR8mSH/versions/Nlt8gI9z/fabric-api-0.116.15%2B1.21.1.jar"

// Env builds the container environment for one profile and workload.
func Env(p Profile, cfg *Config, w Workload, xx []string) []string {
	env := []string{
		"EULA=TRUE", "TYPE=FABRIC", "VERSION=1.21.1", "MEMORY=" + p.Heap(),
		"LEVEL=" + BenchSeed, "SEED=" + BenchSeed,
		"ONLINE_MODE=false", "SERVER_PORT=25598",
		// -Xlog:gc rather than JFR: the pause distribution is all the sweep
		// needs from the collector, and a log line needs no recording
		// lifecycle, no dump step and no jfr tool in the image.
		"JVM_OPTS=" + strings.TrimSpace("-Xlog:gc "+
			strings.Join(append(append([]string{}, SparkOpts...), cfg.CompatFor(p)...), " ")),
		"JVM_XX_OPTS=" + strings.Join(xx, " "),
		fmt.Sprintf("USE_AIKAR_FLAGS=%t", p.Aikar),
	}
	if w == WorkloadPack {
		// spark rides alongside the pack too: without it there is no tick
		// measurement for the workload that matters most. Its overhead is
		// constant across profiles, so comparisons stay valid.
		return append(env, "PACKWIZ_URL="+PackURL, "MODS="+SparkURL)
	}
	return append(env, "MODS="+ChunkyURL+","+FabricAPIURL+","+SparkURL)
}

// Timeouts bound a single run. Exposed so tests need not wait on wall time.
type Timeouts struct {
	Boot, Generate, Sample time.Duration
}

// DefaultTimeouts suit a 2-core box pregenerating a modest radius.
func DefaultTimeouts() Timeouts {
	return Timeouts{Boot: 15 * time.Minute, Generate: 45 * time.Minute, Sample: 10 * time.Second}
}

// Execute runs one profile once: start, wait for ready, pregenerate, measure,
// tear down. The container is always removed, including on failure - a bench
// box littered with dead containers from crashed sweeps fills its disk and
// then every later run fails for an unrelated reason.
func Execute(c Container, p Profile, cfg *Config, w Workload, xx []string, radius int,
	now func() time.Time, to Timeouts) (Run, error) {
	if now == nil {
		now = time.Now
	}
	name := fmt.Sprintf("bench-%s-%s", p.Name, w)
	_ = c.Remove(name)
	defer func() { _ = c.Remove(name) }()

	if err := c.Start(name, p.Image, Env(p, cfg, w, xx)); err != nil {
		return Run{}, fmt.Errorf("starting %s: %w", name, err)
	}
	rc, err := c.Logs(name)
	if err != nil {
		return Run{}, fmt.Errorf("following logs: %w", err)
	}
	defer rc.Close()

	sc := NewScanner(now)
	lines := bufio.NewScanner(rc)
	lines.Buffer(make([]byte, 1<<20), 1<<20)

	for lines.Scan() {
		justReady := sc.Feed(lines.Text())
		if justReady {
			// Chunky needs telling how far and then to begin. Timing starts
			// here rather than at the first progress line, so slow starts are
			// counted rather than hidden.
			_ = c.Exec(name, "rcon-cli", fmt.Sprintf("chunky radius %d", radius))
			sc.StartGeneration()
			_ = c.Exec(name, "rcon-cli", "chunky start")
		}
		if sc.Ready() {
			// Ask spark for tick numbers on the same cadence as the resource
			// sample. It replies into the log asynchronously, so the scanner
			// picks it up on a later line and the newest reading wins.
			_ = c.Exec(name, "rcon-cli", "spark tps")
			if line, err := c.Stats(name); err == nil {
				rss, _ := ParseMemUsage(statsField(line, 1))
				cpu, _ := ParseCPUPerc(statsField(line, 0))
				sc.Observe(rss, cpu)
			}
		}
		if sc.Finished() {
			return sc.Run(), nil
		}
	}
	if err := lines.Err(); err != nil {
		return sc.Run(), fmt.Errorf("reading logs: %w", err)
	}
	if !sc.Ready() {
		return sc.Run(), fmt.Errorf("server never reported ready")
	}
	return sc.Run(), fmt.Errorf("log ended before pregeneration finished")
}

// statsField splits one docker stats line on tabs. Tab rather than a space or a
// comma because the MemUsage column contains both ("6.5GiB / 12GiB"), so any
// separator that also appears inside a field would need quoting to survive.
func statsField(line string, i int) string {
	parts := strings.Split(line, "\t")
	if i >= len(parts) {
		return ""
	}
	return parts[i]
}
