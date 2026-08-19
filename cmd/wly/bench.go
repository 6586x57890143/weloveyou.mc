package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		dry      = fs.Bool("dry-run", false, "preflight the flags and print the plan, measure nothing")
	)
	if err := fs.Parse(args); err != nil {
		return err
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
			res := bench.Result{Profile: p.Name, Heap: p.Heap(), Workload: w, Dropped: dropped}
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
	}

	if *dry {
		fmt.Fprintln(out, "\ndry run: preflight only, nothing measured")
		return nil
	}

	report := bench.Render(results, host, time.Now())
	if err := os.WriteFile(*outPath, []byte(report), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", *outPath, err)
	}
	fmt.Fprintf(out, "\nwrote %s\n", filepath.Base(*outPath))

	// The machine-readable twin, for the published site. The two useful moments
	// are separated in time — results exist only during the sweep, but
	// publishing waits until a human has read the numbers and merged them — so
	// the data is committed alongside the prose rather than re-parsed out of it.
	if *jsonPath != "" {
		doc, err := bench.RenderJSON(results, host, time.Now())
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		if err := os.WriteFile(*jsonPath, append(doc, '\n'), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", *jsonPath, err)
		}
		fmt.Fprintf(out, "wrote %s\n", filepath.Base(*jsonPath))
	}

	// A sweep where every run failed still writes a report, and used to exit 0
	// with it — so the workflow went green having measured nothing. Twelve
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
