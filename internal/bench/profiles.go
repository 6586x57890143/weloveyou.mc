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
		cfg.Profiles = append(cfg.Profiles, Profile{
			Name: name, Description: r.Description, Image: r.Image,
			Aikar: r.Aikar, XX: r.XX, Opts: r.Opts, enabled: r.Enabled,
		})
	}
	// Deterministic order, so two sweeps of the same file produce rows in the
	// same sequence and a diff of BENCHMARKS.md shows only what moved.
	sort.Slice(cfg.Profiles, func(i, j int) bool { return cfg.Profiles[i].Name < cfg.Profiles[j].Name })
	return cfg, nil
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
