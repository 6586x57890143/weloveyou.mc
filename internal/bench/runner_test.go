package bench

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeDocker replays a log instead of running a server.
type fakeDocker struct {
	log        string
	startErr   error
	logsErr    error
	mem        string
	memErr     error
	execs      []string
	removes    int
	startedEnv []string
}

func (f *fakeDocker) Start(name, image string, env []string) error {
	f.startedEnv = env
	return f.startErr
}
func (f *fakeDocker) Logs(string) (io.ReadCloser, error) {
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	return io.NopCloser(strings.NewReader(f.log)), nil
}
func (f *fakeDocker) Exec(_ string, args ...string) error {
	f.execs = append(f.execs, strings.Join(args, " "))
	return nil
}
func (f *fakeDocker) Stats(string) (string, error) { return f.mem, f.memErr }
func (f *fakeDocker) Remove(string) error          { f.removes++; return nil }

const happyLog = `[main/INFO]: Loading 87 mods
[1.0s][info][gc] GC(1) Pause Young 8.500ms
[Server thread/INFO]: Done (11.572s)! For help, type "help"
[2.0s][info][gc] GC(2) Pause Young 12.250ms
[Chunky] Task running for minecraft:overworld. Processed: 400 chunks
[Chunky] Task running for minecraft:overworld. Processed: 1200 chunks
[Chunky] Task finished for minecraft:overworld
`

func testProfile() (Profile, *Config) {
	cfg, _ := Parse([]byte(`
[compat]
java25plus = ["--sun-misc-unsafe-memory-access=allow"]
[profiles.p]
image = "itzg/minecraft-server:java25"
aikar = true
`))
	return cfg.Profiles[0], cfg
}

func TestExecuteHappyPath(t *testing.T) {
	p, cfg := testProfile()
	f := &fakeDocker{log: happyLog, mem: "175.20%	2.5GiB / 11.6GiB"}

	run, err := Execute(f, p, cfg, WorkloadPack, []string{"-XX:+UseCompactObjectHeaders"},
		500, clock(time.Second), DefaultTimeouts())
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if run.Chunks != 1200 {
		t.Errorf("chunks = %d, want 1200", run.Chunks)
	}
	if run.Startup != 11572*time.Millisecond {
		t.Errorf("startup = %v", run.Startup)
	}
	if len(run.GCPauses) != 2 {
		t.Errorf("gc pauses = %v, want 2", run.GCPauses)
	}
	if run.PeakRSS != 2.5*(1<<30) {
		t.Errorf("peak rss = %v", run.PeakRSS)
	}
	// Chunky must be told the radius before being told to start, or it
	// pregenerates whatever the previous run left configured.
	if len(f.execs) < 2 || !strings.Contains(f.execs[0], "radius 500") || !strings.Contains(f.execs[1], "start") {
		t.Errorf("chunky was driven as %v", f.execs)
	}
}

func TestExecuteAlwaysRemovesTheContainer(t *testing.T) {
	// A bench box littered with dead containers fills its disk, and then every
	// later run fails for a reason that has nothing to do with the flags.
	p, cfg := testProfile()
	for _, tc := range []struct {
		name string
		f    *fakeDocker
	}{
		{"success", &fakeDocker{log: happyLog}},
		{"never ready", &fakeDocker{log: "[main/INFO]: Loading 87 mods\n"}},
		{"log ends early", &fakeDocker{log: "Done (1.0s)!\n"}},
		{"logs unavailable", &fakeDocker{logsErr: errors.New("no such container")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = Execute(tc.f, p, cfg, WorkloadVanilla, nil, 100, clock(time.Second), DefaultTimeouts())
			if tc.f.removes < 2 {
				t.Errorf("container removed %d times, want a pre-clean and a teardown", tc.f.removes)
			}
		})
	}
}

func TestExecuteErrors(t *testing.T) {
	p, cfg := testProfile()
	tests := []struct {
		name string
		f    *fakeDocker
		want string
	}{
		{"start fails", &fakeDocker{startErr: errors.New("boom")}, "starting"},
		{"logs fail", &fakeDocker{logsErr: errors.New("nope")}, "following logs"},
		{"never became ready", &fakeDocker{log: "[main/INFO]: Loading mods\n"}, "never reported ready"},
		{"ready but never finished", &fakeDocker{log: "Done (1.0s)!\n"}, "log ended before"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Execute(tt.f, p, cfg, WorkloadVanilla, nil, 100, clock(time.Second), DefaultTimeouts())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestExecuteToleratesStatsFailing(t *testing.T) {
	// docker stats is best-effort; losing a sample must not lose the run.
	p, cfg := testProfile()
	f := &fakeDocker{log: happyLog, memErr: errors.New("stats unavailable")}
	run, err := Execute(f, p, cfg, WorkloadVanilla, nil, 100, clock(time.Second), DefaultTimeouts())
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if run.Chunks != 1200 {
		t.Errorf("the run should still be measured, chunks = %d", run.Chunks)
	}
}

func TestExecuteDefaultsItsClock(t *testing.T) {
	p, cfg := testProfile()
	if _, err := Execute(&fakeDocker{log: happyLog}, p, cfg, WorkloadVanilla, nil, 100, nil, DefaultTimeouts()); err != nil {
		t.Fatalf("Execute() with a nil clock = %v", err)
	}
}

func TestEnvSelectsTheWorkloadSource(t *testing.T) {
	p, cfg := testProfile()
	pack := strings.Join(Env(p, cfg, WorkloadPack, nil), " ")
	if !strings.Contains(pack, "PACKWIZ_URL="+PackURL) || strings.Contains(pack, "MODS=") {
		t.Errorf("pack workload env = %s", pack)
	}
	// The control loads Chunky and nothing else; a content mod here would make
	// it not a control.
	van := strings.Join(Env(p, cfg, WorkloadVanilla, nil), " ")
	if !strings.Contains(van, "MODS="+ChunkyURL) || strings.Contains(van, "PACKWIZ_URL") {
		t.Errorf("vanilla workload env = %s", van)
	}
	for _, want := range []string{"SEED=" + BenchSeed, "-Xlog:gc", "USE_AIKAR_FLAGS=true",
		"--sun-misc-unsafe-memory-access=allow"} {
		if !strings.Contains(van, want) {
			t.Errorf("env is missing %q", want)
		}
	}
}

func TestDefaultTimeoutsArePositive(t *testing.T) {
	to := DefaultTimeouts()
	if to.Boot <= 0 || to.Generate <= 0 || to.Sample <= 0 {
		t.Errorf("DefaultTimeouts() = %+v", to)
	}
}
