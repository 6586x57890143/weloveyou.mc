package bench

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realPackToml is what weloveyou-pack actually serves, byte for byte.
const realPackToml = `name = "weloveyou"
author = "heavywinds"
version = "0.1.6"
pack-format = "packwiz:1.1.0"

[index]
file = "index.toml"
hash-format = "sha256"
hash = "26b5d8e7f3f5572ed0a2a42a0561f9417174b2f36d12e1df56184d6908db914b"

[versions]
fabric = "0.19.3"
minecraft = "1.21.1"
`

func TestParsePack(t *testing.T) {
	p, err := ParsePack([]byte(realPackToml))
	if err != nil {
		t.Fatalf("ParsePack() = %v", err)
	}
	// These four are the provenance a result could not previously state: which
	// pack, which exact content, and which Minecraft and loader it declares.
	for _, tc := range []struct{ got, want, what string }{
		{p.Version, "0.1.6", "version"},
		{p.IndexHash, "26b5d8e7f3f5572ed0a2a42a0561f9417174b2f36d12e1df56184d6908db914b", "index hash"},
		{p.Minecraft, "1.21.1", "minecraft"},
		{p.Fabric, "0.19.3", "fabric loader"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.what, tc.got, tc.want)
		}
	}
	if !p.Known() {
		t.Error("a parsed pack should report itself known")
	}
	if !strings.Contains(p.String(), "0.1.6") || !strings.Contains(p.String(), "26b5d8e7f3f5") {
		t.Errorf("String() = %q, want the version and a hash prefix", p.String())
	}
}

func TestParsePackRejectsWhatIsNotAPack(t *testing.T) {
	// Recording an empty fingerprint as though it were a real one is worse than
	// failing: every row would then agree with every other row.
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"not toml", "<!doctype html><html>404</html>"},
		{"toml but not a pack", "name = \"something\"\n[other]\nkey = 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParsePack([]byte(tc.body)); err == nil {
				t.Error("should have been rejected")
			}
		})
	}
}

func TestPackComparison(t *testing.T) {
	a := Pack{Version: "0.1.6", IndexHash: "aaaa"}
	// Same version, different content: exactly the case the mod count missed.
	b := Pack{Version: "0.1.6", IndexHash: "bbbb"}
	if a.Same(b) {
		t.Error("two packs with different index hashes are not the same pack")
	}
	if !a.Same(Pack{Version: "different-label", IndexHash: "aaaa"}) {
		t.Error("the hash decides, not the label")
	}
	var zero Pack
	if zero.Known() || zero.String() != "unrecorded" {
		t.Errorf("the zero pack should say it is unrecorded, got %q", zero.String())
	}
	// A run from before the hash existed still prints something useful.
	if got := (Pack{Version: "0.1.5"}).String(); got != "0.1.5" {
		t.Errorf("version-only pack = %q", got)
	}
}

func TestFetchPack(t *testing.T) {
	// httptest, not the network: the repo's rule is that no test reaches out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(realPackToml))
	}))
	defer srv.Close()

	p, err := FetchPack(srv.URL)
	if err != nil {
		t.Fatalf("FetchPack() = %v", err)
	}
	if p.Version != "0.1.6" || p.Fabric != "0.19.3" {
		t.Errorf("FetchPack() = %+v", p)
	}
}

func TestFetchPackFailsLoudly(t *testing.T) {
	// A pack URL that has rotted must not read as "no pack", because a sweep
	// would then measure the wrong thing and say nothing about it.
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer missing.Close()
	if _, err := FetchPack(missing.URL); err == nil {
		t.Error("a 404 should be an error")
	}
	if _, err := FetchPack("http://127.0.0.1:1/nope"); err == nil {
		t.Error("an unreachable host should be an error")
	}
}

func TestEnvPinsWhatThePackDeclares(t *testing.T) {
	// TYPE=FABRIC with no loader version let the image resolve "latest for
	// 1.21.1" at container start, so two sweeps a month apart could silently
	// run different loaders while production compose pinned 0.19.3.
	p, cfg := testProfile()
	pk := Pack{Version: "0.1.6", IndexHash: "abc", Minecraft: "1.21.1", Fabric: "0.19.3"}
	env := strings.Join(Env(p, cfg, WorkloadPack, nil, Params{Pack: pk}), " ")
	for _, want := range []string{"FABRIC_LOADER_VERSION=0.19.3", "VERSION=1.21.1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env is missing %q:\n%s", want, env)
		}
	}

	// A pack that declares a different Minecraft is followed, not overridden by
	// a constant that would then disagree with what actually booted.
	other := Params{Pack: Pack{Version: "9", IndexHash: "d", Minecraft: "1.21.4", Fabric: "0.20.0"}}
	env = strings.Join(Env(p, cfg, WorkloadPack, nil, other), " ")
	if !strings.Contains(env, "VERSION=1.21.4") || !strings.Contains(env, "FABRIC_LOADER_VERSION=0.20.0") {
		t.Errorf("env did not follow the pack:\n%s", env)
	}

	// The vanilla control loads no pack, so it falls back to the declared
	// default and pins no loader it has no basis to pin.
	van := strings.Join(Env(p, cfg, WorkloadVanilla, nil, Params{}), " ")
	if !strings.Contains(van, "VERSION="+DefaultMinecraft) {
		t.Errorf("the control should use the default Minecraft:\n%s", van)
	}
	if strings.Contains(van, "FABRIC_LOADER_VERSION") {
		t.Errorf("nothing declared a loader, so none should be pinned:\n%s", van)
	}
}

func TestEnvNeverTouchesTheProductionServer(t *testing.T) {
	// The harness resets worlds by design. The playable server must never be
	// reachable from it: no shared volume, no shared container name, no
	// production port.
	p, cfg := testProfile()
	for _, w := range AllWorkloads {
		env := strings.Join(Env(p, cfg, w, nil, Params{Pack: Pack{IndexHash: "x"}}), " ")
		for _, forbidden := range []string{"mc-data", "wly-mc", "25565", "/srv/app"} {
			if strings.Contains(env, forbidden) {
				t.Errorf("%s env names the production server (%q):\n%s", w, forbidden, env)
			}
		}
	}
}

func TestTPSShowsADeficit(t *testing.T) {
	// 19.97 rounded to "20.0" at one decimal place, identical to a workload
	// that never left the ceiling. It is the only reading this project has
	// produced where the server could not keep up.
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{19.97, "**19.97**"},
		{18.4, "**18.40**"},
		{20, "20"},
		{19.999, "20"}, // rounding noise at the ceiling is not a deficit
		{0, "-"},
	} {
		if got := tps(tc.in); got != tc.want {
			t.Errorf("tps(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTheReportSaysWhatItMeasured(t *testing.T) {
	full := []Result{{
		Profile: Baseline, Workload: WorkloadPack, Heap: "6G",
		Commit: "abc123", Radius: 400, Load: 3, Attempted: 3,
		Java: `openjdk version "21.0.5"`,
		Pack: Pack{Version: "0.1.6", IndexHash: "26b5d8e7f3f5572e", Minecraft: "1.21.1", Fabric: "0.19.3"},
		Runs: []Run{{Chunks: 10, Elapsed: time.Second, TPS: 20}},
	}}
	out := Render(full, "h", time.Unix(0, 0))
	for _, want := range []string{
		"0.1.6", "26b5d8e7f3f5", "minecraft 1.21.1", "fabric loader 0.19.3",
		"openjdk version", "abc123", BenchSeed, "400 blocks", "3x",
		// One of three finished: this must not read as a deliberate short run.
		"1 of 3 runs per profile",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not state %q:\n%s", want, out)
		}
	}

	// A result from before any of this existed still renders, without
	// inventing provenance it does not have.
	bare := []Result{{Profile: Baseline, Workload: WorkloadPack, Attempted: 1,
		Runs: []Run{{Chunks: 1, Elapsed: time.Second}}}}
	out = Render(bare, "h", time.Unix(0, 0))
	for _, absent := range []string{"pack:", "java:", "harness commit:", "pregen radius"} {
		if strings.Contains(out, absent) {
			t.Errorf("%q should be omitted when unrecorded:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, BenchSeed) {
		t.Error("the seed is a constant and should always be stated")
	}
	if firstPack(bare).Known() {
		t.Error("firstPack should find nothing in results that carry none")
	}
}

func TestPackStringCoversEveryShape(t *testing.T) {
	// A hash with no version happens when packwiz omits one; printing an empty
	// string there would make two different packs look identical.
	if got := (Pack{IndexHash: "abcdef123456789"}).String(); got != "abcdef123456" {
		t.Errorf("hash-only pack = %q", got)
	}
}

func TestFetchPackRejectsWhatIsNotAPack(t *testing.T) {
	// A URL that 200s with a login page or an index listing must fail, not be
	// recorded as an empty fingerprint that agrees with every other row.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>Index of /pack</title>"))
	}))
	defer html.Close()
	if _, err := FetchPack(html.URL); err == nil {
		t.Error("an HTML page should not parse as a pack")
	}
}
