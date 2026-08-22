package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"weloveyou-mc/internal/bench"
)

// benchCmd runs the JVM flag sweep.
//
// The measurement itself is deliberately dumb: boot a server with a profile's
// flags, pregenerate a fixed radius from a fixed seed, record what happened,
// tear down, repeat. All the judgement lives in internal/bench, which is pure
// and tested; this file is the part that talks to Docker.
func benchCmd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		profiles = fs.String("profiles", "jvm-profiles.toml", "profile definitions")
		outPath  = fs.String("out", "BENCHMARKS.md", "where to write the results table")
		jsonPath = fs.String("json", "BENCHMARKS.json", "where to write the machine-readable results")
		workload = fs.String("workload", "both", "vanilla | pack | both")
		runs     = fs.Int("runs", 3, "repeats per profile; medians are reported")
		radius   = fs.Int("radius", 1000, "blocks to pregenerate")
		only     = fs.String("only", "", "run just this profile")
		shard    = fs.String("shard", "", `measure only this slice of the matrix, as "i/n"`)
		rawPath  = fs.String("raw", "", "dump this shard's raw results here, for a later --merge")
		merge    = fs.String("merge", "", "comma-separated raw result files to combine, instead of measuring")
		dry      = fs.Bool("dry-run", false, "preflight the flags and print the plan, measure nothing")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Merging is a different job from measuring: it takes shards that other
	// boxes already produced and renders the combined table, so it needs no
	// docker, no profiles file and no bench box.
	if *merge != "" {
		return mergeShards(*merge, *outPath, *jsonPath, out)
	}

	data, err := os.ReadFile(*profiles)
	if err != nil {
		return fmt.Errorf("reading profiles: %w", err)
	}
	cfg, err := bench.Parse(data)
	if err != nil {
		return err
	}

	todo, err := bench.Select(cfg.Runnable(), *only)
	if err != nil {
		return err
	}
	todo, err = bench.Shard(todo, *shard)
	if err != nil {
		return err
	}
	if len(todo) == 0 {
		return fmt.Errorf("shard %s selects no profiles", *shard)
	}
	workloads, err := bench.ParseWorkloads(*workload)
	if err != nil {
		return err
	}

	// Prove the probe works before trusting anything it says. Without this a
	// stopped Docker daemon makes every flag look refused, every profile
	// degrade to container defaults, and the sweep produce a full report that
	// means nothing. Fail-safe on a single flag is right; fail-safe on all of
	// them is just failure.
	if err := dockerProbe(todo[0].Image, []string{"-version"}); err != nil {
		return fmt.Errorf("cannot probe %s (is the docker daemon running?): %w", todo[0].Image, err)
	}

	host, _ := os.Hostname()
	hw := hardwareSnapshot()
	fmt.Fprintf(out, "hardware: %s\n", hw)
	fmt.Fprintf(out, "sweeping %d profile(s) x %d workload(s) x %d run(s) on %s\n",
		len(todo), len(workloads), *runs, host)

	var results []bench.Result
	for _, p := range todo {
		flags := append(append([]string{}, bench.Unlockers...), p.XX...)
		ok, dropped := bench.Preflight(p.Image, flags, dockerProbe)
		fmt.Fprintf(out, "\n%s (%s)\n  flags kept: %d", p.Name, p.Image, len(ok))
		if len(dropped) > 0 {
			fmt.Fprintf(out, ", refused: %s", strings.Join(dropped, " "))
		}
		fmt.Fprintln(out)

		for _, w := range workloads {
			res := bench.Result{Profile: p.Name, Hardware: hw, Heap: p.Heap(), Workload: w, Dropped: dropped}
			if *dry {
				results = append(results, res)
				continue
			}
			for i := range *runs {
				fmt.Fprintf(out, "  %s run %d/%d ... ", w, i+1, *runs)
				r, err := bench.Execute(dockerCLI{}, p, cfg, w, ok, *radius, time.Now, bench.DefaultTimeouts())
				if err != nil {
					fmt.Fprintf(out, "failed: %v\n", err)
					continue
				}
				fmt.Fprintf(out, "%.1f chunks/s\n", r.ChunksPerSec())
				res.Runs = append(res.Runs, r)
			}
			results = append(results, res)
		}
		// Flush after every profile rather than only at the end. A sweep that
		// dies partway used to lose everything: the last one ran seven hours,
		// completed nine runs, and was killed by the idle timer with nothing on
		// disk to show for it. Writing as we go turns that into nine usable
		// rows. A write error here is reported but not fatal, since losing the
		// remaining profiles to a transient failure would be the worse outcome.
		if !*dry {
			if err := writeReports(results, host, *outPath, *jsonPath); err != nil {
				fmt.Fprintf(out, "  warning: %v\n", err)
			}
		}
	}

	if *dry {
		fmt.Fprintln(out, "\ndry run: preflight only, nothing measured")
		return nil
	}

	if err := writeReports(results, host, *outPath, *jsonPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s\n", filepath.Base(*outPath))

	// This shard's raw results, for the merge step to collect. Go-native rather
	// than the presentation JSON, so a combined run goes through exactly the
	// same Render path as a single-box one and the two cannot disagree.
	if *rawPath != "" {
		raw, err := bench.MarshalResults(results)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*rawPath, raw, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", *rawPath, err)
		}
		fmt.Fprintf(out, "wrote %s\n", filepath.Base(*rawPath))
	}

	// A sweep where every run failed still writes a report, and used to exit 0
	// with it, so the workflow went green having measured nothing. Twelve
	// hours of that is worse than a red build, because an empty file looks like
	// a result and gets committed as one.
	measured := 0
	for _, r := range results {
		measured += len(r.Runs)
	}
	if measured == 0 {
		return fmt.Errorf("no run produced a measurement; see the failures above")
	}
	return nil
}

// dockerProbe asks a JVM whether it accepts a flag. This is the whole mechanism
// that keeps the profile file honest across JDK bumps.
func dockerProbe(image string, flags []string) error {
	args := append([]string{"run", "--rm", "--entrypoint", "java", image}, bench.ProbeArgs(flags)...)
	cmd := exec.Command("docker", args...)
	outp, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	// A rejected flag can still exit zero while saying so on stderr.
	for _, bad := range []string{"Unrecognized", "not supported", "Ignoring option"} {
		if strings.Contains(string(outp), bad) {
			return fmt.Errorf("%s", bad)
		}
	}
	return nil
}

// writeReports renders both the human table and its machine-readable twin.
//
// The two exist because the useful moments are separated in time: results live
// only while the sweep is running, but publishing waits until someone has read
// the numbers and merged them, so the data gets committed rather than re-parsed
// out of the prose later.
func writeReports(results []bench.Result, host, outPath, jsonPath string) error {
	now := time.Now()
	if err := os.WriteFile(outPath, []byte(bench.Render(results, host, now)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	if jsonPath == "" {
		return nil
	}
	doc, err := bench.RenderJSON(results, host, now)
	if err != nil {
		return fmt.Errorf("rendering json: %w", err)
	}
	if err := os.WriteFile(jsonPath, append(doc, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", jsonPath, err)
	}
	return nil
}

// mergeShards combines raw result files from several boxes into one report.
func mergeShards(list, outPath, jsonPath string, out io.Writer) error {
	var shards [][]byte
	var names []string
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading shard %s: %w", p, err)
		}
		shards = append(shards, raw)
		names = append(names, filepath.Base(p))
	}
	if len(shards) == 0 {
		return fmt.Errorf("--merge listed no readable files")
	}
	results, err := bench.MergeResults(shards)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	if err := writeReports(results, host, outPath, jsonPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "merged %d shard(s) into %s: %s\n",
		len(shards), filepath.Base(outPath), strings.Join(names, ", "))
	return nil
}

// hardwareSnapshot describes the machine this sweep is running on.
//
// Recorded with every result because a sharded sweep spans several boxes, and
// they are not automatically identical: two provisioned on the same day came up
// AmpereOne with four threads rather than Neoverse-N1 with two, which would
// have put numbers from two different machines in one table.
//
// Best effort throughout. A missing field costs a column, whereas failing the
// sweep because lscpu is not installed would cost the measurement.
func hardwareSnapshot() bench.Hardware {
	var h bench.Hardware
	if out, err := exec.Command("lscpu").Output(); err == nil {
		h = bench.ParseLscpu(string(out))
	}
	// runtime knows these without shelling out, and is right when lscpu is
	// absent or the platform is not Linux.
	if h.CPUs == 0 {
		h.CPUs = runtime.NumCPU()
	}
	if h.Arch == "" {
		h.Arch = runtime.GOARCH
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		h.MemoryMB = bench.ParseMemTotal(string(b))
	}
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		h.Kernel = strings.TrimSpace(string(out))
	}
	return h
}
