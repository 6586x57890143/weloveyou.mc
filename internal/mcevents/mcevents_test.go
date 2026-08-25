package mcevents

import (
	"strings"
	"testing"
	"time"
)

// The authenticated line is the whole basis of the link gate, and this is the
// exact text a real server wrote, copied from latest.log on 2026-08-24. If
// Minecraft ever changes it, this fails here rather than in a gate that
// silently stops verifying anyone.
const realAuthLine = "[10:32:42] [User Authenticator #2/INFO]: " +
	"UUID of player denwa is 6dc57b83-7a60-4b47-baf4-f5ce8c501953"

func TestParseRealAuthLine(t *testing.T) {
	e, ok := Parse(realAuthLine)
	if !ok {
		t.Fatal("did not recognise a line a real server wrote")
	}
	if e.Kind != Authenticated {
		t.Errorf("kind = %v, want authenticated", e.Kind)
	}
	if e.Player != "denwa" {
		t.Errorf("player = %q", e.Player)
	}
	if e.UUID != "6dc57b83-7a60-4b47-baf4-f5ce8c501953" {
		t.Errorf("uuid = %q", e.UUID)
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		line   string
		kind   Kind
		player string
		detail string
	}{
		{"join", "[12:00:01] [Server thread/INFO]: kon joined the game", Joined, "kon", ""},
		{"leave", "[12:00:01] [Server thread/INFO]: kon left the game", Left, "kon", ""},
		{"advancement",
			"[12:00:01] [Server thread/INFO]: ellis has made the advancement [The End?]",
			Advancement, "ellis", "The End?"},
		// The thread counter moves, so it must not be matched literally.
		{"different authenticator thread",
			"[09:00:00] [User Authenticator #17/INFO]: UUID of player m is " +
				"11111111-2222-3333-4444-555555555555", Authenticated, "m", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := Parse(tc.line)
			if !ok {
				t.Fatalf("not recognised: %s", tc.line)
			}
			if e.Kind != tc.kind {
				t.Errorf("kind = %v, want %v", e.Kind, tc.kind)
			}
			if e.Player != tc.player {
				t.Errorf("player = %q, want %q", e.Player, tc.player)
			}
			if tc.detail != "" && e.Detail != tc.detail {
				t.Errorf("detail = %q, want %q", e.Detail, tc.detail)
			}
		})
	}
}

// A UUID is compared against one from Mojang, which may differ in case. Storing
// it lowercased once means every comparison downstream is a plain string match.
func TestUUIDIsLowercased(t *testing.T) {
	e, ok := Parse("[1] [User Authenticator #1/INFO]: UUID of player x is " +
		"6DC57B83-7A60-4B47-BAF4-F5CE8C501953")
	if !ok {
		t.Fatal("not recognised")
	}
	if e.UUID != "6dc57b83-7a60-4b47-baf4-f5ce8c501953" {
		t.Errorf("uuid = %q, want it lowercased", e.UUID)
	}
}

// Almost every line is not an event. Chat is the important one: a player can
// type anything, including something shaped like a join message, and it must
// never be mistaken for one.
func TestParseIgnores(t *testing.T) {
	for _, line := range []string{
		"",
		"[12:00:01] [Server thread/INFO]: <kon> denwa joined the game",
		// Chat that looks exactly like a death. The angle brackets are the
		// whole defence, because a death line has no marker of its own.
		"[12:00:01] [Server thread/INFO]: <kon> was slain by a zombie",
		"[12:00:01] [Server thread/INFO]: <kon> fell from a high place",
		// A mod talking about something that is not a player. A name is at
		// most 16 characters of [A-Za-z0-9_], which is what excludes these.
		"[12:00:01] [Server thread/INFO]: some.block.entity died",
		"[12:00:01] [Server thread/INFO]: [Oritech] machine blew up",
		"[12:00:01] [Server thread/INFO]: <kon> UUID of player admin is 1",
		"[12:00:01] [Server thread/WARN]: Can't keep up! Is the server overloaded?",
		"not a log line at all",
	} {
		if e, ok := Parse(line); ok {
			t.Errorf("parsed %q as %v, want it ignored", line, e.Kind)
		}
	}
}

func TestKindString(t *testing.T) {
	if Authenticated.String() != "authenticated" {
		t.Error("authenticated")
	}
	if !strings.Contains(Kind(99).String(), "99") {
		t.Error("unknown kind should name its number")
	}
}

// The numbers below come from a real stats file on the production server,
// 2026-08-24, so the shape and the tick conversion are both checked against
// something Minecraft actually wrote rather than something invented here.
const realStats = `{"stats":{"minecraft:custom":{
	"minecraft:play_time":33119,"minecraft:deaths":1,"minecraft:mob_kills":1,
	"minecraft:walk_one_cm":96936,"minecraft:jump":209,"minecraft:damage_taken":547},
	"minecraft:crafted":{"minecraft:oak_planks":80}}}`

func TestReadStats(t *testing.T) {
	s, err := ReadStats(strings.NewReader(realStats))
	if err != nil {
		t.Fatal(err)
	}
	if s.PlayTimeTicks != 33119 {
		t.Errorf("ticks = %d", s.PlayTimeTicks)
	}
	if s.Deaths != 1 || s.MobKills != 1 || s.WalkedCm != 96936 || s.Jumps != 209 {
		t.Errorf("stats = %+v", s)
	}
	// 33119 ticks at 20/s is 1655.95s, which is 27 minutes and change.
	if got := s.PlayTime().Truncate(time.Minute); got != 27*time.Minute {
		t.Errorf("play time = %v, want 27m", got)
	}
	// Discord's linked-role metadata is an integer, so this truncates.
	if s.Hours() != 0 {
		t.Errorf("hours = %d, want 0 for under an hour", s.Hours())
	}
	if s.Deathless() {
		t.Error("a player with one death is not deathless")
	}
}

// A brand new player has almost no keys at all. Treating a missing key as a
// parse failure would break the leaderboard on exactly the newest player.
func TestReadStatsMissingKeysAreZero(t *testing.T) {
	s, err := ReadStats(strings.NewReader(`{"stats":{"minecraft:custom":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s != (Statistics{}) {
		t.Errorf("stats = %+v, want all zero", s)
	}
	if !s.Deathless() {
		t.Error("a player who has never died is deathless")
	}
	if _, err := ReadStats(strings.NewReader(`{}`)); err != nil {
		t.Errorf("an empty document should read as zeroes, not fail: %v", err)
	}
}

func TestReadStatsRejectsGarbage(t *testing.T) {
	if _, err := ReadStats(strings.NewReader("not json")); err == nil {
		t.Fatal("accepted a file that is not JSON")
	}
}

func TestHoursRoundsDown(t *testing.T) {
	// 100 hours is the `rooted` threshold, so the boundary is worth pinning.
	s := Statistics{PlayTimeTicks: 100 * 3600 * 20}
	if s.Hours() != 100 {
		t.Errorf("hours = %d, want exactly 100 at the threshold", s.Hours())
	}
	s.PlayTimeTicks--
	if s.Hours() != 99 {
		t.Errorf("hours = %d, want 99 one tick below", s.Hours())
	}
}

// The repo's rule is that every parser gets a fuzzer. This one earns it twice
// over: the username in that line is attacker-influenced text, since anyone can
// attempt a join under any name, and the result of parsing it decides whether a
// Discord account is linked to a Minecraft one.
func FuzzParse(f *testing.F) {
	f.Add(realAuthLine)
	f.Add("[12:00:01] [Server thread/INFO]: kon joined the game")
	f.Add("[12:00:01] [Server thread/INFO]: <kon> hello")
	f.Add("[] []: UUID of player  is ")
	f.Add("[a] [User Authenticator/INFO]: UUID of player \x00 is " +
		"00000000-0000-0000-0000-000000000000")

	f.Fuzz(func(t *testing.T, line string) {
		e, ok := Parse(line)
		if !ok {
			return
		}
		// A recognised event must never carry an empty player, or a downstream
		// lookup keys on "" and matches whatever happens to be first.
		if e.Player == "" {
			t.Fatalf("parsed %q with no player", line)
		}
		if e.Kind != Authenticated {
			if e.UUID != "" {
				t.Fatalf("%v carried a uuid: %q", e.Kind, e.UUID)
			}
			return
		}
		// The uuid is what the link gate matches on, so its shape is the
		// invariant that matters most: 36 chars, hex and dashes, lowercase.
		if len(e.UUID) != 36 {
			t.Fatalf("uuid %q is %d chars, want 36", e.UUID, len(e.UUID))
		}
		if e.UUID != strings.ToLower(e.UUID) {
			t.Fatalf("uuid %q is not lowercased", e.UUID)
		}
		for _, r := range e.UUID {
			if !strings.ContainsRune("0123456789abcdef-", r) {
				t.Fatalf("uuid %q contains %q", e.UUID, r)
			}
		}
	})
}

// Deaths are the only lines with no marker, so they get their own table.
// Verified shapes: the phrase table in death.go is a vocabulary, and adding
// 1.22's messages should be adding strings to it.
func TestParseDeaths(t *testing.T) {
	for _, tc := range []struct{ line, player, detail string }{
		{"[12:00:01] [Server thread/INFO]: kon fell from a high place",
			"kon", "fell from a high place"},
		{"[12:00:01] [Server thread/INFO]: denwa was slain by Zombie",
			"denwa", "was slain by Zombie"},
		{"[12:00:01] [Server thread/INFO]: m tried to swim in lava to escape Blaze",
			"m", "tried to swim in lava to escape Blaze"},
		{"[12:00:01] [Server thread/INFO]: ellis was blown up by Creeper",
			"ellis", "was blown up by Creeper"},
		{"[12:00:01] [Server thread/INFO]: kon drowned",
			"kon", "drowned"},
		{"[12:00:01] [Server thread/INFO]: kon withered away",
			"kon", "withered away"},
		{"[12:00:01] [Server thread/INFO]: kon was squashed by a falling anvil",
			"kon", "was squashed by a falling anvil"},
	} {
		e, ok := Parse(tc.line)
		if !ok {
			t.Errorf("%q was not read as a death", tc.line)
			continue
		}
		if e.Kind != Died || e.Player != tc.player || e.Detail != tc.detail {
			t.Errorf("%q -> %v/%q/%q, want died/%q/%q",
				tc.line, e.Kind, e.Player, e.Detail, tc.player, tc.detail)
		}
	}
}

// The order of the phrase table is load-bearing: Go's alternation is
// leftmost-first, so a prefix listed before the longer phrase would swallow it
// and drop the reason.
func TestLongerDeathPhraseWins(t *testing.T) {
	e, ok := Parse("[12:00:01] [Server thread/INFO]: kon died because of Skeleton")
	if !ok || e.Detail != "died because of Skeleton" {
		t.Errorf("detail = %q, want the whole sentence rather than just \"died\"", e.Detail)
	}
}

// The lifecycle. RCON cannot answer "is it up": a server that is still starting
// refuses the connection exactly like one that is down, and those are very
// different things to tell a player.
func TestParseLifecycle(t *testing.T) {
	e, ok := Parse(`[12:00:01] [Server thread/INFO]: Done (12.345s)! For help, type "help"`)
	if !ok || e.Kind != ServerReady {
		t.Fatalf("startup line -> %v/%v", e.Kind, ok)
	}
	if e.Detail != "12.345s" {
		t.Errorf("detail = %q, want the boot time", e.Detail)
	}
	if e.Player != "" {
		t.Errorf("player = %q, a lifecycle event has none", e.Player)
	}
	if e, ok := Parse("[12:00:01] [Server thread/INFO]: Stopping the server"); !ok || e.Kind != ServerStopping {
		t.Errorf("stop line -> %v/%v", e.Kind, ok)
	}
}

// The exact lines the live server produced on 2026-08-25. spark answers on its
// own worker thread, so these arrive in latest.log and never in the RCON reply,
// which is the whole reason the bridge has to read them.
func TestParseSpark(t *testing.T) {
	const (
		tpsLine  = `[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  20.0, 20.0, 20.0, *20.0, *20.0`
		tickLine = `[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  0.6/0.7/1.0/1.6;  0.6/0.8/1.0/2.8`
	)

	tps, ok := ParseSpark(tpsLine)
	if !ok || !tps.HasTPS {
		t.Fatalf("TPS line not read: %v %v", tps, ok)
	}
	if len(tps.TPS) != 5 {
		t.Fatalf("got %d figures, want 5", len(tps.TPS))
	}
	// The starred ones are estimates, and must still parse as numbers.
	if tps.TPS[3] != 20.0 || tps.TPS[4] != 20.0 {
		t.Errorf("starred estimates did not parse: %v", tps.TPS)
	}
	if v, ok := tps.TPS1m(); !ok || v != 20.0 {
		t.Errorf("TPS1m = %v %v", v, ok)
	}

	tick, ok := ParseSpark(tickLine)
	if !ok || !tick.HasMSPT {
		t.Fatalf("tick line not read: %v %v", tick, ok)
	}
	// min/med/95%ile/max over the ONE MINUTE window, the second group.
	if tick.MSPT95 != 1.0 {
		t.Errorf("MSPT95 = %v, want the 1m group's 95%%ile of 1.0", tick.MSPT95)
	}
}

// Each line is matched on its shape, so a heading, a blank or another thread's
// output cannot be mistaken for figures.
func TestParseSparkIgnores(t *testing.T) {
	for _, line := range []string{
		`[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡] TPS from last 5s, 10s, 1m, 5m, 15m:`,
		`[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡] `,
		`[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  8%, 4%, 12%  (system)`,
		// Another mod printing numbers is not tick health.
		`[12:32:01] [Server thread/INFO]: [⚡]  20.0, 20.0, 20.0, 20.0, 20.0`,
		`[12:32:01] [Server thread/INFO]: kon fell from a high place`,
	} {
		if got, ok := ParseSpark(line); ok {
			t.Errorf("read %q as tick health: %+v", line, got)
		}
	}
}

// A death or a join must never be read as spark output, and vice versa.
func TestSparkAndEventsDoNotOverlap(t *testing.T) {
	spark := `[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  0.6/0.7/1.0/1.6;  0.6/0.8/1.0/2.8`
	if e, ok := Parse(spark); ok {
		t.Errorf("spark output parsed as an event: %v", e.Kind)
	}
	death := `[12:00:01] [Server thread/INFO]: kon fell from a high place`
	if tk, ok := ParseSpark(death); ok {
		t.Errorf("a death parsed as tick health: %+v", tk)
	}
}

// A figure that matches the shape but is not a number must be refused, not
// half-read. "1.2.3" gets past [\d.]+ and dies in ParseFloat, which is exactly
// the case a shape-based matcher has to handle.
func TestParseSparkRefusesMalformedFigures(t *testing.T) {
	for _, line := range []string{
		`[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  1.2.3, 20.0, 20.0, 20.0, 20.0`,
		`[12:32:01] [spark-worker-pool-1-thread-1/INFO]: [⚡]  0.6/0.7/1.0/1.6;  0.6/0.8/1.2.3/2.8`,
	} {
		if got, ok := ParseSpark(line); ok {
			t.Errorf("accepted a malformed figure from %q: %+v", line, got)
		}
	}
}

// TPS1m must say "no" rather than return a zero that reads as a dead server.
func TestTPS1mWithoutFigures(t *testing.T) {
	if _, ok := (Tick{}).TPS1m(); ok {
		t.Error("claimed a TPS figure with none read")
	}
	if _, ok := (Tick{HasTPS: true, TPS: []float64{20, 20}}).TPS1m(); ok {
		t.Error("claimed a 1m figure from a short list")
	}
	v, ok := Tick{HasTPS: true, TPS: []float64{1, 2, 3, 4, 5}}.TPS1m()
	if !ok || v != 3 {
		t.Errorf("TPS1m = %v %v, want the third figure", v, ok)
	}
}
