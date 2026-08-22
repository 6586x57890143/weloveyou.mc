package bench

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseWorkloads(t *testing.T) {
	tests := []struct {
		in   string
		want []Workload
	}{
		{"vanilla", []Workload{WorkloadVanilla}},
		{"pack", []Workload{WorkloadPack}},
		// "both" is bench.yml's default and appears in every documented
		// invocation. It keeps working forever.
		{"both", []Workload{WorkloadVanilla, WorkloadPack}},
		{"", []Workload{WorkloadVanilla, WorkloadPack}},
		{"worldgen", []Workload{WorkloadVanilla, WorkloadPack}},
		{"players", []Workload{WorkloadExplore, WorkloadVillage, WorkloadMachines}},
		{"all", AllWorkloads},
		{"explore", []Workload{WorkloadExplore}},
		{"village,explore", []Workload{WorkloadExplore, WorkloadVillage}},
		{" Pack , VANILLA ", []Workload{WorkloadVanilla, WorkloadPack}},
		// A repeat must not measure the same thing twice on a box that bills by
		// the hour.
		{"pack,pack", []Workload{WorkloadPack}},
	}
	for _, tt := range tests {
		got, err := ParseWorkloads(tt.in)
		if err != nil {
			t.Errorf("ParseWorkloads(%q) = %v", tt.in, err)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("ParseWorkloads(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ParseWorkloads(%q) = %v, want %v", tt.in, got, tt.want)
				break
			}
		}
	}
}

func TestParseWorkloadsRejectsTheUnknown(t *testing.T) {
	for _, in := range []string{"nonsense", "pack,nonsense", ","} {
		_, err := ParseWorkloads(in)
		if err == nil {
			t.Fatalf("ParseWorkloads(%q) should have been rejected", in)
		}
		// The message has to list what is allowed. The workflow's choice list
		// once drifted from what this accepted and failed a sweep on its first
		// command, and a bare "unknown workload" would not have shortened that.
		for _, want := range []string{"explore", "players", "worldgen"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("ParseWorkloads(%q) error %q does not mention %q", in, err, want)
			}
		}
	}
}

func TestEveryWorkloadIsRenderableAndComplete(t *testing.T) {
	// AllWorkloads is what the report and the JSON iterate. A spec missing from
	// it renders nowhere; a workload in it with no spec renders an empty row.
	if len(AllWorkloads) != len(Specs) {
		t.Fatalf("AllWorkloads has %d entries, Specs has %d", len(AllWorkloads), len(Specs))
	}
	for _, w := range AllWorkloads {
		sp, ok := Specs[w]
		if !ok {
			t.Fatalf("%s is in AllWorkloads with no spec", w)
		}
		if sp.Title == "" || sp.Drive == nil || sp.Metric.Of == nil || sp.Metric.Label == "" {
			t.Errorf("%s spec is incomplete: %+v", w, sp)
		}
		// Every workload needs the tick instrument, or it cannot report the
		// numbers these workloads exist to produce.
		if !contains(strings.Join(sp.Mods, " "), SparkURL) {
			t.Errorf("%s does not load spark, so it has no MSPT", w)
		}
		// A tick workload must terminate on its own: there is no completion
		// line to wait for, and an unbounded run holds a box that bills hourly.
		if !sp.Worldgen() && sp.Steady <= 0 {
			t.Errorf("%s is not worldgen-shaped but never stops", w)
		}
	}
}

func TestTickWorkloadsAreReadByMSPTNotTPS(t *testing.T) {
	// TPS is capped at 20, so a load that is merely heavy reads 20 on every
	// profile and reproduces the exact blind spot these workloads remove.
	for _, w := range []Workload{WorkloadExplore, WorkloadVillage, WorkloadMachines} {
		sp := SpecFor(w)
		if sp.Metric.Label != "MSPT p95" || !sp.Metric.Lower {
			t.Errorf("%s is read by %q (lower=%v), want MSPT p95 lower-is-better",
				w, sp.Metric.Label, sp.Metric.Lower)
		}
		if sp.Warmup <= 0 {
			t.Errorf("%s has no warmup, so its median includes JIT and chunk loading", w)
		}
		if !sp.Pack {
			t.Errorf("%s must run against the real pack; tick cost is a pack property", w)
		}
		if !contains(strings.Join(sp.Mods, " "), CarpetURL) {
			t.Errorf("%s has no fake players, so nothing generates the load", w)
		}
	}
}

func TestWorldgenWorkloadsAreUnchanged(t *testing.T) {
	// These numbers are comparable to ones taken before the tick workloads
	// existed, and they stay that way only if nothing about them moved.
	for _, w := range []Workload{WorkloadVanilla, WorkloadPack} {
		sp := SpecFor(w)
		if !sp.Worldgen() || sp.Warmup != 0 || sp.Step != nil {
			t.Errorf("%s is no longer the worldgen shape: %+v", w, sp)
		}
		if sp.Metric.Label != "chunks/s" || sp.Metric.Lower {
			t.Errorf("%s is read by %q, want chunks/s higher-is-better", w, sp.Metric.Label)
		}
		got := sp.Drive(Params{Radius: 700})
		want := []string{"chunky radius 700", "chunky start"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			// Radius before start, or Chunky pregenerates whatever the previous
			// run left configured.
			t.Errorf("%s drive = %v, want %v", w, got, want)
		}
	}
}

func TestSpecForIsSafeOnAnUnknownWorkload(t *testing.T) {
	// A shard file written by an older build can name a workload this one has
	// never heard of. Rendering its rows beats panicking on them.
	sp := SpecFor(Workload("from-the-future"))
	if !sp.Worldgen() {
		t.Error("the zero spec should be worldgen-shaped")
	}
	if workloadTitle(Workload("from-the-future")) != "from-the-future" {
		t.Error("an unknown workload should fall back to printing its own name")
	}
}

func TestExploreDrivesEveryBotOutwardOnItsOwnBearing(t *testing.T) {
	p := Params{Radius: 1000, Load: 1}
	drive := strings.Join(exploreDrive(p), "\n")
	for _, want := range []string{
		"carpet commandPlayer true",
		"time set noon",
		"gamerule doDaylightCycle false",
		"player BotNorth spawn at 0 120 0",
		"player BotNorth sprint",
		"player BotNorth move forward",
		// Spawning around a moving player is a dominant real cost of exploring.
		// A player who does not trigger it is not the thing being simulated.
		"gamerule doMobSpawning true",
	} {
		if !contains(drive, want) {
			t.Errorf("explore drive is missing %q:\n%s", want, drive)
		}
	}

	// Every step must move every bot, or the workload is a bot standing still
	// and the chunk churn never happens.
	first := exploreStep(0, p)
	second := exploreStep(1, p)
	if len(first) != botCount(p.Load) || len(second) != len(first) {
		t.Fatalf("step moved %d and %d bots, want %d", len(first), len(second), botCount(p.Load))
	}
	if first[0] == second[0] {
		t.Errorf("consecutive steps put a bot in the same place: %q", first[0])
	}
	if !contains(first[0], "tp BotNorth 0 120 -100") {
		t.Errorf("first north step = %q, want a stride of radius/10", first[0])
	}

	// The patrol is bounded and it turns around. Running outward forever made
	// the world size a function of how long the workload ran, so two runs of
	// different lengths were not comparable, and it is the prime suspect for
	// the run that died partway through.
	var maxOut, turns int
	prev := 0
	for n := range 60 {
		cmd := exploreStep(n, p)[0]
		var name string
		var x, y, z int
		fmt.Sscanf(cmd, "tp %s %d %d %d", &name, &x, &y, &z)
		d := -z
		if d > maxOut {
			maxOut = d
		}
		if n > 0 && ((d < prev && turns%2 == 0) || (d > prev && turns%2 == 1)) {
			turns++
		}
		prev = d
	}
	if maxOut > p.Radius {
		t.Errorf("bots reached %d blocks, beyond the %d they were asked for", maxOut, p.Radius)
	}
	if turns < 2 {
		t.Errorf("the patrol turned around %d times in 60 steps; it is not a loop", turns)
	}
	// Below six chunks consecutive positions share most of their loaded chunks.
	if got := exploreStride(Params{Radius: 100}); got != 96 {
		t.Errorf("exploreStride floor = %d, want 96", got)
	}
}

func TestVillageSummonsItsOwnPopulationOnAFixedPlatform(t *testing.T) {
	drive := villageDrive(Params{Load: 1})
	joined := strings.Join(drive, "\n")
	// Natural spawning off, so the entity count is exactly what was summoned
	// and two repeats of the same profile are comparable.
	if !contains(joined, "gamerule doMobSpawning false") {
		t.Error("village must disable natural spawning or its entity count drifts")
	}
	// Terrain at spawn is whatever the seed produced; the platform is not.
	for _, want := range []string{"minecraft:stone", "forceload add", "minecraft:composter"} {
		if !contains(joined, want) {
			t.Errorf("village drive is missing %q", want)
		}
	}
	if n := strings.Count(joined, "summon minecraft:villager"); n != 40 {
		t.Errorf("summoned %d villagers at load 1, want 40", n)
	}
	// The load knob has to actually move the load, or calibration is theatre.
	if n := strings.Count(strings.Join(villageDrive(Params{Load: 2}), "\n"),
		"summon minecraft:villager"); n != 80 {
		t.Errorf("summoned %d villagers at load 2, want 80", n)
	}

	// Workstations have to keep up with the population. A fixed strip starved
	// the surplus, which then stopped pathing and went idle, so past a certain
	// load the knob stopped changing anything and quietly lied.
	for _, load := range []float64{1, 3, 5} {
		var depth int
		for _, cmd := range villageDrive(Params{Load: load}) {
			if strings.Contains(cmd, "minecraft:composter") {
				var a, b, c, d, e, f int
				fmt.Sscanf(cmd, "fill %d %d %d %d %d %d", &a, &b, &c, &d, &e, &f)
				depth = f - c + 1
			}
		}
		if posts := depth * (2*platformHalf + 1); posts < villagerCount(load) {
			t.Errorf("load %g: %d villagers but only %d workstations",
				load, villagerCount(load), posts)
		}
	}
}

func TestMachinesBuildsAPoweredArray(t *testing.T) {
	joined := strings.Join(machinesDrive(Params{Load: 1}), "\n")
	// Without the creative storage the machines are inert and the workload
	// measures an empty platform while still reporting a plausible number.
	for _, want := range []string{
		"oritech:creative_storage_block",
		"oritech:energy_pipe",
		"oritech:pulverizer_block",
	} {
		if !contains(joined, want) {
			t.Errorf("machines drive is missing %q:\n%s", want, joined)
		}
	}
	// Every machine row must sit next to a pipe row, or only the first row is
	// powered and the load stops scaling.
	if strings.Count(joined, "oritech:pulverizer_block") !=
		strings.Count(joined, "oritech:energy_pipe")-1 {
		t.Errorf("machine rows and pipe rows are not paired:\n%s", joined)
	}
	if machineRows(3) <= machineRows(1) {
		t.Error("load should add machine rows")
	}
	// The array has to stay on the platform it is built on.
	if got := machineRows(1000); got > platformHalf {
		t.Errorf("machineRows(1000) = %d, wider than the platform", got)
	}
}

func TestScaleAndBotCountStayInBounds(t *testing.T) {
	if got := scale(40, 0); got != 40 {
		t.Errorf("scale(40, 0) = %d, want the unscaled count", got)
	}
	if got := scale(40, -1); got != 40 {
		t.Errorf("scale(40, -1) = %d, want the unscaled count", got)
	}
	if got := scale(1, 0.01); got != 1 {
		t.Errorf("scale(1, 0.01) = %d, want at least one of a thing", got)
	}
	// More bots than bearings would put two on the same line and measure one.
	if got := botCount(100); got != len(bots) {
		t.Errorf("botCount(100) = %d, want %d", got, len(bots))
	}
	if got := botCount(1); got != 4 {
		t.Errorf("botCount(1) = %d, want 4", got)
	}
}

func TestEveryBearingIsDistinct(t *testing.T) {
	// Two bots on the same yaw explore the same chunks and the load is half
	// what the row claims.
	seen := map[int]bool{}
	for _, b := range bots {
		y := bearing(b.DX, b.DZ)
		if seen[y] {
			t.Errorf("bearing %d is used twice", y)
		}
		seen[y] = true
	}
}
