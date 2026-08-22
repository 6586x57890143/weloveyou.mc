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
// CDN (same jar name, different version hash), and the failure arrived as
// "server never reported ready", which names the symptom and not the cause.
const ChunkyURL = "https://cdn.modrinth.com/data/fALzjamp/versions/RVFHfo1D/Chunky-Fabric-1.4.23.jar"

// SparkURL is the tick instrument, and it is pinned deliberately.
//
// 1.10.109 (2024-09-26) is the newest build declaring 1.21.1, spark moved on to
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
// control: this is a library and the pregenerator, not content, but without it
// the server refuses to start with "requires any version of fabric, which is
// missing", which is the same class of bug deps-check.py exists to catch in the
// pack repo and which nothing here was checking.
const FabricAPIURL = "https://cdn.modrinth.com/data/P7dR8mSH/versions/Nlt8gI9z/fabric-api-0.116.15%2B1.21.1.jar"

// CarpetURL is the load instrument: it supplies the fake players the tick
// workloads drive.
//
// 1.4.147 is the only build declaring 1.21.1; carpet has moved on and is not
// coming back to it, so this is pinned the same way spark and Chunky are, and
// for the same reason - a stale pin surfaces as "server never reported ready",
// which names the symptom and not the cause.
//
// It is loaded through MODS= as an instrument and is deliberately NOT in the
// pack. A fake-player mod has no business shipping to players, and the pack
// repo's side-checking has enough to worry about.
//
// Carpet fake players are real server players: they hold chunk tickets, they are
// ticked and tracked, and mobs spawn around them. That is what makes them a
// simulation of load rather than a summoned mob standing in for one.
const CarpetURL = "https://cdn.modrinth.com/data/TQTTVgYE/versions/f2mvlGrg/fabric-carpet-1.21-1.4.147%2Bv240613.jar"

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
	// What a workload loads is a property of the workload, so it comes from the
	// spec table rather than from a branch here. spark is in every spec's Mods:
	// without it there is no tick measurement for the workloads that matter
	// most, and its overhead is constant across profiles, so comparisons stay
	// valid.
	sp := SpecFor(w)
	if sp.Pack {
		env = append(env, "PACKWIZ_URL="+PackURL)
	}
	if len(sp.Mods) > 0 {
		env = append(env, "MODS="+strings.Join(sp.Mods, ","))
	}
	return env
}

// Timeouts bound a single run. Exposed so tests need not wait on wall time.
//
// Boot and Generate were declared when this was written and then never read, so
// a server that hung after starting was bounded only by whether its log happened
// to end. They are wired now: a tick workload has no pregeneration line to wait
// for, so an unbounded run would simply hold the box until the sweep's own
// timeout killed it, and the box bills by the hour.
type Timeouts struct {
	// Boot is how long the server has to report ready.
	Boot time.Duration
	// Generate is the hard cap on a whole run, whatever it is doing.
	Generate time.Duration
	// Sample is the interval between resource samples and spark queries.
	Sample time.Duration
}

// DefaultTimeouts suit a 2-core box pregenerating a modest radius.
func DefaultTimeouts() Timeouts {
	return Timeouts{Boot: 15 * time.Minute, Generate: 45 * time.Minute, Sample: 10 * time.Second}
}

// Execute runs one profile once: start, wait for ready, pregenerate, measure,
// tear down. The container is always removed, including on failure - a bench
// box littered with dead containers from crashed sweeps fills its disk and
// then every later run fails for an unrelated reason.
func Execute(c Container, p Profile, cfg *Config, w Workload, xx []string, par Params,
	now func() time.Time, to Timeouts) (Run, error) {
	if now == nil {
		now = time.Now
	}
	if to.Sample <= 0 {
		to.Sample = time.Second
	}
	sp := SpecFor(w)
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

	// The log is read on its own goroutine and the run is driven by a select.
	//
	// It used to be `for lines.Scan()`, which works for Chunky because Chunky
	// prints continuously, and DEADLOCKS a tick workload: a quiet server prints
	// nothing, Scan blocks, and neither the sample clock nor the stop condition
	// can fire. The `spark tps` reply is itself the only thing producing lines,
	// and it cannot be issued from a loop that is blocked waiting for one.
	//
	// The goroutine only pushes text. Every Scanner call stays on this
	// goroutine, so nothing shared is mutated from two places.
	lines := make(chan string, 256)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		s := bufio.NewScanner(rc)
		s.Buffer(make([]byte, 1<<20), 1<<20)
		for s.Scan() {
			lines <- s.Text()
		}
		readErr <- s.Err()
	}()

	tick := time.NewTicker(to.Sample)
	defer tick.Stop()

	begin := now()
	// Zero value means the first sample fires as soon as warmup allows.
	var lastSample, driveAt time.Time
	steps := 0

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return sc.Run(), logEnded(sc, sp, readErr)
			}
			if sc.Feed(line) {
				// The drive script runs the moment the server is up. Timing
				// starts here rather than at the first progress line, so slow
				// starts are counted rather than hidden.
				for _, cmd := range sp.Drive(par) {
					_ = c.Exec(name, "rcon-cli", cmd)
				}
				sc.StartGeneration()
				driveAt = now()
			}
		case <-tick.C:
			// Nothing to do but re-check the clocks below. This case is the
			// whole reason a silent server still gets sampled and still stops.
		}

		if !sc.Ready() {
			if now().Sub(begin) >= to.Boot {
				return sc.Run(), fmt.Errorf("server never reported ready within %s", to.Boot)
			}
			continue
		}

		// Warmup is discarded rather than averaged in. The first minutes are
		// chunk loading, AlwaysPreTouch and JIT warmup; folding them into the
		// median would measure startup and report it as tick health. Worldgen
		// workloads have no warmup, so their numbers stay comparable to the
		// ones taken before this existed.
		warm := now().Sub(driveAt) >= sp.Warmup
		// Sample on a clock, not per log line. Both calls below are expensive
		// (`docker stats --no-stream` alone takes a second or two) and this
		// block used to run for EVERY line the server printed. Worse, each
		// `spark tps` reply is about ten more log lines, each of which
		// triggered another sample: a feedback loop that dragged a seven minute
		// run out to forty-five and reported ~1.0 chunks/s for every profile in
		// a sweep that then took seven hours.
		if warm && now().Sub(lastSample) >= to.Sample {
			lastSample = now()
			if sp.Step != nil {
				for _, cmd := range sp.Step(steps, par) {
					_ = c.Exec(name, "rcon-cli", cmd)
				}
				steps++
			}
			// spark answers into the log asynchronously, so the scanner picks
			// the reply up on a later line and the newest reading wins.
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
		// A tick workload has no completion line to wait for; it is done when it
		// has been under load long enough.
		if sp.Steady > 0 && now().Sub(driveAt) >= sp.Warmup+sp.Steady {
			return sc.Run(), nil
		}
		if now().Sub(begin) >= to.Generate {
			return sc.Run(), fmt.Errorf("run exceeded %s", to.Generate)
		}
	}
}

// logEnded says what an exhausted log stream means, which depends on how far the
// run got and on what shape of workload it was.
func logEnded(sc *Scanner, sp Spec, readErr <-chan error) error {
	select {
	case err := <-readErr:
		if err != nil {
			return fmt.Errorf("reading logs: %w", err)
		}
	default:
	}
	if !sc.Ready() {
		return fmt.Errorf("server never reported ready")
	}
	if sp.Steady > 0 {
		return fmt.Errorf("log ended before the run completed")
	}
	return fmt.Errorf("log ended before pregeneration finished")
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
