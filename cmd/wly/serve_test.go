package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"weloveyou-mc/internal/discord"
	"weloveyou-mc/internal/mcevents"
)

func testDaemon(t *testing.T) *daemon {
	t.Helper()
	return &daemon{
		out:     os.Stderr,
		channel: map[string]string{"events": "feed", "status": "st", "spend": "ops"},
		uuids:   map[string]string{},
		emoji:   map[string]string{},
		budget:  5,
	}
}

// The log carries a name; first-join detection needs the uuid. The
// Authenticated line always precedes the join, which is where the pairing comes
// from, and it is the whole reason that event is parsed at all.
func TestAuthenticatedSuppliesTheUUIDAJoinLacks(t *testing.T) {
	d := testDaemon(t)
	posts := make(chan discord.Payload, 4)

	d.handle(context.Background(), mcevents.Event{
		Kind: mcevents.Authenticated, Player: "kon",
		UUID: "6dc57b83-7a60-4b47-baf4-f5ce8c501953",
	}, posts)

	d.mu.Lock()
	got := d.uuids["kon"]
	d.mu.Unlock()
	if got != "6dc57b83-7a60-4b47-baf4-f5ce8c501953" {
		t.Errorf("uuid = %q, want the one Mojang authenticated", got)
	}
}

// An unknown uuid means an unknown answer, and the honest response is "no". A
// wrong welcome to a regular is worse than a missed one to a newcomer.
func TestFirstJoinSaysNoWhenItCannotTell(t *testing.T) {
	d := testDaemon(t)
	if d.firstJoin("stranger") {
		t.Error("welcomed a player whose uuid is unknown")
	}
}

// The lifecycle is learned from the log, not guessed from whether RCON answers:
// a server that is still starting refuses a connection exactly like one that is
// down, and those are very different things to tell a player.
func TestLifecycleTracksUpAndDown(t *testing.T) {
	d := testDaemon(t)
	d.channel = map[string]string{} // no Discord in this test; posting is a no-op path
	posts := make(chan discord.Payload, 4)

	// A cancelled context makes refreshStatus return before touching the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d.handle(ctx, mcevents.Event{Kind: mcevents.ServerReady, Detail: "12.345s"}, posts)
	d.mu.Lock()
	up, since := d.up, d.since
	d.mu.Unlock()
	if !up {
		t.Error("a ready server was not recorded as up")
	}
	if since.IsZero() {
		t.Error("no start time was recorded, so the board would say nothing")
	}

	d.handle(ctx, mcevents.Event{Kind: mcevents.ServerStopping}, posts)
	d.mu.Lock()
	up = d.up
	d.mu.Unlock()
	if up {
		t.Error("a stopping server was still recorded as up")
	}
}

// A full queue drops rather than blocks. A bridge blocked on Discord stops
// reading the log, which loses everything after it, and falling behind a flood
// is the survivable failure.
func TestFeedQueueDropsRatherThanBlocks(t *testing.T) {
	posts := make(chan discord.Payload, 2)
	done := make(chan struct{})
	go func() {
		for range 100 {
			send(posts, discord.Payload{})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("send blocked on a full queue; the bridge would stop reading the log")
	}
}

// A null amount is unknown and never zero, all the way from the file. Reporting
// a broken report as a free day is how credits run out quietly.
func TestCostReportKeepsNullDistinctFromZero(t *testing.T) {
	var r costReport
	if err := json.Unmarshal([]byte(`{"generated":"2026-08-25T06:30:24+00:00",
		"yesterday":null,"month_to_date":0.7372,"currency":"EUR"}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Yesterday != nil {
		t.Errorf("a null amount decoded as %v, want nil", *r.Yesterday)
	}
	if r.MonthToDate == nil || *r.MonthToDate != 0.7372 {
		t.Errorf("month to date = %v", r.MonthToDate)
	}
	if r.Currency != "EUR" {
		t.Errorf("currency = %q; nothing downstream may assume one", r.Currency)
	}

	// And a real zero stays a zero, or the distinction is pointless.
	var z costReport
	if err := json.Unmarshal([]byte(`{"yesterday":0}`), &z); err != nil {
		t.Fatal(err)
	}
	if z.Yesterday == nil || *z.Yesterday != 0 {
		t.Errorf("a real zero was lost: %v", z.Yesterday)
	}
}

// This is the shape the box actually writes, fetched from it on 2026-08-25. If
// cost-report.sh changes, this is what notices.
func TestCostReportMatchesTheRealFile(t *testing.T) {
	real := `{
  "generated": "2026-08-25T06:30:24+00:00",
  "yesterday": 0.1798,
  "month_to_date": 0.7372,
  "currency": "EUR",
  "by_service": {
    "compute": 0.1798
  }
}`
	var r costReport
	if err := json.Unmarshal([]byte(real), &r); err != nil {
		t.Fatal(err)
	}
	if r.Yesterday == nil || *r.Yesterday != 0.1798 {
		t.Errorf("yesterday = %v", r.Yesterday)
	}
	if r.Generated.IsZero() {
		t.Error("the timestamp did not parse, so staleness could never be detected")
	}
	if r.ByService["compute"] == nil {
		t.Error("by_service did not decode")
	}
}

func TestNextBackupIsAlwaysAhead(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 8, 25, 3, 59, 0, 0, time.UTC),
		time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 25, 23, 59, 0, 0, time.UTC),
	} {
		if got := nextBackup(now); !got.After(now) {
			t.Errorf("next backup from %v is %v, which is not ahead", now, got)
		}
	}
}

func TestDaysInMonth(t *testing.T) {
	for _, tc := range []struct {
		in   time.Time
		want int
	}{
		{time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC), 28},
		{time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC), 29}, // a leap year
		{time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), 31},
	} {
		if got := daysInMonth(tc.in); got != tc.want {
			t.Errorf("daysInMonth(%v) = %d, want %d", tc.in.Format("2006-01"), got, tc.want)
		}
	}
}

func TestEnvFloat(t *testing.T) {
	t.Setenv("WLY_TEST_BUDGET", "12.5")
	if got := envFloat("WLY_TEST_BUDGET", 5); got != 12.5 {
		t.Errorf("got %v", got)
	}
	t.Setenv("WLY_TEST_BUDGET", "not a number")
	if got := envFloat("WLY_TEST_BUDGET", 5); got != 5 {
		t.Errorf("a malformed budget must fall back to the default, got %v", got)
	}
	if got := envFloat("WLY_TEST_UNSET_BUDGET", 5); got != 5 {
		t.Errorf("got %v", got)
	}
}

func TestServeRejectsBadConfig(t *testing.T) {
	t.Setenv("WLY_DISCORD_TOKEN", "test-token")
	if err := runServe([]string{"--config", "nope.toml"}, os.Stderr); err == nil {
		t.Error("accepted a missing guild config")
	}
	if err := runServe([]string{"--config", filepath.Join("..", "..", "guild.toml"),
		"--wly", "nope.toml"}, os.Stderr); err == nil {
		t.Error("accepted a missing daemon config")
	}
	if err := runServe([]string{"--nope"}, os.Stderr); err == nil {
		t.Error("accepted an unknown flag")
	}
}

// Without DOCKER_HOST there is no uptime source, and the board must say nothing
// rather than invent one. The first live post said "up 2026 years ago" because
// a zero time reached the renderer.
func TestUptimeIsUnavailableWithoutTheProxy(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	d := testDaemon(t)
	if _, err := d.containerStarted(); err == nil {
		t.Error("claimed to know the uptime with no Docker API configured")
	}
}
