package bench

import (
	"strings"
	"testing"
	"time"
)

// clock returns a now() that advances by step on every call, so elapsed times
// are exact instead of racing the real clock.
func clock(step time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(step); return t }
}

const serverLog = `
[15:08:01] [main/INFO]: Loading 87 mods
[12.3s][info][gc] GC(1) Pause Young (Normal) 512M->128M(6144M) 8.500ms
[15:08:13] [Server thread/INFO]: Done (11.572s)! For help, type "help"
[20.0s][info][gc] GC(2) Pause Young (Normal) 600M->140M(6144M) 12.250ms
[Chunky] Task running for minecraft:overworld. Processed: 400 chunks (4.0%)
[25.0s][info][gc] GC(3) Concurrent Mark 40.000ms
[Chunky] Task running for minecraft:overworld. Processed: 1200 chunks (12.0%)
[Chunky] Task finished for minecraft:overworld
`

func feedAll(t *testing.T, s *Scanner, log string) int {
	t.Helper()
	readyAt := -1
	for i, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if s.Feed(line) {
			readyAt = i
			s.StartGeneration()
		}
	}
	return readyAt
}

func TestScannerReadsAWholeRun(t *testing.T) {
	s := NewScanner(clock(time.Second))
	readyAt := feedAll(t, s, serverLog)

	if readyAt < 0 || !s.Ready() {
		t.Fatal("the server never reported ready")
	}
	if !s.Finished() {
		t.Error("pregeneration completion was not detected")
	}
	run := s.Run()
	if run.Startup != 11572*time.Millisecond {
		t.Errorf("startup = %v, want 11.572s", run.Startup)
	}
	if run.Chunks != 1200 {
		t.Errorf("chunks = %d, want the latest count 1200", run.Chunks)
	}
	// Two pauses, and the concurrent phase must not be counted as one.
	if len(run.GCPauses) != 2 {
		t.Errorf("gc pauses = %v, want 2 (concurrent phases are not pauses)", run.GCPauses)
	}
	if run.Elapsed <= 0 {
		t.Error("elapsed time was never recorded")
	}
}

func TestScannerReportsReadyExactlyOnce(t *testing.T) {
	// A second "Done (" - from a restart inside the same log - must not reset
	// the clock and silently discard the measurement so far.
	s := NewScanner(clock(time.Second))
	n := 0
	for _, l := range []string{"Done (1.0s)!", "Done (2.0s)!"} {
		if s.Feed(l) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("ready fired %d times, want 1", n)
	}
	if got := s.Run().Startup; got != time.Second {
		t.Errorf("startup = %v, want the first reading", got)
	}
}

func TestScannerElapsedFallsBackToWallClock(t *testing.T) {
	// A run whose progress lines never carried a count still needs a duration,
	// or throughput would silently be zero.
	s := NewScanner(clock(2 * time.Second))
	s.Feed("Done (1.0s)!")
	s.StartGeneration()
	s.Feed("[Chunky] Task finished for minecraft:overworld")
	if got := s.Run().Elapsed; got <= 0 {
		t.Errorf("elapsed = %v, want a positive fallback", got)
	}
}

func TestScannerWithoutGenerationHasNoElapsed(t *testing.T) {
	s := NewScanner(clock(time.Second))
	s.Feed("Done (1.0s)!")
	if got := s.Run().Elapsed; got != 0 {
		t.Errorf("elapsed = %v, want 0 when generation never started", got)
	}
}

func TestObserveKeepsThePeak(t *testing.T) {
	s := NewScanner(nil)
	for _, v := range []float64{1e9, 3e9, 2e9} {
		s.Observe(v)
	}
	if got := s.Run().PeakRSS; got != 3e9 {
		t.Errorf("PeakRSS = %v, want the high-water mark 3e9", got)
	}
}

func TestNewScannerDefaultsItsClock(t *testing.T) {
	s := NewScanner(nil)
	s.StartGeneration()
	if s.Run().Elapsed < 0 {
		t.Error("a default clock should still produce a sane duration")
	}
}

func TestParseWorkloads(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{{"vanilla", 1}, {"pack", 1}, {"both", 2}, {"", 2}}
	for _, tt := range tests {
		got, err := ParseWorkloads(tt.in)
		if err != nil || len(got) != tt.want {
			t.Errorf("ParseWorkloads(%q) = (%v, %v), want %d workloads", tt.in, got, err, tt.want)
		}
	}
	if _, err := ParseWorkloads("nonsense"); err == nil {
		t.Error("an unknown workload should be rejected")
	} else if !strings.Contains(err.Error(), "vanilla, pack or both") {
		t.Errorf("error should say what is allowed, got %q", err)
	}
}

func TestSelect(t *testing.T) {
	ps := []Profile{{Name: "a"}, {Name: "b"}}
	if got, _ := Select(ps, ""); len(got) != 2 {
		t.Errorf("no filter should keep everything, got %v", got)
	}
	got, err := Select(ps, "b")
	if err != nil || len(got) != 1 || got[0].Name != "b" {
		t.Errorf("Select(b) = (%v, %v)", got, err)
	}
	if _, err := Select(ps, "zzz"); err == nil {
		t.Error("selecting a profile that does not exist should be an error")
	} else if !strings.Contains(err.Error(), "zzz") {
		t.Errorf("error should name the profile, got %q", err)
	}
}

func TestParseLoadedMods(t *testing.T) {
	tests := []struct {
		name, line string
		want       int
		ok         bool
	}{
		{"fabric startup", "[main/INFO]: Loading 87 mods:", 87, true},
		{"vanilla control", "[main/INFO]: Loading 1 mods:", 1, true},
		{"unrelated line", "[main/INFO]: Done (12.3s)! For help, type \"help\"", 0, false},
		{"no digits", "Loading mods", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseLoadedMods(tt.line)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ParseLoadedMods(%q) = %d, %v; want %d, %v", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestScannerRecordsModCount(t *testing.T) {
	// Provenance for the pack workload: the pack is a skeleton now and will
	// grow, so a row without a mod count invites a comparison that was never valid.
	s := NewScanner(nil)
	s.Feed("[main/INFO]: Loading 87 mods:")
	if got := s.Run().Mods; got != 87 {
		t.Errorf("Run().Mods = %d, want 87", got)
	}
}

func TestResultModsIgnoresRunsThatNeverLogged(t *testing.T) {
	res := Result{Runs: []Run{{Mods: 0}, {Mods: 87}}}
	if got := res.Mods(); got != 87 {
		t.Errorf("Mods() = %d, want 87", got)
	}
	if got := (Result{Runs: []Run{{}}}).Mods(); got != 0 {
		t.Errorf("Mods() with nothing logged = %d, want 0", got)
	}
}

func TestParseLoadedModsRejectsAnUnparseableCount(t *testing.T) {
	// The regex guarantees digits but not that they fit in an int, and a
	// mangled count would be worse than no count at all.
	if got, ok := ParseLoadedMods("Loading 99999999999999999999 mods"); ok {
		t.Errorf("ParseLoadedMods overflowed to %d instead of declining", got)
	}
}
