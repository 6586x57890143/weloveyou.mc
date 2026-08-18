package bench

import "time"

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
	if ms, ok := ParseGCPause(line); ok {
		s.run.GCPauses = append(s.run.GCPauses, ms)
	}
	if n, ok := ParseChunkyChunks(line); ok {
		s.run.Chunks = n
		if !s.genStart.IsZero() {
			s.run.Elapsed = s.now().Sub(s.genStart)
		}
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

// Observe records a memory sample, keeping the high-water mark. The interesting
// figure is the peak during generation, not whatever it happens to be at the end.
func (s *Scanner) Observe(rssBytes float64) {
	if rssBytes > s.run.PeakRSS {
		s.run.PeakRSS = rssBytes
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

// ParseWorkloads turns the --workload flag into the list to run.
func ParseWorkloads(s string) ([]Workload, error) {
	switch s {
	case "vanilla":
		return []Workload{WorkloadVanilla}, nil
	case "pack":
		return []Workload{WorkloadPack}, nil
	case "both", "":
		return []Workload{WorkloadVanilla, WorkloadPack}, nil
	}
	return nil, &UnknownWorkloadError{Value: s}
}

// UnknownWorkloadError is returned for an unrecognised --workload.
type UnknownWorkloadError struct{ Value string }

func (e *UnknownWorkloadError) Error() string {
	return "unknown workload " + e.Value + "; want vanilla, pack or both"
}

// Select narrows the runnable profiles to one by name, or returns them all.
func Select(ps []Profile, only string) ([]Profile, error) {
	if only == "" {
		return ps, nil
	}
	for _, p := range ps {
		if p.Name == only {
			return []Profile{p}, nil
		}
	}
	return nil, &UnknownProfileError{Name: only}
}

// UnknownProfileError is returned when --only names nothing runnable.
type UnknownProfileError struct{ Name string }

func (e *UnknownProfileError) Error() string {
	return "no enabled profile named " + e.Name
}
