package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"weloveyou-mc/internal/discord"
)

// liveDaemon is a daemon wired to the fake Discord, so the loops can be run for
// real rather than have their halves tested apart.
func liveDaemon(t *testing.T) (*daemon, *recorder) {
	t.Helper()
	rec := surfaceServer(t, `[]`)
	d := testDaemon(t)
	d.api = newDiscordClient("test-token")
	d.emoji = map[string]string{"heart": "1", "coin": "2", "skull": "3",
		"world": "4", "player": "5", "map": "6"}
	d.out = os.Stderr
	return d, rec
}

// startFeedWorker runs the worker and GUARANTEES it has stopped before the test
// finishes.
//
// Cancelling is not enough: cancel only asks, and the goroutine keeps running
// for a moment afterwards. surfaceServer's cleanup restores the package-level
// discordAPI, and a worker still inside discordClient.do is reading it, which is
// a real data race that -race catches every run. Registering this cleanup AFTER
// the fake server's means it runs BEFORE them, because cleanups are LIFO.
func startFeedWorker(t *testing.T, d *daemon, channelID string, posts <-chan discord.Payload) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.feedWorker(ctx, channelID, posts)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func writeCost(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cost.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The real file from the box, and the numbers have to survive the whole way to
// the payload rather than being rounded into something else.
func TestRefreshSpendReadsTheRealReport(t *testing.T) {
	d, rec := liveDaemon(t)
	d.cfg.Cost.ReportPath = writeCost(t, `{
	  "generated": "`+time.Now().UTC().Format(time.RFC3339)+`",
	  "yesterday": 0.1798, "month_to_date": 0.7372, "currency": "EUR",
	  "by_service": {"compute": 0.1798}}`)

	d.refreshSpend(context.Background())

	if len(rec.Paths()) == 0 {
		t.Fatal("the spend surface was never posted")
	}
	body := strings.Join(rec.Bodies(), "")
	for _, want := range []string{"0.18", "0.74", "5.00"} {
		if !strings.Contains(body, want) {
			t.Errorf("posted spend does not mention %s: %s", want, body)
		}
	}
	// Projected is month-to-date scaled to the whole month, so it must exceed
	// what has been spent so far rather than repeat it.
	if strings.Contains(body, "projected **€0.74**") {
		t.Error("projection is just the month-to-date, so it projects nothing")
	}
}

// A missing report IS the alert. cost-push.sh treats it that way on the box and
// the surface has to agree, or two things report the same numbers against
// different thresholds.
func TestRefreshSpendTreatsAMissingReportAsTheAlert(t *testing.T) {
	d, rec := liveDaemon(t)
	d.cfg.Cost.ReportPath = filepath.Join(t.TempDir(), "absent.json")

	d.refreshSpend(context.Background())

	body := strings.Join(rec.Bodies(), "")
	if !strings.Contains(body, "no cost report") {
		t.Errorf("a missing report did not raise the alert: %s", body)
	}
	if !strings.Contains(body, "12873820") { // AccentLose
		t.Errorf("a missing report did not read as a failure: %s", body)
	}
}

// A corrupt report is a failure, not a free day.
func TestRefreshSpendTreatsGarbageAsTheAlert(t *testing.T) {
	d, rec := liveDaemon(t)
	d.cfg.Cost.ReportPath = writeCost(t, `{not json at all`)

	d.refreshSpend(context.Background())
	if !strings.Contains(strings.Join(rec.Bodies(), ""), "no cost report") {
		t.Error("unparseable JSON was not treated as a missing report")
	}
}

// RCON refusing covers stopped, starting and wedged alike, and all three mean
// "you cannot join right now", which is what a player needs to know.
func TestRefreshStatusReportsDownWhenRCONRefuses(t *testing.T) {
	d, rec := liveDaemon(t)
	d.cfg.Server.RCONAddr = "127.0.0.1:1" // nothing listens there

	d.refreshStatus(context.Background())

	body := strings.Join(rec.Bodies(), "")
	if !strings.Contains(body, "down") {
		t.Errorf("an unreachable server was not reported as down: %s", body)
	}
	if !strings.Contains(body, "12873820") { // AccentLose
		t.Errorf("a down server did not read as a failure: %s", body)
	}
	// And no invented uptime: a zero time renders as "2026 years ago", which is
	// what the first live post of this board actually said.
	if strings.Contains(body, "years ago") || strings.Contains(body, "<t:-") {
		t.Errorf("invented an uptime it does not know: %s", body)
	}
}

// A cancelled context means shutdown; nothing should still be posting.
func TestRefreshesDoNothingOnceCancelled(t *testing.T) {
	d, rec := liveDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d.refreshStatus(ctx)
	d.refreshSpend(ctx)
	if len(rec.Paths()) != 0 {
		t.Errorf("posted during shutdown: %v", rec.Paths())
	}
}

// The pace exists so a creeper killing four people produces four posts rather
// than a 429 and a gap.
func TestFeedWorkerPostsWhatItIsGiven(t *testing.T) {
	d, rec := liveDaemon(t)
	posts := make(chan discord.Payload, 4)
	posts <- discord.Event(discord.EventData{
		Kind: discord.EventDeath, Player: "kon", Detail: "drowned", WorldDay: 1})
	startFeedWorker(t, d, "feed", posts)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.Paths()) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the feed worker never posted")
}

// An unresolved emoji placeholder would reach players as literal text, so the
// worker must refuse rather than post it.
func TestFeedWorkerRefusesUnresolvedEmoji(t *testing.T) {
	d, rec := liveDaemon(t)
	d.emoji = map[string]string{} // nothing uploaded
	posts := make(chan discord.Payload, 2)
	posts <- discord.Event(discord.EventData{
		Kind: discord.EventDeath, Player: "kon", Detail: "drowned", WorldDay: 1})
	startFeedWorker(t, d, "feed", posts)

	time.Sleep(2 * time.Second)
	for _, p := range rec.Paths() {
		if strings.HasPrefix(p, "POST") {
			t.Fatalf("posted a surface whose emoji do not exist: %v", rec.Paths())
		}
	}
}

// The board refreshes every minute and a pack release happens weekly, so this
// must not be sixty HTTP requests an hour to learn the same string.
func TestPackVersionIsCached(t *testing.T) {
	// Atomic because httptest serves on its own goroutine, so a plain counter
	// here is the same test-only race as the recorder above.
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("name = \"weloveyou\"\nversion = \"0.1.7\"\n" +
			"[index]\nhash = \"abc\"\n[versions]\nminecraft = \"1.21.1\"\n"))
	}))
	defer srv.Close()

	d := testDaemon(t)
	d.cfg.Channels = map[string]struct {
		MCVersion string `toml:"mc_version"`
		PackURL   string `toml:"pack_url"`
	}{"stable": {PackURL: srv.URL}}

	if got := d.packVersion(); got != "0.1.7" {
		t.Fatalf("version = %q", got)
	}
	if got := d.packVersion(); got != "0.1.7" {
		t.Fatalf("cached version = %q", got)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("fetched %d times, want it cached", got)
	}
}

// An unreachable pack site returns the cached value, or empty, and the board
// says unknown rather than inventing one.
func TestPackVersionSurvivesAnUnreachablePackSite(t *testing.T) {
	d := testDaemon(t)
	d.cfg.Channels = map[string]struct {
		MCVersion string `toml:"mc_version"`
		PackURL   string `toml:"pack_url"`
	}{"stable": {PackURL: "http://127.0.0.1:1/pack.toml"}}

	if got := d.packVersion(); got != "" {
		t.Errorf("version = %q, want empty so the board says unknown", got)
	}
}

// wly mounts mc-data read-only at /mc, so this is a read and can never be
// anything else.
func TestStatsPathStaysUnderTheReadOnlyMount(t *testing.T) {
	got := statsPath("6dc57b83-7a60-4b47-baf4-f5ce8c501953")
	if !strings.HasPrefix(got, "/mc/world/stats/") || !strings.HasSuffix(got, ".json") {
		t.Errorf("stats path = %q", got)
	}
}

func TestDialFailsCleanlyWithNothingListening(t *testing.T) {
	d := testDaemon(t)
	d.cfg.Server.RCONAddr = "127.0.0.1:1"
	if _, err := d.dial(); err == nil {
		t.Error("dialled a server that is not there")
	}
}
