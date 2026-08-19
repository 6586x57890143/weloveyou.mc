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
	// Both high-water marks move independently: the run that used the most
	// memory is not necessarily the moment that used the most CPU.
	s := NewScanner(nil)
	for _, v := range [][2]float64{{1e9, 40}, {3e9, 190}, {2e9, 55}} {
		s.Observe(v[0], v[1])
	}
	run := s.Run()
	if run.PeakRSS != 3e9 {
		t.Errorf("PeakRSS = %v, want the high-water mark 3e9", run.PeakRSS)
	}
	if run.PeakCPU != 190 {
		t.Errorf("PeakCPU = %v, want the high-water mark 190", run.PeakCPU)
	}
}

func TestObserveIgnoresASampleThatDidNotParse(t *testing.T) {
	// A failed docker stats read arrives as zero, and zero must not clobber a
	// peak already seen.
	s := NewScanner(nil)
	s.Observe(3e9, 190)
	s.Observe(0, 0)
	if run := s.Run(); run.PeakRSS != 3e9 || run.PeakCPU != 190 {
		t.Errorf("a zero sample lowered the peaks: %+v", run)
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

func TestParseSparkTicks(t *testing.T) {
	// spark's real reply, as it appears in the server log.
	const line = "[15:42:58] [spark-worker-pool-1-thread-3/INFO]: [⚡]  0.2/0.2/0.3/0.4;  1.1/2.2/9.9/44.6"
	med, p95, ok := ParseSparkTicks(line)
	if !ok {
		t.Fatalf("ParseSparkTicks did not match %q", line)
	}
	// The 1-minute window, not the 10-second one: steadier over a pregeneration
	// that runs for minutes.
	if med != 2.2 || p95 != 9.9 {
		t.Errorf("got median %v p95 %v, want 2.2 and 9.9 from the 1m group", med, p95)
	}
	if _, _, ok := ParseSparkTicks("no tick durations here"); ok {
		t.Error("matched a line with no tick durations")
	}
}

func TestParseSparkTPS(t *testing.T) {
	tests := []struct {
		name, in string
		want     float64
		ok       bool
	}{
		{"healthy", "[⚡]  20.0, 20.0, *20.0, *20.0, *20.0", 20.0, true},
		{"struggling", "[⚡]  14.2, 15.0, 16.5, 18.0, 19.1", 16.5, true},
		// Five numbers that are not a tick rate: 20 is the ceiling, so anything
		// above it is some other line that happened to have the same shape.
		{"not a tps line", "[⚡]  512, 128, 6144, 40, 12", 0, false},
		{"unrelated", "Done (1.2s)!", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseSparkTPS(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ParseSparkTPS(%q) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestScannerRecordsTickHealth(t *testing.T) {
	s := NewScanner(nil)
	s.Feed("[⚡]  0.2/0.2/0.3/0.4;  1.1/2.2/9.9/44.6")
	s.Feed("[⚡]  20.0, 20.0, *19.4, *20.0, *20.0")
	run := s.Run()
	if run.MSPTMed != 2.2 || run.MSPTP95 != 9.9 {
		t.Errorf("tick durations not recorded: %+v", run)
	}
	if run.TPS != 19.4 {
		t.Errorf("TPS = %v, want the 1m figure 19.4", run.TPS)
	}
}

func TestResultTickAccessors(t *testing.T) {
	res := Result{Runs: []Run{
		{MSPTMed: 2.0, MSPTP95: 8.0, TPS: 20},
		{MSPTMed: 4.0, MSPTP95: 12.0, TPS: 18},
	}}
	if got := res.MSPTMed(); got != 3.0 {
		t.Errorf("MSPTMed() = %v, want the median of repeats 3.0", got)
	}
	if got := res.MSPTP95(); got != 10.0 {
		t.Errorf("MSPTP95() = %v, want 10.0", got)
	}
	if got := res.TPS(); got != 19.0 {
		t.Errorf("TPS() = %v, want 19.0", got)
	}
}

func TestParseSparkTicksRejectsAnUnparseableNumber(t *testing.T) {
	// The regex guarantees digits, not that they fit in a float64. A mangled
	// MSPT would be worse than none, because it still renders as a plausible ms.
	huge := strings.Repeat("9", 400)
	line := "[⚡]  0.2/0.2/0.3/0.4;  1.1/" + huge + "/9.9/44.6"
	if med, p95, ok := ParseSparkTicks(line); ok {
		t.Errorf("ParseSparkTicks overflowed to (%v, %v) instead of declining", med, p95)
	}
}
