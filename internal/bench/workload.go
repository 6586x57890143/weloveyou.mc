package bench

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Workload identifies what was measured. A profile that wins on vanilla
// worldgen and loses on the real pack is a finding, not noise, which is why each
// is recorded rather than averaged together.
type Workload string

const (
	// WorkloadVanilla is the control: stock worldgen, no content mods.
	WorkloadVanilla Workload = "vanilla"
	// WorkloadPack is the same run against the pack we actually ship.
	WorkloadPack Workload = "pack"
	// WorkloadExplore is players travelling: chunk load churn and the mob
	// spawning that follows a moving player around.
	WorkloadExplore Workload = "explore"
	// WorkloadVillage is entity AI: villagers pathing to workstations.
	WorkloadVillage Workload = "village"
	// WorkloadMachines is the Oritech tick load: block entities and an energy
	// network.
	WorkloadMachines Workload = "machines"
)

// AllWorkloads is the render order. Report, JSON and the site iterate this
// rather than each carrying its own copy of the list, which is how the two
// renderers would otherwise disagree about which workloads exist.
var AllWorkloads = []Workload{
	WorkloadVanilla, WorkloadPack,
	WorkloadExplore, WorkloadVillage, WorkloadMachines,
}

// Params are the knobs a drive script is written against.
type Params struct {
	// Radius in blocks. The worldgen workloads pregenerate it; explore travels it.
	Radius int
	// Load scales entity and machine counts. The calibration knob: a workload
	// whose load cannot move MSPT is not measuring anything, and how much load
	// this box needs is a property of the box rather than something to hardcode.
	Load float64
}

// Metric is the number a workload is read by.
//
// Worldgen is a throughput question and reads high-is-good; a tick workload is a
// latency question and reads low-is-good. The direction travels with the
// workload rather than being assumed by whatever is rendering, because a report
// that signs a comparison backwards is worse than one that omits it.
type Metric struct {
	Label string
	Of    func(Result) float64
	Lower bool
}

// Spec is what makes a workload a workload: what to load, what to type once the
// server is up, what to type on each sample tick, when to stop, and which number
// the table is read by.
//
// A table rather than branches inside Execute because five workloads across
// three renderers is five places to forget one. Adding a workload is a row.
type Spec struct {
	Title string
	// Pack loads the published pack rather than the bare instruments.
	Pack bool
	// Mods are the instrument jars, appended to MODS=.
	Mods []string
	// Drive is issued once, in order, the moment the server reports ready.
	Drive func(Params) []string
	// Step is issued on each sample tick, n counting from zero. Nil for
	// workloads that need no further prodding.
	Step func(n int, p Params) []string
	// Warmup discards everything before it: the first minute is chunk loading,
	// AlwaysPreTouch and JIT warmup, and folding that into the median would
	// measure startup and call it tick health.
	Warmup time.Duration
	// Steady ends the run this long after Warmup. Zero means the run ends when
	// the log says pregeneration finished, which is the worldgen shape.
	Steady time.Duration
	Metric Metric
}

// Worldgen reports whether this workload produces a chunk throughput figure.
// The tick workloads do not, and a 0.0 in that column would read as a
// measurement rather than as an absence.
func (s Spec) Worldgen() bool { return s.Steady == 0 }

var chunkThroughput = Metric{Label: "chunks/s", Of: Result.ChunksPerSec}

// tickHealth is MSPT p95 rather than TPS deliberately. TPS is capped at 20, so a
// load that is merely heavy reads 20 on every profile and reproduces the exact
// blind spot these workloads exist to remove. p95 tick duration is unbounded and
// moves long before TPS does.
var tickHealth = Metric{Label: "MSPT p95", Of: Result.MSPTP95, Lower: true}

// Specs is the table. Adding a workload is a row here plus an alias below;
// nothing else in the package needs to learn about it.
var Specs = map[Workload]Spec{
	WorkloadVanilla: {
		Title:  "Workload A - vanilla worldgen (control)",
		Mods:   []string{ChunkyURL, FabricAPIURL, SparkURL},
		Drive:  chunkyDrive,
		Metric: chunkThroughput,
	},
	WorkloadPack: {
		Title:  "Workload B - the pack we actually ship",
		Pack:   true,
		Mods:   []string{SparkURL},
		Drive:  chunkyDrive,
		Metric: chunkThroughput,
	},
	WorkloadExplore: {
		Title:  "Workload C - players exploring",
		Pack:   true,
		Mods:   []string{SparkURL, CarpetURL},
		Drive:  exploreDrive,
		Step:   exploreStep,
		Warmup: 2 * time.Minute,
		Steady: 6 * time.Minute,
		Metric: tickHealth,
	},
	WorkloadVillage: {
		Title:  "Workload D - villagers and entity AI",
		Pack:   true,
		Mods:   []string{SparkURL, CarpetURL},
		Drive:  villageDrive,
		Warmup: 2 * time.Minute,
		Steady: 6 * time.Minute,
		Metric: tickHealth,
	},
	WorkloadMachines: {
		Title:  "Workload E - Oritech machines under power",
		Pack:   true,
		Mods:   []string{SparkURL, CarpetURL},
		Drive:  machinesDrive,
		Warmup: 2 * time.Minute,
		Steady: 6 * time.Minute,
		Metric: tickHealth,
	},
}

// SpecFor returns a workload's spec. An unknown workload yields the zero Spec,
// which is worldgen-shaped and drives nothing: a renderer handed a workload from
// an older shard file prints a row instead of panicking.
func SpecFor(w Workload) Spec { return Specs[w] }

// preamble pins the world state every tick workload runs against.
//
// Weather and time of day change what a tick costs. Left alone they would land
// as variance between repeats of the same profile, which is indistinguishable
// from a flag doing something.
var preamble = []string{
	"carpet commandPlayer true",
	"time set noon",
	"gamerule doDaylightCycle false",
	"gamerule doWeatherCycle false",
	"gamerule doFireTick false",
	"difficulty normal",
}

func chunkyDrive(p Params) []string {
	// Radius before start, or Chunky pregenerates whatever the previous run
	// left configured.
	return []string{fmt.Sprintf("chunky radius %d", p.Radius), "chunky start"}
}

// bots are the fake players, one per bearing. Carpet fake players are real
// server players: they hold chunk tickets, they are ticked and tracked, and mobs
// spawn around them. That is the reason for the mod rather than a mob summon
// standing in for a player.
var bots = []struct {
	Name   string
	DX, DZ int
}{
	{"BotNorth", 0, -1},
	{"BotEast", 1, 0},
	{"BotSouth", 0, 1},
	{"BotWest", -1, 0},
	{"BotNorthEast", 1, -1},
	{"BotSouthWest", -1, 1},
}

// scale applies the load multiplier, never returning less than one of a thing.
func scale(base int, load float64) int {
	if load <= 0 {
		load = 1
	}
	n := int(float64(base)*load + 0.5)
	if n < 1 {
		return 1
	}
	return n
}

// botCount is how many fake players this load asks for, capped by the number of
// bearings there are to send them along.
func botCount(load float64) int {
	n := scale(4, load)
	if n > len(bots) {
		return len(bots)
	}
	return n
}

// exploreStride is how far each bot moves per sample tick, derived from the
// radius so --radius still tunes this workload the way it tunes the worldgen
// ones. Six chunks is the floor: below that, consecutive positions share most of
// their loaded chunks, which is a bot standing still with extra steps.
func exploreStride(p Params) int {
	if s := p.Radius / 10; s > 96 {
		return s
	}
	return 96
}

// exploreDrive spawns the bots above spawn and sets them running.
//
// They are teleported rather than pathfound (see exploreStep), but they are left
// sprinting so movement, collision and entity tracking still tick between hops.
func exploreDrive(p Params) []string {
	out := append([]string{}, preamble...)
	// Mob spawning stays ON here, unlike the other tick workloads. Spawning
	// around a moving player is a dominant real cost of exploring, and a player
	// who does not trigger it is not the thing being simulated. Do not "fix"
	// this to reduce variance.
	out = append(out, "gamerule doMobSpawning true")
	for _, b := range bots[:botCount(p.Load)] {
		out = append(out,
			fmt.Sprintf("player %s spawn at 0 120 0 facing %d 0", b.Name, bearing(b.DX, b.DZ)),
			fmt.Sprintf("player %s sprint", b.Name),
			fmt.Sprintf("player %s move forward", b.Name))
	}
	return out
}

// exploreStep moves every bot one stride further out along its own bearing.
//
// Teleporting rather than walking is deliberate. A walking bot gets stuck on
// terrain, drowns, or falls into a ravine, which would make the load depend on
// where the fixed seed happened to put one - the same class of mistake BenchSeed
// exists to prevent. Teleporting is terrain-independent and repeatable, and it
// is the honest simulation anyway: elytra and boat travel is how a player
// actually generates chunk load churn.
func exploreStep(n int, p Params) []string {
	d := exploreStride(p) * (n + 1)
	var out []string
	for _, b := range bots[:botCount(p.Load)] {
		out = append(out, fmt.Sprintf("tp %s %d 120 %d", b.Name, b.DX*d, b.DZ*d))
	}
	return out
}

// bearing turns a direction into the yaw Minecraft wants, where 0 is south and
// yaw increases clockwise.
func bearing(dx, dz int) int {
	switch {
	case dx == 0 && dz < 0:
		return 180
	case dx > 0 && dz == 0:
		return -90
	case dx == 0 && dz > 0:
		return 0
	case dx < 0 && dz == 0:
		return 90
	case dx > 0 && dz < 0:
		return -135
	default:
		return 45
	}
}

// The tick workloads build on a platform in the air rather than on the ground.
//
// Terrain at spawn is whatever the seed produced; a slab of stone at a fixed
// height is the same on every profile and every repeat. The workload is meant to
// measure the flags, not the hill it landed on.
const (
	platformY    = 118
	platformHalf = 20
)

// platform is the stone slab everything else is placed on, plus the forceload
// that keeps it ticking whether or not a player is standing in it.
func platform() []string {
	return []string{
		fmt.Sprintf("forceload add %d %d %d %d",
			-platformHalf-16, -platformHalf-16, platformHalf+16, platformHalf+16),
		fmt.Sprintf("fill %d %d %d %d %d %d minecraft:stone",
			-platformHalf, platformY, -platformHalf, platformHalf, platformY, platformHalf),
	}
}

// villageDrive summons a villager population onto workstations.
//
// Villager AI is the heaviest load vanilla has, and the one lithium changes
// most, so this measures the flags and the performance stack together, which is
// what actually ships.
func villageDrive(p Params) []string {
	out := append([]string{}, preamble...)
	// Natural spawning off, so the entity count is exactly what was summoned
	// and two repeats are comparable.
	out = append(out, "gamerule doMobSpawning false")
	out = append(out, platform()...)
	// Composters rather than beds: one block each, a valid farmer workstation,
	// and one fill places the whole strip. Villagers pathing to a claimed POI is
	// the expensive part, and that needs the POI to exist.
	out = append(out, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:composter",
		-platformHalf, platformY+1, -platformHalf, platformHalf, platformY+1, -platformHalf+1))
	for i := range scale(40, p.Load) {
		x := -platformHalf + 2 + (i%18)*2
		z := platformHalf - 2 - (i/18)*2
		out = append(out, fmt.Sprintf("summon minecraft:villager %d %d %d", x, platformY+1, z))
	}
	// A player is present because some paths only run near one, and because a
	// village with nobody in it is not the situation worth measuring.
	out = append(out, fmt.Sprintf("player %s spawn at 0 %d 0", bots[0].Name, platformY+2))
	return out
}

// machinesDrive builds a powered Oritech machine array.
//
// oritech:creative_storage_block is an infinite energy source placeable with a
// single fill, which is the only reason this workload is scriptable at all: the
// alternative is fuel logistics, item insertion by NBT, or a structure file
// somebody has to build by hand in a client.
//
// What this measures is block entity ticking and energy network propagation on
// machines that are powered but not fed. It is NOT production throughput, and
// the report says so. Feeding them needs an item source and is a later question;
// the tick cost is the half that hurts a two-core box.
//
// ponytail: unfed machines, add an item source if a row ever needs to reflect
// machines processing rather than idling under power.
func machinesDrive(p Params) []string {
	out := append([]string{}, preamble...)
	out = append(out, "gamerule doMobSpawning false")
	out = append(out, platform()...)
	y := platformY + 1
	rows := machineRows(p.Load)
	// The storage row, and a spine of pipe running away from it along one edge.
	// Every machine row hangs off the spine, so one source powers all of them.
	out = append(out,
		fmt.Sprintf("fill %d %d %d %d %d %d oritech:creative_storage_block",
			-platformHalf, y, -platformHalf, platformHalf, y, -platformHalf),
		fmt.Sprintf("fill %d %d %d %d %d %d oritech:energy_pipe",
			-platformHalf, y, -platformHalf+1, -platformHalf, y, -platformHalf+2*rows))
	for r := range rows {
		z := -platformHalf + 1 + 2*r
		out = append(out,
			fmt.Sprintf("fill %d %d %d %d %d %d oritech:energy_pipe",
				-platformHalf+1, y, z, platformHalf, y, z),
			fmt.Sprintf("fill %d %d %d %d %d %d oritech:pulverizer_block",
				-platformHalf+1, y, z+1, platformHalf, y, z+1))
	}
	out = append(out, fmt.Sprintf("player %s spawn at 0 %d 0", bots[0].Name, y+1))
	return out
}

// machineRows keeps the array inside the platform it is built on.
func machineRows(load float64) int {
	n := scale(3, load)
	if n > platformHalf {
		return platformHalf
	}
	return n
}

// workloadAliases are the shorthands --workload accepts.
//
// "both" is first among equals: it is bench.yml's default and appears in every
// documented invocation, so it keeps working forever. The workflow's choice list
// once drifted from what this function accepted and failed a whole sweep on its
// first command.
var workloadAliases = map[string][]Workload{
	"":         {WorkloadVanilla, WorkloadPack},
	"both":     {WorkloadVanilla, WorkloadPack},
	"worldgen": {WorkloadVanilla, WorkloadPack},
	"players":  {WorkloadExplore, WorkloadVillage, WorkloadMachines},
	"all":      AllWorkloads,
}

// ParseWorkloads turns the --workload flag into the list to run. It takes an
// alias, one workload, or a comma-separated list of them.
func ParseWorkloads(s string) ([]Workload, error) {
	s = strings.TrimSpace(s)
	if ws, ok := workloadAliases[strings.ToLower(s)]; ok {
		return append([]Workload{}, ws...), nil
	}
	var out []Workload
	seen := map[Workload]bool{}
	for _, part := range strings.Split(s, ",") {
		w := Workload(strings.ToLower(strings.TrimSpace(part)))
		if w == "" {
			continue
		}
		if _, ok := Specs[w]; !ok {
			return nil, &UnknownWorkloadError{Value: string(w)}
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return nil, &UnknownWorkloadError{Value: s}
	}
	// Render order, not the order they were typed, so two invocations naming the
	// same workloads produce the same report.
	sort.SliceStable(out, func(i, j int) bool { return orderOf(out[i]) < orderOf(out[j]) })
	return out, nil
}

func orderOf(w Workload) int {
	for i, x := range AllWorkloads {
		if x == w {
			return i
		}
	}
	return len(AllWorkloads)
}

// UnknownWorkloadError is returned for an unrecognised --workload.
type UnknownWorkloadError struct{ Value string }

func (e *UnknownWorkloadError) Error() string {
	names := make([]string, 0, len(AllWorkloads))
	for _, w := range AllWorkloads {
		names = append(names, string(w))
	}
	return "unknown workload " + e.Value + "; want " + strings.Join(names, ", ") +
		", or one of both, worldgen, players, all"
}
