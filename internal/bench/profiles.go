// Package bench drives the JVM flag sweep: it reads candidate profiles, drops
// flags the JVM rejects, runs a fixed workload against each, and renders the
// medians into BENCHMARKS.md.
//
// Everything that decides something lives here as a pure function. The parts
// that talk to Docker and the clock are interfaces, so the interesting logic is
// testable without a container.
package bench

import (
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"
)

// Profile is one candidate configuration: a JDK image plus the flags to try.
type Profile struct {
	Name        string   `toml:"-"`
	Description string   `toml:"description"`
	Image       string   `toml:"image"`
	Aikar       bool     `toml:"aikar"`
	XX          []string `toml:"xx"`
	Opts        []string `toml:"opts"`
	enabled     *bool
}

// Enabled reports whether the sweep should run this profile. Absent means yes:
// a profile is opt-out, so adding one to the file is enough to have it measured.
func (p Profile) Enabled() bool { return p.enabled == nil || *p.enabled }

type rawProfile struct {
	Description string   `toml:"description"`
	Image       string   `toml:"image"`
	Aikar       bool     `toml:"aikar"`
	Flagsets    []string `toml:"flagsets"`
	XX          []string `toml:"xx"`
	Opts        []string `toml:"opts"`
	Enabled     *bool    `toml:"enabled"`
}

// Config is jvm-profiles.toml.
type Config struct {
	// Compat holds launcher options applied to whole families of JDK, keyed by
	// an arbitrary name. They are not -XX flags, so they travel separately.
	Compat   map[string][]string
	Profiles []Profile
}

type rawConfig struct {
	Compat   map[string][]string   `toml:"compat"`
	Flagsets map[string][]string   `toml:"flagsets"`
	Profiles map[string]rawProfile `toml:"profiles"`
}

// Parse reads jvm-profiles.toml.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing profiles: %w", err)
	}
	if len(raw.Profiles) == 0 {
		return nil, fmt.Errorf("no [profiles.*] defined")
	}
	cfg := &Config{Compat: raw.Compat}
	for name, r := range raw.Profiles {
		if r.Image == "" {
			return nil, fmt.Errorf("profile %q has no image", name)
		}
		xx, err := resolveFlagsets(name, r, raw.Flagsets)
		if err != nil {
			return nil, err
		}
		cfg.Profiles = append(cfg.Profiles, Profile{
			Name: name, Description: r.Description, Image: r.Image,
			Aikar: r.Aikar, XX: xx, Opts: r.Opts, enabled: r.Enabled,
		})
	}
	// Deterministic order, so two sweeps of the same file produce rows in the
	// same sequence and a diff of BENCHMARKS.md shows only what moved.
	sort.Slice(cfg.Profiles, func(i, j int) bool { return cfg.Profiles[i].Name < cfg.Profiles[j].Name })
	return cfg, nil
}

// resolveFlagsets expands a profile's named flagsets into one flat flag list.
//
// The matrix is a JDK dimension crossed with a collector dimension, and most
// cells share the same twenty-flag base. Naming the shared lists once keeps the
// file readable and, more usefully, lets a set be omitted where it does nothing
// - the C2-only flags are inert on GraalVM, where Graal replaces C2, and they
// are ACCEPTED there, so the preflight cannot drop them for us.
//
// Flagset flags come first so a profile's own xx is the last word on a conflict.
// An unknown name is an error rather than an empty expansion: a typo would
// otherwise silently measure the wrong thing and still produce a plausible row.
func resolveFlagsets(name string, r rawProfile, sets map[string][]string) ([]string, error) {
	if len(r.Flagsets) == 0 {
		return r.XX, nil
	}
	var out []string
	for _, set := range r.Flagsets {
		flags, ok := sets[set]
		if !ok {
			return nil, fmt.Errorf("profile %q references undefined flagset %q", name, set)
		}
		out = append(out, flags...)
	}
	return append(out, r.XX...), nil
}

// Runnable returns the profiles the sweep should actually execute.
func (c *Config) Runnable() []Profile {
	var out []Profile
	for _, p := range c.Profiles {
		if p.Enabled() {
			out = append(out, p)
		}
	}
	return out
}

// CompatFor returns the launcher options for a profile's JDK. Anything from 25
// onward needs Unsafe and native access permitted explicitly, because the
// defaults tighten with each release and we would rather the behaviour not
// change under us when the base image moves.
func (c *Config) CompatFor(p Profile) []string {
	if c.Compat == nil {
		return nil
	}
	if javaMajor(p.Image) >= 25 {
		return c.Compat["java25plus"]
	}
	return nil
}
