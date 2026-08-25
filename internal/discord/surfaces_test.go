package discord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The committed payloads under testdata/surfaces are the design mockup AND the
// golden file. These tests are the join between them: if Go stops emitting what
// the design says, one of the two moved and the diff shows which.
//
// A layout change is therefore always two reviewable diffs that have to agree,
// which is the whole point of authoring the design as real payloads in D0.

// goldenEquals compares a built payload against a committed one, ignoring the
// _meta block, which is documentation for the renderer and never sent.
func goldenEquals(t *testing.T, name string, got Payload) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "surfaces", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	delete(want, "_meta")

	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotMap map[string]any
	if err := json.Unmarshal(gotRaw, &gotMap); err != nil {
		t.Fatal(err)
	}

	wantPretty, _ := json.MarshalIndent(want, "", "  ")
	gotPretty, _ := json.MarshalIndent(gotMap, "", "  ")
	if string(wantPretty) != string(gotPretty) {
		t.Errorf("%s does not match its design payload.\n--- testdata/surfaces/%s.json\n%s\n--- built in Go\n%s",
			name, name, wantPretty, gotPretty)
	}
}

// The sample values are the ones the design was drawn with, so the golden stays
// readable as a mockup rather than turning into a fixture nobody can picture.
func TestGetStartedMatchesItsDesign(t *testing.T) {
	goldenEquals(t, "getstarted.b", GetStarted(GetStartedData{
		MinecraftVersion: "1.21.1",
		InstanceZipURL:   "https://weloveyou-pack.pages.dev/pack/stable/weloveyou-stable.zip",
		ServerAddress:    "158.180.53.71",
		PrismImageURL:    "https://weloveyou-pack.pages.dev/assets/prism-import.png",
	}))
}

func TestStatusMatchesItsDesign(t *testing.T) {
	goldenEquals(t, "status", Status(StatusData{
		Up:       true,
		Since:    time.Unix(1787580000, 0),
		Online:   []string{"kon", "ellis", "m"},
		MapURL:   "http://100.103.121.9:8123/",
		WorldDay: 412,
		// HasTick is what the golden was drawn with: a server where something
		// can actually answer for tick health. Nothing on production can today,
		// so the other branch is the one that runs, and it is covered below.
		HasTick:     true,
		TickFrom:    "spark",
		TPS:         19.98,
		MSPTp95:     8.4,
		PackVersion: "v0.1.7",
		MCVersion:   "1.21.1",
		NextBackup:  time.Unix(1787632200, 0),
	}))
}

func TestSpendMatchesItsDesign(t *testing.T) {
	y, m, p := 0.0338, 0.81, 1.02
	goldenEquals(t, "spend", Spend(SpendData{
		Day:           time.Unix(1787500800, 0),
		Yesterday:     &y,
		MonthToDate:   &m,
		Projected:     &p,
		Budget:        5,
		Currency:      "EUR",
		CreditsExpire: time.Unix(1789488000, 0),
		AverageDaily:  0.027,
	}))
}

func TestReleaseMatchesItsDesign(t *testing.T) {
	goldenEquals(t, "release", Release(ReleaseData{
		Version:     "v0.1.8",
		MCVersion:   "1.21.1",
		ModCount:    74,
		Added:       []string{"minimotd, so the server list can say something true"},
		Updated:     []string{"terralith 2.5.4 → 2.5.6", "oritech 1.2.9 → 1.3.0"},
		PackPageURL: "https://weloveyou-pack.pages.dev/pack/stable/",
	}))
}

func TestMapMatchesItsDesign(t *testing.T) {
	goldenEquals(t, "map", Map(MapData{
		WorldDay: 412,
		Rendered: time.Unix(1787580000, 0),
		ImageURL: "https://weloveyou-pack.pages.dev/assets/map-latest.png",
		MapURL:   "http://100.103.121.9:8123/",
	}))
}

func TestEventsMatchTheirDesign(t *testing.T) {
	goldenEquals(t, "events.death", Event(EventData{
		Kind: EventDeath, Player: "kon", Detail: "fell from a high place",
		WorldDay: 412, X: 214, Z: -88, HasWhere: true,
	}))
	goldenEquals(t, "events.advancement", Event(EventData{
		Kind: EventAdvancement, Player: "ellis", Detail: "The End?", WorldDay: 412,
	}))
	goldenEquals(t, "events.firstjoin", Event(EventData{
		Kind: EventFirstJoin, Player: "m", WorldDay: 412,
	}))
}

// 0,0 is a real place. A death at spawn must not look like a death whose
// coordinates nobody captured.
func TestDeathAtSpawnStillPrintsItsCoordinates(t *testing.T) {
	at := Event(EventData{Kind: EventDeath, Player: "kon", Detail: "drowned",
		WorldDay: 1, X: 0, Z: 0, HasWhere: true})
	if got := at.Components[0].Components[0].Content; !strings.Contains(got, "x 0, z 0") {
		t.Errorf("spawn death lost its coordinates: %q", got)
	}
	without := Event(EventData{Kind: EventDeath, Player: "kon", Detail: "drowned", WorldDay: 1})
	if got := without.Components[0].Components[0].Content; strings.Contains(got, "x 0") {
		t.Errorf("a death with no known place invented one: %q", got)
	}
}

// A null amount is unknown, never zero. Reporting a broken cost report as a free
// day is how credits run out quietly, which is the failure the surface exists
// to catch.
func TestSpendNeverReportsUnknownAsZero(t *testing.T) {
	p := Spend(SpendData{Day: time.Unix(1787500800, 0), Budget: 5, Currency: "EUR"})
	body := p.Components[0].Components[2].Content
	if !strings.Contains(body, "unknown") {
		t.Errorf("a missing amount was not reported as unknown: %q", body)
	}
	if strings.Contains(body, "0.000") || strings.Contains(body, "€0.00") {
		t.Errorf("a missing amount was rendered as a number: %q", body)
	}
	if got := *p.Components[0].AccentColor; got != AccentLose {
		t.Errorf("accent = #%06X, a missing report is a failure and reads as lose", got)
	}
}

// The escalation has to match cost-push.sh, because two things reporting the
// same numbers against different thresholds is worse than one.
func TestSpendEscalation(t *testing.T) {
	day := time.Unix(1787500800, 0)
	amt := func(v float64) *float64 { return &v }

	cases := []struct {
		name string
		in   SpendData
		want int
	}{
		{"quiet day", SpendData{Day: day, Yesterday: amt(0.03), Projected: amt(0.9),
			Budget: 5, AverageDaily: 0.03}, AccentDim},
		{"projected over budget", SpendData{Day: day, Yesterday: amt(0.2),
			Projected: amt(6), Budget: 5, AverageDaily: 0.2}, AccentBase},
		{"double the running average", SpendData{Day: day, Yesterday: amt(0.5),
			Projected: amt(1), Budget: 5, AverageDaily: 0.1}, AccentBase},
		{"stale report", SpendData{Day: day, Yesterday: amt(0.03), Projected: amt(1),
			Budget: 5, AverageDaily: 0.03, ReportAge: 48 * time.Hour}, AccentLose},
		{"no report at all", SpendData{Day: day, Budget: 5, ReportedMissed: true}, AccentLose},
	}
	for _, tc := range cases {
		if got := *Spend(tc.in).Components[0].AccentColor; got != tc.want {
			t.Errorf("%s: accent #%06X, want #%06X", tc.name, got, tc.want)
		}
	}
}

// A server that is off still has a status, and saying so is the whole job.
func TestStatusWhenNobodyIsOnAndWhenItIsDown(t *testing.T) {
	empty := Status(StatusData{Up: true, Online: nil})
	if got := empty.Components[0].Components[2].Components[0].Content; !strings.Contains(got, "nobody") {
		t.Errorf("an empty server claimed players: %q", got)
	}
	down := Status(StatusData{Up: false})
	if got := *down.Components[0].AccentColor; got != AccentLose {
		t.Errorf("a down server read as #%06X", got)
	}
	if got := down.Components[0].Components[0].Content; !strings.Contains(got, "down") {
		t.Errorf("a down server still claimed to be up: %q", got)
	}
	// A zero time is "we do not know", not the year 1. The first live post of
	// this board said "up 2026 years ago", because the daemon started after the
	// server and had no source for uptime.
	for _, p := range []Payload{down, Status(StatusData{Up: true})} {
		if got := p.Components[0].Components[0].Content; strings.Contains(got, "<t:-") ||
			strings.Contains(got, "<t:0:") {
			t.Errorf("rendered a zero timestamp: %q", got)
		}
	}
	known := Status(StatusData{Up: false, Since: time.Unix(1787580000, 0)})
	if got := known.Components[0].Components[0].Content; !strings.Contains(got, "down since") {
		t.Errorf("a known downtime lost its timestamp: %q", got)
	}
	// An empty pack version rendered as "****", which collapses into a stray
	// glyph and reads as corruption rather than as a missing value.
	if got := down.Components[0].Components[4].Content; strings.Contains(got, "****") {
		t.Errorf("empty bold markers reached the board: %q", got)
	}
}

// Discord renders an unresolved placeholder as literal text, so a surface that
// posts with one looks broken to every reader. Refusing is the better failure.
func TestResolveEmoji(t *testing.T) {
	p := GetStarted(GetStartedData{})
	if _, err := ResolveEmoji(p, map[string]string{}); err == nil {
		t.Fatal("posted a surface whose custom emoji do not exist")
	}
	done, err := ResolveEmoji(p, map[string]string{"heart": "123"})
	if err != nil {
		t.Fatal(err)
	}
	if got := done.Components[0].Components[0].Content; !strings.Contains(got, "<:heart:123>") {
		t.Errorf("placeholder not resolved: %q", got)
	}
	// And it reaches inside sections and accessories, not only the top level.
	nested := Container(AccentInfo,
		Section(LinkButton("x", "https://example.invalid"), Text("<:coin:> hello")))
	out, err := ResolveEmoji(nested, map[string]string{"coin": "9"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Components[0].Components[0].Components[0].Content; !strings.Contains(got, "<:coin:9>") {
		t.Errorf("a placeholder inside a section survived: %q", got)
	}
}

// Every custom_id a surface uses must be declared in guild.toml, or it is a
// button that does nothing when pressed, which is worse than an absent one
// because a player has already committed to it.
func TestEveryCustomIDIsDeclared(t *testing.T) {
	g, err := Load("../../guild.toml")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, b := range g.Interactions.Buttons {
		declared[b] = true
	}
	var walk func([]Component)
	walk = func(cs []Component) {
		for _, c := range cs {
			if c.CustomID != "" && !declared[c.CustomID] {
				t.Errorf("custom_id %q is used by a surface and not declared in "+
					"guild.toml [interactions]", c.CustomID)
			}
			walk(c.Components)
			if c.Accessory != nil {
				walk([]Component{*c.Accessory})
			}
		}
	}
	walk(GetStarted(GetStartedData{}).Components)
}

// Nothing on this server can answer for tick health: spark is pinned in the
// bench harness and is not in pack/stable, and `spark tps` over RCON on the live
// server answers "Unknown or incomplete command". The board must therefore not
// print a TPS figure, because a confident 20.0 that nothing measured is exactly
// the failure this project keeps writing rules about.
func TestStatusWillNotInventTickHealth(t *testing.T) {
	world := Status(StatusData{
		Up: true, WorldDay: 412, Latency: 37 * time.Millisecond,
	}).Components[0].Components[3].Content

	if strings.Contains(world, "TPS") || strings.Contains(world, "p95") {
		t.Errorf("the board claimed tick health with no source: %q", world)
	}
	if !strings.Contains(world, "**37ms** server response") {
		t.Errorf("the board dropped the one number it can measure: %q", world)
	}
	if !strings.Contains(world, "day **412**") {
		t.Errorf("the world day went missing: %q", world)
	}
}
