package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"weloveyou-mc/internal/discord"
)

func surfaceConfigFile(t *testing.T, mapImage string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wly.toml")
	body := `[surfaces]
server_address = "10.0.0.1"
map_url        = "http://map.invalid/"
instance_zip   = "http://pack.invalid/x.zip"
pack_page      = "http://pack.invalid/"
prism_image    = "http://pack.invalid/prism.png"
map_image      = "` + mapImage + `"

[channels.stable]
mc_version = "1.21.1"
pack_url   = "http://pack.invalid/pack.toml"
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A surface that cannot be built has to say so. Silently missing is
// indistinguishable from failed to post, and half of these are waiting on data
// sources that genuinely do not exist yet.
func TestBuildSurfacesSkipsLoudly(t *testing.T) {
	g, err := discord.Load(filepath.Join("..", "..", "guild.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg surfaceConfig
	cfg.Surfaces.ServerAddress = "10.0.0.1"

	built, skipped := buildSurfaces(g, cfg)
	if len(built) != 1 || built[0].name != "getstarted" {
		t.Fatalf("built = %v, want only getstarted until the data sources land", built)
	}
	if built[0].channelID == "" {
		t.Error("the surface has no channel id, so apply could not address it")
	}

	why := map[string]string{}
	for _, s := range skipped {
		why[s.name] = s.why
	}
	for _, want := range []string{"status", "spend", "events", "map", "release"} {
		if why[want] == "" {
			t.Errorf("%s was neither built nor explained", want)
		}
	}
	if !strings.Contains(why["map"], "broken image") {
		t.Errorf("map skipped for the wrong reason: %q", why["map"])
	}
}

// The map card is a gallery around a render nothing publishes yet. With one
// published it must build rather than stay skipped forever.
func TestMapBuildsOnceThereIsARender(t *testing.T) {
	g, err := discord.Load(filepath.Join("..", "..", "guild.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg surfaceConfig
	cfg.Surfaces.MapImage = "http://pack.invalid/map.png"
	built, _ := buildSurfaces(g, cfg)
	var names []string
	for _, b := range built {
		names = append(names, b.name)
	}
	if !slices.Contains(names, "map") {
		t.Errorf("built = %v, want map once an image exists", names)
	}
}

// recorder is what the fake Discord wrote down.
//
// Mutex-guarded, and that is not belt and braces: httptest serves each request
// on its OWN goroutine, so a handler appending here races any test reading it.
// The tests that poll while a background loop is still posting hit this every
// time under -race, which is exactly the kind of test-only race that hides a
// real one in the noise.
type recorder struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
}

func (r *recorder) add(path, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	r.bodies = append(r.bodies, body)
}

func (r *recorder) Paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func (r *recorder) Bodies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

// surfaceServer stands in for Discord and records what was written.
func surfaceServer(t *testing.T, existing string) *recorder {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/users/@me":
			_, _ = w.Write([]byte(`{"id":"botuser"}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/emojis"):
			_, _ = w.Write([]byte(`[{"id":"111","name":"heart"},{"id":"222","name":"coin"},
				{"id":"333","name":"map"},{"id":"444","name":"skull"},
				{"id":"555","name":"world"},{"id":"666","name":"player"}]`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = w.Write([]byte(existing))
		default:
			raw := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(raw)
			rec.add(r.Method+" "+r.URL.Path, string(raw))
			_, _ = w.Write([]byte(`{"id":"newmsg"}`))
		}
	}))
	// LIFO matters here. These are registered when the fake is built, so they
	// run LAST, after any cleanup a test registers afterwards to stop a
	// background loop. Restoring discordAPI while a goroutine is still reading
	// it through discordClient.do is the second race -race found.
	t.Cleanup(srv.Close)
	old := discordAPI
	discordAPI = srv.URL
	t.Cleanup(func() { discordAPI = old })
	return rec
}

// The first post is a one-way door: Components V2 sets its flag irreversibly on
// a message, so the flag has to be on the very first POST.
func TestSurfacesPostsWithTheComponentsV2Flag(t *testing.T) {
	rec := surfaceServer(t, `[]`)
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")

	var out bytes.Buffer
	err := runSurfaces([]string{"--config", filepath.Join("..", "..", "guild.toml"),
		"--wly", surfaceConfigFile(t, ""), "--apply"}, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	joined := strings.Join(rec.Paths(), " ")
	if !strings.Contains(joined, "POST /channels/") {
		t.Fatalf("nothing was posted: %v", rec.Paths())
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.Bodies()[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["flags"] != float64(discord.FlagComponentsV2) {
		t.Errorf("flags = %v, want IS_COMPONENTS_V2 on the first post", sent["flags"])
	}
	// And the placeholder became a real emoji, because Discord renders an
	// unresolved one as literal text in front of everyone. Checked on the
	// decoded content, not the raw body: encoding/json escapes the angle
	// bracket, so a substring search for the literal form fails on a
	// payload that is perfectly fine.
	head := sent["components"].([]any)[0].(map[string]any)["components"].([]any)[0]
	if got := head.(map[string]any)["content"].(string); !strings.Contains(got, "<:heart:111>") {
		t.Errorf("the emoji placeholder was posted unresolved: %q", got)
	}
	if !strings.Contains(joined, "PUT /channels/") {
		t.Errorf("the surface was never pinned: %v", rec.Paths())
	}
}

// Edited in place, never reposted. A channel of superseded status boards is
// worse than no status board.
func TestSurfacesEditsItsOwnMessageRatherThanPosting(t *testing.T) {
	rec := surfaceServer(t, `[
		{"id":"m2","author":{"id":"someoneelse"}},
		{"id":"m1","author":{"id":"botuser"}}
	]`)
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")

	var out bytes.Buffer
	if err := runSurfaces([]string{"--config", filepath.Join("..", "..", "guild.toml"),
		"--wly", surfaceConfigFile(t, ""), "--apply"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	joined := strings.Join(rec.Paths(), " ")
	if !strings.Contains(joined, "PATCH /channels/1541487310070087781/messages/m1") {
		t.Fatalf("it did not edit its own message: %v", rec.Paths())
	}
	if strings.Contains(joined, "POST /channels/") {
		t.Errorf("it reposted instead of editing: %v", rec.Paths())
	}
	if !strings.Contains(out.String(), "edited") {
		t.Errorf("output did not say it edited: %s", out.String())
	}
}

func TestSurfacesPrintsAndPostsNothingWithoutApply(t *testing.T) {
	rec := surfaceServer(t, `[]`)
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")

	var out bytes.Buffer
	if err := runSurfaces([]string{"--config", filepath.Join("..", "..", "guild.toml"),
		"--wly", surfaceConfigFile(t, "")}, &out); err != nil {
		t.Fatal(err)
	}
	if len(rec.Paths()) != 0 {
		t.Errorf("a dry run wrote to Discord: %v", rec.Paths())
	}
	if !strings.Contains(out.String(), "posted nothing") {
		t.Errorf("output did not say it changed nothing: %s", out.String())
	}
}

func TestSurfacesNeedsATokenAndAConfig(t *testing.T) {
	t.Setenv("WLY_DISCORD_TOKEN", "")
	err := runSurfaces([]string{"--config", filepath.Join("..", "..", "guild.toml"),
		"--wly", surfaceConfigFile(t, "")}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "WLY_DISCORD_TOKEN") {
		t.Errorf("error = %v, want it to name the missing token", err)
	}

	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	if err := runSurfaces([]string{"--wly", "nope.toml"}, &bytes.Buffer{}); err == nil {
		t.Error("accepted a missing surface config")
	}
	if err := runSurfaces([]string{"--nope"}, &bytes.Buffer{}); err == nil {
		t.Error("accepted an unknown flag")
	}
}
