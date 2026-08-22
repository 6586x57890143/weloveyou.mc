package bench

import (
	"strconv"
	"strings"
	"time"
)

// Scanner turns a container's log into a Run.
//
// It lives here rather than in the command because reading a log is parsing,
// not I/O: the caller owns the pipe, this owns the meaning. That split is what
// makes the interesting half testable against a fixture instead of a container.
type Scanner struct {
	run      Run
	ready    bool
	finished bool
	genStart time.Time
	now      func() time.Time
}

// NewScanner returns a Scanner. now is injected so elapsed time is deterministic
// under test.
func NewScanner(now func() time.Time) *Scanner {
	if now == nil {
		now = time.Now
	}
	return &Scanner{now: now}
}

// Feed consumes one log line. It reports whether the server has just become
// ready, which is the caller's cue to start the pregeneration.
func (s *Scanner) Feed(line string) (justReady bool) {
	if d, ok := ParseServerReady(line); ok && !s.ready {
		s.run.Startup = d
		s.ready = true
		return true
	}
	if n, ok := ParseLoadedMods(line); ok {
		s.run.Mods = n
	}
	if med, p95, ok := ParseSparkTicks(line); ok {
		s.run.MSPTMed, s.run.MSPTP95 = med, p95
	}
	if tps, ok := ParseSparkTPS(line); ok {
		s.run.TPS = tps
	}
	if ms, ok := ParseGCPause(line); ok {
		s.run.GCPauses = append(s.run.GCPauses, ms)
	}
	if n, ok := ParseChunkyChunks(line); ok {
		s.run.Chunks = n
		if !s.genStart.IsZero() {
			s.run.Elapsed = s.now().Sub(s.genStart)
		}
	}
	if ParseWatchdog(line) {
		s.run.Watchdog = true
	}
	if isPregenDone(line) {
		s.finished = true
	}
	return false
}

// StartGeneration marks the moment pregeneration was asked for, so throughput
// is timed from the request rather than from the first progress line.
func (s *Scanner) StartGeneration() { s.genStart = s.now() }

// Ready reports whether the server has finished starting.
func (s *Scanner) Ready() bool { return s.ready }

// Finished reports whether pregeneration has completed.
func (s *Scanner) Finished() bool { return s.finished }

// Observe records a resource sample, keeping the high-water marks. The
// interesting figures are the peaks during generation, not whatever they happen
// to be at the end.
func (s *Scanner) Observe(rssBytes, cpuPercent float64) {
	if rssBytes > s.run.PeakRSS {
		s.run.PeakRSS = rssBytes
	}
	if cpuPercent > s.run.PeakCPU {
		s.run.PeakCPU = cpuPercent
	}
}

// Run returns what was measured, filling in elapsed time if no progress line
// carried it.
func (s *Scanner) Run() Run {
	out := s.run
	if out.Elapsed == 0 && !s.genStart.IsZero() {
		out.Elapsed = s.now().Sub(s.genStart)
	}
	return out
}

func isPregenDone(line string) bool {
	for _, m := range []string{"Task finished for", "Chunky task finished"} {
		if len(line) >= len(m) && contains(line, m) {
			return true
		}
	}
	return false
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

// Select narrows the runnable profiles to those named, or returns them all.
//
// It takes a comma-separated list rather than a single name because the
// confirmation pass runs the workloads that cost money against the handful of
// profiles screening liked, and naming them one invocation at a time meant one
// bench-box boot per profile.
func Select(ps []Profile, only string) ([]Profile, error) {
	only = strings.TrimSpace(only)
	if only == "" {
		return ps, nil
	}
	want := map[string]bool{}
	for _, n := range strings.Split(only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var out []Profile
	for _, p := range ps {
		if want[p.Name] {
			out = append(out, p)
			delete(want, p.Name)
		}
	}
	// A typo must not silently measure a smaller matrix and still produce a
	// plausible report, which is the rule resolveFlagsets already holds for an
	// unknown flagset.
	for n := range want {
		return nil, &UnknownProfileError{Name: n}
	}
	if len(out) == 0 {
		return nil, &UnknownProfileError{Name: only}
	}
	return out, nil
}

// UnknownProfileError is returned when --only names nothing runnable.
type UnknownProfileError struct{ Name string }

func (e *UnknownProfileError) Error() string {
	return "no enabled profile named " + e.Name
}

// Shard narrows profiles to the slice this runner should measure.
//
// One box takes about seven minutes per run, and 21 profiles across two
// workloads is most of a night. That is too long to be useful: a sweep that
// only fits overnight can only be run overnight, and the last one was killed
// before it finished. Splitting the list across several boxes turns wall time
// into a spend decision, which is the right trade while credits expire unused.
//
// Round-robin rather than contiguous blocks, so the slow profiles (the pack
// workload, the larger heaps) spread evenly instead of landing on one unlucky
// shard and leaving the others idle.
//
// spec is "i/n", one-based: shard 1 of 3 takes profiles 0, 3, 6 and so on.
func Shard(ps []Profile, spec string) ([]Profile, error) {
	if spec == "" {
		return ps, nil
	}
	i, n, err := parseShard(spec)
	if err != nil {
		return nil, err
	}
	var out []Profile
	for k, p := range ps {
		if k%n == i-1 {
			out = append(out, p)
		}
	}
	return out, nil
}

func parseShard(spec string) (i, n int, err error) {
	parts := strings.Split(spec, "/")
	if len(parts) != 2 {
		return 0, 0, &BadShardError{Spec: spec}
	}
	i, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	n, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || n < 1 || i < 1 || i > n {
		return 0, 0, &BadShardError{Spec: spec}
	}
	return i, n, nil
}

// BadShardError is returned for a --shard value that is not a usable "i/n".
type BadShardError struct{ Spec string }

func (e *BadShardError) Error() string {
	return "bad shard " + e.Spec + `; want "i/n" with 1 <= i <= n, for example 2/3`
}
