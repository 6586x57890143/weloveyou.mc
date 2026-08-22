package bench

import (
	"io"
	"strings"
	"testing"
	"time"
)

// pipeDocker keeps its log stream OPEN after the last line, the way a real
// server does. That is the case the old `for lines.Scan()` loop could not
// survive: a quiet server printed nothing, Scan blocked, and neither the sample
// clock nor the stop condition could ever fire.
type pipeDocker struct {
	fakeDocker
	rc io.ReadCloser
	w  *io.PipeWriter
}

func newPipeDocker(preamble string) *pipeDocker {
	r, w := io.Pipe()
	d := &pipeDocker{w: w}
	d.rc = r
	go func() {
		// Written and then the pipe is simply left open, exactly like a server
		// that has booted and gone quiet.
		_, _ = io.WriteString(w, preamble)
	}()
	return d
}

func (d *pipeDocker) Logs(string) (io.ReadCloser, error) {
	if d.logsErr != nil {
		return nil, d.logsErr
	}
	return d.rc, nil
}

// tickTimeouts keep the wall clock out of it: the ticker fires immediately and
// the injected clock supplies the elapsed time.
func tickTimeouts() Timeouts {
	return Timeouts{Boot: time.Hour, Generate: 2 * time.Hour, Sample: time.Millisecond}
}

func TestExecuteStopsATickWorkloadOnItsOwnClock(t *testing.T) {
	// A tick workload has no completion line to wait for. Before this it would
	// have run until the sweep's own timeout killed it, on a box that bills by
	// the hour.
	p, cfg := testProfile()
	d := newPipeDocker("[Server thread/INFO]: Done (1.5s)! For help, type \"help\"\n")
	defer d.w.Close()

	run, err := Execute(d, p, cfg, WorkloadVillage, nil, Params{Load: 1},
		clock(10*time.Second), tickTimeouts())
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if run.Startup != 1500*time.Millisecond {
		t.Errorf("startup = %v, want the boot line to still be read", run.Startup)
	}
	// The drive script must have actually been sent, or the run measured an
	// empty world for six minutes and reported a plausible number.
	execs := strings.Join(d.execs, "\n")
	for _, want := range []string{"summon minecraft:villager", "gamerule doMobSpawning false"} {
		if !strings.Contains(execs, want) {
			t.Errorf("village was never driven, execs:\n%s", execs)
		}
	}
}

func TestExecuteDiscardsTheWarmup(t *testing.T) {
	// The first minutes are chunk loading, AlwaysPreTouch and JIT warmup.
	// Sampling them would fold startup into the median and report it as tick
	// health, which is the number this whole workload exists to produce.
	p, cfg := testProfile()
	d := newPipeDocker("[Server thread/INFO]: Done (1.5s)!\n")
	defer d.w.Close()

	// A clock that never advances: warmup can therefore never elapse, so no
	// sample may be taken at all. The run still has to terminate.
	frozen := func() time.Time { return time.Unix(0, 0) }
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Execute(d, p, cfg, WorkloadVillage, nil, Params{Load: 1},
			frozen, Timeouts{Boot: time.Hour, Generate: 0, Sample: time.Millisecond})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not return; the run is not bounded")
	}
	if d.statCalls != 0 {
		t.Errorf("sampled %d times during warmup, want none", d.statCalls)
	}
	if strings.Contains(strings.Join(d.execs, "\n"), "spark tps") {
		t.Error("spark was queried during warmup")
	}
}

func TestExecuteFailsWhenTheServerNeverBoots(t *testing.T) {
	// Timeouts.Boot was declared when this was written and then never read, so
	// a server that hung before reporting ready was bounded only by whether its
	// log happened to end.
	p, cfg := testProfile()
	d := newPipeDocker("[main/INFO]: Loading 87 mods\n")
	defer d.w.Close()

	_, err := Execute(d, p, cfg, WorkloadPack, nil, Params{Radius: 100},
		clock(time.Minute), Timeouts{Boot: 5 * time.Minute, Generate: time.Hour, Sample: time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "never reported ready within") {
		t.Fatalf("Execute() = %v, want a boot timeout", err)
	}
}

func TestExecuteCapsARunawayRun(t *testing.T) {
	// Timeouts.Generate was the other dead field. A pregeneration that stalls
	// after booting would otherwise hold the box until something else killed it.
	p, cfg := testProfile()
	d := newPipeDocker("[Server thread/INFO]: Done (1.5s)!\n")
	defer d.w.Close()

	_, err := Execute(d, p, cfg, WorkloadPack, nil, Params{Radius: 100},
		clock(time.Minute), Timeouts{Boot: time.Hour, Generate: 10 * time.Minute, Sample: time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "run exceeded") {
		t.Fatalf("Execute() = %v, want a run cap", err)
	}
}

func TestExecuteSaysWhichKindOfRunTheLogCutShort(t *testing.T) {
	// "log ended before pregeneration finished" is a lie about a workload that
	// was never pregenerating, and the next person debugs the wrong thing.
	p, cfg := testProfile()
	f := &fakeDocker{log: "[Server thread/INFO]: Done (1.0s)!\n"}
	_, err := Execute(f, p, cfg, WorkloadExplore, nil, Params{Radius: 500, Load: 1},
		clock(time.Second), tickTimeouts())
	if err == nil || !strings.Contains(err.Error(), "log ended before the run completed") {
		t.Fatalf("Execute() = %v, want the tick-workload wording", err)
	}
}

func TestExecuteStepsTheBotsOutward(t *testing.T) {
	// The explore workload is driven entirely by its Step script. If that never
	// runs, the bots stand at spawn and the workload measures an idle server.
	p, cfg := testProfile()
	d := newPipeDocker("[Server thread/INFO]: Done (1.0s)!\n")
	defer d.w.Close()

	if _, err := Execute(d, p, cfg, WorkloadExplore, nil, Params{Radius: 1000, Load: 1},
		clock(10*time.Second), tickTimeouts()); err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	var tps int
	for _, e := range d.execs {
		if strings.HasPrefix(e, "rcon-cli tp Bot") {
			tps++
		}
	}
	if tps < 2 {
		t.Errorf("issued %d teleports; the bots never travelled", tps)
	}
}

func TestWatchdogIsRecordedAndSurfaced(t *testing.T) {
	// Async parallelised entity ticking, its workers contended with the server
	// thread on two cores, and the watchdog force-killed production every few
	// minutes. Reported as a failure this looks like a flaky container;
	// reported as a missing row it looks like nothing happened.
	for _, line := range []string{
		"[Server Watchdog/FATAL]: A single server tick took 60.00 seconds",
		"[Server Watchdog/FATAL]: Considering it to be crashed, server will forcibly shutdown.",
	} {
		if !ParseWatchdog(line) {
			t.Errorf("ParseWatchdog(%q) = false", line)
		}
	}
	if ParseWatchdog("[Server thread/INFO]: Done (1.0s)!") {
		t.Error("an ordinary line was read as a watchdog kill")
	}

	s := NewScanner(clock(time.Second))
	s.Feed("[Server Watchdog/FATAL]: A single server tick took 60.00 seconds")
	if !s.Run().Watchdog {
		t.Fatal("the scanner did not record the watchdog kill")
	}

	res := []Result{{
		Profile: Baseline, Workload: WorkloadVillage,
		Runs: []Run{{MSPTP95: 90, Watchdog: true}},
	}}
	out := Render(res, "h", time.Unix(0, 0))
	if !strings.Contains(out, "watchdog killed the server") {
		t.Errorf("the watchdog kill is not in the report:\n%s", out)
	}
	if !watchdogged(res[0]) {
		t.Error("watchdogged() did not see it")
	}
	if watchdogged(Result{Runs: []Run{{}}}) {
		t.Error("watchdogged() reported a healthy run as killed")
	}
}

func TestTickWorkloadRowsDoNotClaimAChunkRate(t *testing.T) {
	// A tick workload generates no chunks. A 0.0 in that column reads as a
	// measurement rather than as an absence.
	res := []Result{{
		Profile: Baseline, Workload: WorkloadVillage,
		Runs: []Run{{MSPTP95: 12, TPS: 20}},
	}}
	out := Render(res, "h", time.Unix(0, 0))
	if !strings.Contains(out, "| - |") {
		t.Errorf("chunks/s should be blanked for a tick workload:\n%s", out)
	}
	// And the reader has to be told which way the column reads, or a win looks
	// like a loss.
	if !strings.Contains(out, "lower is better") {
		t.Errorf("the report does not say how to read MSPT p95:\n%s", out)
	}
	if !strings.Contains(Render([]Result{{
		Profile: Baseline, Workload: WorkloadPack,
		Runs: []Run{{Chunks: 10, Elapsed: time.Second}},
	}}, "h", time.Unix(0, 0)), "higher is better") {
		t.Error("the worldgen table does not say how to read chunks/s")
	}
	// The machines caveat belongs where the numbers are, not only in the docs.
	if !strings.Contains(Render([]Result{{
		Profile: Baseline, Workload: WorkloadMachines,
		Runs: []Run{{MSPTP95: 9}},
	}}, "h", time.Unix(0, 0)), "not production throughput") {
		t.Error("the machines table does not carry its caveat")
	}
}

func TestSelectTakesAListAndRejectsATypo(t *testing.T) {
	ps := []Profile{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got, err := Select(ps, "c, a")
	if err != nil || len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("Select(list) = (%v, %v), want a and c in matrix order", got, err)
	}
	// A typo must not quietly measure a smaller matrix and still produce a
	// plausible report.
	if _, err := Select(ps, "a,typo"); err == nil {
		t.Error("an unknown profile in a list should be rejected")
	}
	if _, err := Select(ps, " , "); err == nil {
		t.Error("a list naming nothing should be rejected")
	}
}

func TestOrderOfIsSafeOnAnUnknownWorkload(t *testing.T) {
	if got := orderOf(Workload("from-the-future")); got != len(AllWorkloads) {
		t.Errorf("orderOf(unknown) = %d, want it sorted last", got)
	}
}

func TestTheReportNamesTheBoxThatMeasured(t *testing.T) {
	// The first real sweep published `host: runnervm76f27`, which is the
	// ubuntu-latest merge runner. The numbers came off three Ampere boxes.
	rs := []Result{
		{Profile: Baseline, Host: "weloveyou-bench-2", Workload: WorkloadPack,
			Runs: []Run{{Chunks: 10, Elapsed: time.Second}}},
		{Profile: "other", Host: "weloveyou-bench", Workload: WorkloadPack,
			Runs: []Run{{Chunks: 10, Elapsed: time.Second}}},
	}
	out := Render(rs, "runnervm76f27", time.Unix(0, 0))
	if !strings.Contains(out, "weloveyou-bench, weloveyou-bench-2") {
		t.Errorf("the report does not name the measuring boxes:\n%s", out)
	}
	if strings.Contains(out, "runnervm76f27") {
		t.Errorf("the report still names the machine that rendered it:\n%s", out)
	}
	// Sorted and deduped, so two sweeps of the same boxes read the same.
	if got := Hosts(append(rs, rs[0])); len(got) != 2 || got[0] != "weloveyou-bench" {
		t.Errorf("Hosts() = %v", got)
	}
	// A shard written before Result.Host existed says so, rather than borrowing
	// whichever machine happened to run the merge.
	old := []Result{{Profile: Baseline, Workload: WorkloadPack, Runs: []Run{{Chunks: 1, Elapsed: time.Second}}}}
	if !strings.Contains(Render(old, "", time.Unix(0, 0)), "unrecorded") {
		t.Error("a hostless result should be reported as unrecorded")
	}
	if !strings.Contains(Render(old, "some-box", time.Unix(0, 0)), "some-box") {
		t.Error("the passed host is still the fallback when results carry none")
	}
}

func TestAProfileThatNeverRanIsNotAScore(t *testing.T) {
	// j25-shenandoah-gen failed every run in the first real sweep and rendered
	// as `0.0 chunks/s | -100.0%`, which reads as a measurement and is not one.
	rs := []Result{
		{Profile: Baseline, Workload: WorkloadPack, Runs: []Run{{Chunks: 100, Elapsed: 10 * time.Second}}},
		{Profile: "j25-shenandoah-gen", Heap: "6G", Workload: WorkloadPack},
	}
	if !rs[1].Failed() || rs[0].Failed() {
		t.Fatal("Failed() does not distinguish a profile with no runs")
	}
	out := Render(rs, "h", time.Unix(0, 0))
	if !strings.Contains(out, "| FAILED |") {
		t.Errorf("a profile with no runs should read FAILED:\n%s", out)
	}
	if strings.Contains(out, "-100.0%") {
		t.Errorf("a profile that never ran must not be given a delta:\n%s", out)
	}
	doc, err := RenderJSON(rs, "h", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"failed": true`) {
		t.Error("the JSON does not mark the failed row")
	}
}
