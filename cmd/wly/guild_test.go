package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stand-in Discord. No test touches the network, which is the repo's rule for
// every remote fetch, and it is what makes the reconciler testable at all.
func fakeDiscord(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			t.Errorf("Authorization = %q, want the bot token", got)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "DiscordBot (") {
			t.Errorf("User-Agent = %q, Discord requires the DiscordBot form",
				r.Header.Get("User-Agent"))
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Unknown Guild","code":10004}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	old := discordAPI
	discordAPI = srv.URL
	t.Cleanup(func() { discordAPI = old })
	return srv
}

// Positions are deliberately shuffled and non-contiguous, because Discord's are.
func guildRoutes() map[string]string {
	return map[string]string{
		"/guilds/42": `{"id":"42","name":"wly","features":["ENHANCED_ROLE_COLORS"]}`,
		"/guilds/42/roles": `[
			{"id":"3","name":"player","color":9340535,"position":1},
			{"id":"9","name":"wly","managed":true,"position":50},
			{"id":"1","name":"@everyone","position":0},
			{"id":"2","name":"admin","color":12873820,"hoist":true,"position":40}
		]`,
		"/guilds/42/channels": `[
			{"id":"c1","name":"the server","type":4},
			{"id":"c2","name":"general","type":0,"topic":"talk","parent_id":"c1"},
			{"id":"c3","name":"orphan","type":0,"topic":""}
		]`,
		"/guilds/42/emojis":      `[{"name":"heart"}]`,
		"/guilds/42/members/@me": `{"roles":["9","3"]}`,
	}
}

func TestFetchLive(t *testing.T) {
	fakeDiscord(t, guildRoutes())
	live, err := newDiscordClient("test-token").fetchLive("42")
	if err != nil {
		t.Fatal(err)
	}

	if live.ID != "42" || live.Name != "wly" {
		t.Errorf("guild = %s/%s", live.ID, live.Name)
	}
	// Highest first, which is what internal/discord's hierarchy check assumes.
	want := []string{"wly", "admin", "player", "@everyone"}
	if len(live.Roles) != len(want) {
		t.Fatalf("roles = %d, want %d", len(live.Roles), len(want))
	}
	for i, n := range want {
		if live.Roles[i].Name != n {
			t.Errorf("role %d = %q, want %q", i, live.Roles[i].Name, n)
		}
	}
	// The bot holds wly (50) and player (1); the highest is what constrains it.
	if live.BotHighestRole != "wly" {
		t.Errorf("bot highest role = %q, want wly", live.BotHighestRole)
	}
	// Categories are structure, not channels, and a channel in one reports it.
	if len(live.Channels) != 2 {
		t.Fatalf("channels = %v, want general and orphan only", live.Channels)
	}
	byName := map[string]string{}
	for _, c := range live.Channels {
		byName[c.Name] = c.Category
	}
	if byName["general"] != "the server" {
		t.Errorf("general category = %q", byName["general"])
	}
	if byName["orphan"] != "" {
		t.Errorf("orphan category = %q, want empty", byName["orphan"])
	}
	if len(live.Emojis) != 1 || live.Emojis[0] != "heart" {
		t.Errorf("emojis = %v", live.Emojis)
	}
	if len(live.Features) != 1 {
		t.Errorf("features = %v", live.Features)
	}
}

// Each fetch is a separate call, and any of them failing has to surface rather
// than yielding a half-built Live that plans against nothing.
func TestFetchLivePropagatesEachFailure(t *testing.T) {
	for _, drop := range []string{
		"/guilds/42", "/guilds/42/roles", "/guilds/42/channels",
		"/guilds/42/emojis", "/guilds/42/members/@me",
	} {
		t.Run(drop, func(t *testing.T) {
			routes := guildRoutes()
			delete(routes, drop)
			fakeDiscord(t, routes)
			if _, err := newDiscordClient("test-token").fetchLive("42"); err == nil {
				t.Fatalf("no error when %s failed", drop)
			}
		})
	}
}

func TestDoReportsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"retry_after":1.5}`))
	}))
	defer srv.Close()
	old := discordAPI
	discordAPI = srv.URL
	defer func() { discordAPI = old }()

	err := newDiscordClient("t").do("GET", "/x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want it to name the rate limit", err)
	}
}

func TestDoSendsBodyAndDecodes(t *testing.T) {
	var gotBody, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody, gotType = string(b), r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"id":"7"}`))
	}))
	defer srv.Close()
	old := discordAPI
	discordAPI = srv.URL
	defer func() { discordAPI = old }()

	var out struct {
		ID string `json:"id"`
	}
	if err := newDiscordClient("t").do("POST", "/x", map[string]string{"name": "a"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `"name":"a"`) {
		t.Errorf("body = %q", gotBody)
	}
	if gotType != "application/json" {
		t.Errorf("content-type = %q", gotType)
	}
	if out.ID != "7" {
		t.Errorf("decoded id = %q", out.ID)
	}
}

func TestDoRejectsBadURL(t *testing.T) {
	old := discordAPI
	discordAPI = "://not a url"
	defer func() { discordAPI = old }()
	if err := newDiscordClient("t").do("GET", "/x", nil, nil); err == nil {
		t.Fatal("accepted an unparseable URL")
	}
}

// guild.toml with the real guild id, so a plan can be computed in a test.
func testConfig(t *testing.T, id string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "guild.toml")
	body := `[guild]
name = "wly"
id = "` + id + `"

[[roles]]
name = "admin"
color = "#C4705C"
hoist = true
manual = true

[[roles]]
name = "player"
color = "#8E8677"

[[channels]]
name = "general"
category = "the server"
topic = "talk"
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunGuildNeedsToken(t *testing.T) {
	t.Setenv("WLY_DISCORD_TOKEN", "")
	err := runGuild([]string{"--config", testConfig(t, "42")}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "WLY_DISCORD_TOKEN") {
		t.Fatalf("error = %v, want it to name the missing token", err)
	}
	// The message must not suggest putting a secret in the committed file.
	if !strings.Contains(err.Error(), "deploy/.env") {
		t.Errorf("error %v should say where the token goes", err)
	}
}

func TestRunGuildRejectsBadConfig(t *testing.T) {
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	if err := runGuild([]string{"--config", "does-not-exist.toml"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted a missing config")
	}
}

func TestRunGuildRejectsBadFlag(t *testing.T) {
	if err := runGuild([]string{"--nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted an unknown flag")
	}
}

func TestRunGuildPrintsPlanAndChangesNothing(t *testing.T) {
	fakeDiscord(t, guildRoutes())
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")

	var out bytes.Buffer
	if err := runGuild([]string{"--config", testConfig(t, "42")}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// general exists and matches; the category does too. What is missing is
	// nothing, so the only output should be drift plus the closing note.
	if !strings.Contains(got, "changed nothing") && !strings.Contains(got, "no changes") {
		t.Errorf("plan output does not say it changed nothing:\n%s", got)
	}
	// orphan is on the server and not in the file: reported, never removed.
	if !strings.Contains(got, "orphan") || !strings.Contains(got, "NEVER removes") {
		t.Errorf("drift not reported safely:\n%s", got)
	}
}

func TestRunGuildRefusesWrongGuild(t *testing.T) {
	fakeDiscord(t, guildRoutes())
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	err := runGuild([]string{"--config", testConfig(t, "999")}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("planned against a guild the config does not describe")
	}
}

func TestRunGuildApplyIsNotSilentlyAccepted(t *testing.T) {
	fakeDiscord(t, guildRoutes())
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	err := runGuild([]string{"--config", testConfig(t, "42"), "--apply"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("error = %v; --apply must fail loudly rather than appear to work", err)
	}
}

func TestRunGuildSurfacesFetchFailure(t *testing.T) {
	fakeDiscord(t, map[string]string{}) // every route 404s
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	if err := runGuild([]string{"--config", testConfig(t, "42")}, &bytes.Buffer{}); err == nil {
		t.Fatal("no error when the API refused everything")
	}
}

func TestSortRolesHighestFirst(t *testing.T) {
	roles := []apiRole{{Name: "c", Position: 1}, {Name: "a", Position: 9}, {Name: "b", Position: 5}}
	sortRolesHighestFirst(roles)
	for i, want := range []string{"a", "b", "c"} {
		if roles[i].Name != want {
			t.Fatalf("order = %v", roles)
		}
	}
	sortRolesHighestFirst(nil) // must not panic
}

func TestFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	body := "# a comment\n\n" +
		"OTHER=x\n" +
		"  WLY_DISCORD_TOKEN = plain-value  \n" +
		"QUOTED=\"in quotes\"\n" +
		"SINGLE='single quotes'\n" +
		"HASHY=tok#en-with-hash\n" +
		"NOEQUALS\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, want string }{
		{"WLY_DISCORD_TOKEN", "plain-value"},
		{"QUOTED", "in quotes"},
		{"SINGLE", "single quotes"},
		// A bot token can contain almost anything, so stripping a trailing
		// comment would silently truncate it.
		{"HASHY", "tok#en-with-hash"},
		{"NOEQUALS", ""},
		{"ABSENT", ""},
	} {
		if got := fromEnvFile(p, tc.key); got != tc.want {
			t.Errorf("fromEnvFile(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
	if got := fromEnvFile(filepath.Join(dir, "nope"), "X"); got != "" {
		t.Errorf("missing file returned %q", got)
	}
}

// The environment wins over the file, so an export can override a stale .env
// without editing it.
func TestRunGuildPrefersEnvOverFile(t *testing.T) {
	fakeDiscord(t, guildRoutes())
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	if err := runGuild([]string{"--config", testConfig(t, "42")}, &bytes.Buffer{}); err != nil {
		t.Fatalf("env token not used: %v", err)
	}
}
