package bench

import (
	"strings"
	"testing"
)

const sample = `
[compat]
java25plus = ["--sun-misc-unsafe-memory-access=allow", "--enable-native-access=ALL-UNNAMED"]

[profiles.baseline-j21]
description = "control"
image = "itzg/minecraft-server:java21"
aikar = true

[profiles.j25-g1-coh]
image = "itzg/minecraft-server:java25"
aikar = true
xx = ["-XX:+UseCompactObjectHeaders"]

[profiles.j26-g1]
image = "itzg/minecraft-server:java26"
aikar = true
enabled = false
`

func TestParse(t *testing.T) {
	cfg, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}
	if len(cfg.Profiles) != 3 {
		t.Fatalf("got %d profiles, want 3", len(cfg.Profiles))
	}
	// Sorted, so BENCHMARKS.md rows do not shuffle between runs.
	if cfg.Profiles[0].Name != "baseline-j21" {
		t.Errorf("profiles not sorted: first is %q", cfg.Profiles[0].Name)
	}
	if got := cfg.Profiles[1]; got.Name != "j25-g1-coh" || len(got.XX) != 1 {
		t.Errorf("j25-g1-coh parsed as %+v", got)
	}
}

func TestRunnableSkipsDisabled(t *testing.T) {
	cfg, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	run := cfg.Runnable()
	if len(run) != 2 {
		t.Fatalf("got %d runnable, want 2", len(run))
	}
	for _, p := range run {
		if p.Name == "j26-g1" {
			t.Error("a disabled profile was scheduled to run")
		}
	}
}

func TestEnabledDefaultsToTrue(t *testing.T) {
	// Absent means yes: adding a profile to the file should be enough to have
	// it measured, without remembering a flag.
	cfg, _ := Parse([]byte("[profiles.x]\nimage = \"itzg/minecraft-server:java21\"\n"))
	if !cfg.Profiles[0].Enabled() {
		t.Error("a profile with no enabled key should run")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"not toml", "this is not toml {{{", "parsing profiles"},
		{"no profiles", "[compat]\njava25plus = []\n", "no [profiles.*]"},
		{"profile without image", "[profiles.x]\ndescription = \"y\"\n", "has no image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestCompatForAppliesFrom25(t *testing.T) {
	cfg, _ := Parse([]byte(sample))
	byName := map[string]Profile{}
	for _, p := range cfg.Profiles {
		byName[p.Name] = p
	}
	if got := cfg.CompatFor(byName["baseline-j21"]); got != nil {
		t.Errorf("java21 should get no compat flags, got %v", got)
	}
	if got := cfg.CompatFor(byName["j25-g1-coh"]); len(got) != 2 {
		t.Errorf("java25 should get the compat flags, got %v", got)
	}
	if got := cfg.CompatFor(byName["j26-g1"]); len(got) != 2 {
		t.Errorf("java26 should get them too, got %v", got)
	}
}

func TestCompatForWithoutCompatSection(t *testing.T) {
	cfg, _ := Parse([]byte("[profiles.x]\nimage = \"itzg/minecraft-server:java25\"\n"))
	if got := cfg.CompatFor(cfg.Profiles[0]); got != nil {
		t.Errorf("no [compat] should yield no flags, got %v", got)
	}
}

func TestJavaMajor(t *testing.T) {
	tests := []struct {
		image string
		want  int
	}{
		{"itzg/minecraft-server:java21", 21},
		{"itzg/minecraft-server:java25", 25},
		{"itzg/minecraft-server:java25-graalvm", 25},
		{"itzg/minecraft-server:2026.8.0-java25-alpine", 25},
		// Unknown means assume nothing, so compat flags are skipped rather than
		// applied to a JDK that would reject them.
		{"itzg/minecraft-server:latest", 0},
		{"", 0},
	}
	for _, tt := range tests {
		if got := javaMajor(tt.image); got != tt.want {
			t.Errorf("javaMajor(%q) = %d, want %d", tt.image, got, tt.want)
		}
	}
}
