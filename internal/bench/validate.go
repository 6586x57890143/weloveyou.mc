package bench

import (
	"fmt"
	"sort"
	"strings"
)

// Validate reports the structural problems that make a sweep unpublishable.
//
// It exists because the only gate before this was "did any run at all succeed",
// which one row out of seventy-five satisfies. Everything else - a mixed
// hardware table, a pack that changed halfway through, a baseline that never
// ran - was narrated in prose and published anyway.
//
// The line is deliberate: this catches problems that make the WHOLE SWEEP
// misleading, not problems with individual profiles. A profile that crashed is
// a finding, renders as FAILED, and must still reach the page. A baseline that
// crashed is different in kind, because every `vs base` on the page is then
// computed against zero.
//
// Empty means publishable.
func Validate(rs []Result) []string {
	var bad []string
	add := func(f string, a ...any) { bad = append(bad, fmt.Sprintf(f, a...)) }

	if len(rs) == 0 {
		return []string{"no results at all"}
	}

	measured := 0
	for _, r := range rs {
		measured += len(r.Runs)
	}
	if measured == 0 {
		return []string{"no run produced a measurement"}
	}

	// A failed baseline poisons every comparison on the page, so it blocks
	// where a failed ordinary profile does not.
	for _, w := range AllWorkloads {
		rows := filter(rs, w)
		if len(rows) < 2 {
			continue
		}
		for _, r := range rows {
			if r.Profile == Baseline && r.Failed() {
				add("%s: the baseline profile %q produced no run, so every "+
					"`vs base` in that table is computed against zero", w, Baseline)
			}
		}
	}

	// One sweep, one pack. If the channel was republished halfway through, the
	// early rows and the late rows measured different content and nothing in
	// the rendered table would say so.
	packs := map[string]bool{}
	for _, r := range rs {
		if r.Pack.Known() {
			packs[r.Pack.String()] = true
		}
	}
	if len(packs) > 1 {
		add("rows measured %d different packs (%s); the channel was republished "+
			"mid-sweep and these rows are not comparable", len(packs), joinSorted(packs))
	}

	// Same for the machine. HardwareSpread already narrates this in the report;
	// here it stops the sweep.
	if hw := HardwareSpread(rs); len(hw) > 1 {
		var names []string
		for _, h := range hw {
			names = append(names, h.String())
		}
		add("rows measured on %d different machines (%s); a flag cannot be "+
			"compared across them", len(hw), strings.Join(names, " / "))
	}

	// Numbers that cannot be true. A tick rate above the ceiling or a negative
	// duration means the parser matched the wrong line, and every figure
	// derived from that run is suspect.
	for _, r := range rs {
		for i, run := range r.Runs {
			where := fmt.Sprintf("%s/%s run %d", r.Profile, r.Workload, i+1)
			if run.TPS > TPSCeiling+0.01 {
				add("%s: TPS %.2f is above the ceiling of %.0f", where, run.TPS, TPSCeiling)
			}
			if run.MSPTP95 < 0 || run.MSPTMed < 0 {
				add("%s: negative tick duration", where)
			}
			if run.Elapsed < 0 {
				add("%s: negative elapsed time", where)
			}
			if run.Chunks < 0 {
				add("%s: negative chunk count", where)
			}
		}
		// A pack workload that loaded no mods did not load the pack, whatever
		// else it reported.
		if SpecFor(r.Workload).Pack && !r.Failed() && r.Mods() == 0 {
			add("%s/%s: no mods loaded, so the pack did not install", r.Profile, r.Workload)
		}
	}
	return bad
}

func joinSorted(m map[string]bool) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
