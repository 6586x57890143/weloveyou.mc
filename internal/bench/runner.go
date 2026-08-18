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
	MemUsage(name string) (string, error)
	Remove(name string) error
}

// BenchSeed is fixed so every profile generates the same terrain. Comparing
// runs over different worlds would measure the seed, not the flags.
const BenchSeed = "weloveyou-bench"

// PackURL is the published stable channel, used by the pack workload.
const PackURL = "https://weloveyou-pack.pages.dev/pack/stable/pack.toml"

// ChunkyURL is the pregenerator, and the only mod the vanilla control loads.
const ChunkyURL = "https://cdn.modrinth.com/data/fALzjamp/versions/UgPo4rtq/Chunky-Fabric-1.4.23.jar"

// Env builds the container environment for one profile and workload.
func Env(p Profile, cfg *Config, w Workload, xx []string) []string {
	env := []string{
		"EULA=TRUE", "TYPE=FABRIC", "VERSION=1.21.1", "MEMORY=6G",
		"LEVEL=" + BenchSeed, "SEED=" + BenchSeed,
		"ONLINE_MODE=false", "SERVER_PORT=25598",
		// -Xlog:gc rather than JFR: the pause distribution is all the sweep
		// needs from the collector, and a log line needs no recording
		// lifecycle, no dump step and no jfr tool in the image.
		"JVM_OPTS=" + strings.TrimSpace("-Xlog:gc "+strings.Join(cfg.CompatFor(p), " ")),
		"JVM_XX_OPTS=" + strings.Join(xx, " "),
		fmt.Sprintf("USE_AIKAR_FLAGS=%t", p.Aikar),
	}
	if w == WorkloadPack {
		return append(env, "PACKWIZ_URL="+PackURL)
	}
	return append(env, "MODS="+ChunkyURL)
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
			if s, err := c.MemUsage(name); err == nil {
				if v, ok := ParseMemUsage(s); ok {
					sc.Observe(v)
				}
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
