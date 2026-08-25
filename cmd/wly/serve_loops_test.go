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
	if !strings.Contains(body, "no spend report") {
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
	if !strings.Contains(strings.Join(rec.Bodies(), ""), "no spend report") {
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

func writeWhitelist(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "whitelist.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	old := whitelistPath
	whitelistPath = p
	t.Cleanup(func() { whitelistPath = old })
}

// The real file, as the live server writes it.
func TestWhitelistedReadsTheServersOwnFile(t *testing.T) {
	writeWhitelist(t, `[{"name":"denwa","uuid":"6dc57b83-7a60-4b47-baf4-f5ce8c501953"}]`)

	if listed, ok := whitelisted("denwa"); !ok || !listed {
		t.Errorf("denwa = %v %v, want listed", listed, ok)
	}
	// Minecraft names are case-insensitive for this purpose.
	if listed, ok := whitelisted("DENWA"); !ok || !listed {
		t.Errorf("case-different name was not matched")
	}
	if listed, ok := whitelisted("stranger"); !ok || listed {
		t.Errorf("stranger = %v %v, want not listed but readable", listed, ok)
	}
}

// Unreadable is NOT the same as not-listed, and conflating them posts every
// single join to #ops and trains everyone to ignore the channel.
func TestUnreadableWhitelistIsNotTakenAsEmpty(t *testing.T) {
	writeWhitelist(t, `{ this is not a whitelist`)
	if _, ok := whitelisted("denwa"); ok {
		t.Error("garbage parsed as a readable whitelist")
	}
	old := whitelistPath
	whitelistPath = filepath.Join(t.TempDir(), "absent.json")
	t.Cleanup(func() { whitelistPath = old })
	if _, ok := whitelisted("denwa"); ok {
		t.Error("a missing file parsed as a readable whitelist")
	}
}

// A stranger reaches an admin; a regular does not; and neither has to type
// anything.
func TestAskToLetThemIn(t *testing.T) {
	writeWhitelist(t, `[{"name":"denwa","uuid":"6dc57b83"}]`)
	d, rec := liveDaemon(t)

	d.askToLetThemIn("denwa", "6dc57b83") // already on the list
	if len(rec.Paths()) != 0 {
		t.Errorf("posted about a whitelisted player: %v", rec.Paths())
	}

	d.askToLetThemIn("stranger", "abc-123")
	if len(rec.Paths()) != 1 {
		t.Fatalf("a stranger did not reach ops: %v", rec.Paths())
	}
	body := strings.Join(rec.Bodies(), "")
	for _, want := range []string{"stranger", "abc-123", "whitelist add stranger"} {
		if !strings.Contains(body, want) {
			t.Errorf("the ops card omits %q: %s", want, body)
		}
	}

	// A client retrying every few seconds must not fill the channel.
	d.askToLetThemIn("stranger", "abc-123")
	d.askToLetThemIn("stranger", "abc-123")
	if len(rec.Paths()) != 1 {
		t.Errorf("asked %d times about one player", len(rec.Paths()))
	}
}

func TestAskToLetThemInNeedsBothNameAndUUID(t *testing.T) {
	writeWhitelist(t, `[]`)
	d, rec := liveDaemon(t)
	d.askToLetThemIn("", "abc")
	d.askToLetThemIn("someone", "")
	if len(rec.Paths()) != 0 {
		t.Errorf("posted on incomplete input: %v", rec.Paths())
	}
}

// The channel is admin-only and that is Discord's enforcement. Checking the role
// as well is defence in depth: a permission edit or an integration must not
// silently become a route to an operator command.
func TestOnlyAdminsCanApprove(t *testing.T) {
	d := testDaemon(t)
	d.adminRole = "admin-role-id"

	if !d.isAdmin([]string{"other", "admin-role-id"}) {
		t.Error("an admin was refused")
	}
	if d.isAdmin([]string{"player", "supporter"}) {
		t.Error("a non-admin was accepted")
	}
	// Unknown means no, never "probably fine".
	d.adminRole = ""
	if d.isAdmin([]string{"anything"}) {
		t.Error("approvals were accepted with no admin role resolved")
	}
}

// A restart must not replay every approval ever typed and re-run each one, the
// same rule the log tailer follows by starting at the end.
func TestOpsLoopStartsFromNow(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		_, _ = w.Write([]byte(`[{"id":"999","content":"ok denwa","author":{"id":"a"}}]`))
	}))
	defer srv.Close()
	old := discordAPI
	discordAPI = srv.URL
	defer func() { discordAPI = old }()

	d := testDaemon(t)
	d.api = newDiscordClient("t")
	got, err := d.newestMessage("ops")
	if err != nil || got != "999" {
		t.Fatalf("newestMessage = %q, %v", got, err)
	}
	if len(seen) == 0 || !strings.Contains(seen[0], "limit=1") {
		t.Errorf("did not ask for just the newest: %v", seen)
	}
}

// Every message in #ops that is not a command must be left alone, and the bot's
// own cards must never be read as instructions.
func TestCheckOpsIgnoresItsOwnPostsAndChatter(t *testing.T) {
	body := `[
	  {"id":"3","content":"ok denwa","author":{"id":"bot","bot":true},"member":{"roles":["admin-role-id"]}},
	  {"id":"2","content":"looks fine","author":{"id":"human"},"member":{"roles":["admin-role-id"]}},
	  {"id":"1","content":"ok denwa","author":{"id":"stranger"},"member":{"roles":["player"]}}
	]`
	var writes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes = append(writes, r.Method+" "+r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	old := discordAPI
	discordAPI = srv.URL
	defer func() { discordAPI = old }()

	d := testDaemon(t)
	d.api = newDiscordClient("t")
	d.adminRole = "admin-role-id"
	d.cfg.Server.RCONAddr = "127.0.0.1:1" // nothing listens; no command can run

	d.checkOps(context.Background(), "ops")

	// The bot's own message is skipped, the chatter is not a command, and the
	// stranger is not an admin, so nothing should have been acted on.
	for _, w := range writes {
		if strings.HasPrefix(w, "POST") {
			t.Errorf("acted on a message it should have ignored: %v", writes)
		}
	}
}

// An admin who types "ok denwa" and hears nothing cannot tell a name wly
// ignored from a server that refused, and will type it again. So it answers
// even when the command could not run.
func TestWhitelistAnswersWhenTheServerIsUnreachable(t *testing.T) {
	d, rec := liveDaemon(t)
	d.cfg.Server.RCONAddr = "127.0.0.1:1" // nothing listening

	d.whitelist("ops", "denwa", "kon")

	if len(rec.Paths()) != 1 {
		t.Fatalf("said nothing back: %v", rec.Paths())
	}
	body := strings.Join(rec.Bodies(), "")
	if !strings.Contains(body, "denwa") {
		t.Errorf("the reply does not name the player: %s", body)
	}
	if !strings.Contains(body, "not answering") {
		t.Errorf("the reply does not say why it failed: %s", body)
	}
	if !strings.Contains(body, "12873820") { // AccentLose
		t.Errorf("a failure did not read as one: %s", body)
	}
}

// A real approval from a real admin reaches the whitelist path.
func TestCheckOpsActsOnAnAdminApproval(t *testing.T) {
	d, rec := liveDaemon(t)
	d.adminRole = "admin-role-id"
	d.cfg.Server.RCONAddr = "127.0.0.1:1"

	// surfaceServer answers every GET with "[]", so feed it the message list
	// through the same fake by pointing checkOps at a channel whose GET returns
	// one admin approval.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`[{"id":"5","content":"ok denwa",
				"author":{"id":"kon","username":"kon"},
				"member":{"roles":["admin-role-id"]}}]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"new"}`))
	}))
	defer srv.Close()
	old := discordAPI
	discordAPI = srv.URL
	defer func() { discordAPI = old }()
	d.api = newDiscordClient("t")

	d.checkOps(context.Background(), "ops")

	// It tried, and it said so. RCON is unreachable here, so the answer is a
	// failure card rather than a success, which is the honest outcome.
	if d.opsSeen != "5" {
		t.Errorf("did not advance past the message it handled: %q", d.opsSeen)
	}
	_ = rec
}

// Every loop must stop when the context is cancelled, or a deploy waits on a
// container that will not exit and docker kills it mid-write.
func TestEveryLoopStopsWhenAsked(t *testing.T) {
	d, _ := liveDaemon(t)
	d.cfg.Server.RCONAddr = "127.0.0.1:1"
	d.cfg.Cost.ReportPath = filepath.Join(t.TempDir(), "absent.json")
	d.cfg.Server.LogPath = filepath.Join(t.TempDir(), "absent.log")

	for name, loop := range map[string]func(context.Context){
		"bridge":  d.bridge,
		"status":  d.statusLoop,
		"spend":   d.spendLoop,
		"release": d.releaseLoop,
		"ops":     d.opsLoop,
	} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			loop(ctx)
		}()
		time.Sleep(30 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Errorf("%s did not stop when cancelled", name)
		}
	}
}
