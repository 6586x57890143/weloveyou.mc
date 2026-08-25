package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"weloveyou-mc/internal/bench"
	"weloveyou-mc/internal/discord"
	"weloveyou-mc/internal/logtail"
	"weloveyou-mc/internal/mcevents"
	"weloveyou-mc/internal/rcon"
)

// `wly serve` is the daemon: the log bridge, the status board and the spend
// post, running unattended.
//
// Three loops and no framework. The bridge follows latest.log and posts what
// happens as it happens; the board is edited in place on a ticker; the spend
// post is refreshed from the cost report. They share a Discord client and
// nothing else, so one failing loop cannot take the others down.
//
// The design constraint that shapes all of it: THIS PROCESS MUST SURVIVE WEEKS
// UNATTENDED, on a two-core box, next to the Minecraft server it reports on. So
// every loop reconnects rather than exits, every remote call is bounded, and
// nothing accumulates without a limit.

type serveConfig struct {
	Server struct {
		RCONAddr  string `toml:"rcon_addr"`
		LogPath   string `toml:"log_path"`
		Container string `toml:"container"`
	} `toml:"server"`
	Cost struct {
		ReportPath string `toml:"report_path"`
	} `toml:"cost"`
	Surfaces struct {
		ServerAddress string `toml:"server_address"`
		MapURL        string `toml:"map_url"`
		InstanceZip   string `toml:"instance_zip"`
		PackPage      string `toml:"pack_page"`
		PrismImage    string `toml:"prism_image"`
		MapImage      string `toml:"map_image"`
		FeedStrip     string `toml:"feed_strip"`
	} `toml:"surfaces"`
	Channels map[string]struct {
		MCVersion string `toml:"mc_version"`
		PackURL   string `toml:"pack_url"`
	} `toml:"channels"`
}

type daemon struct {
	cfg     serveConfig
	guild   *discord.Guild
	api     *discordClient
	emoji   map[string]string
	out     io.Writer
	channel map[string]string // surface name -> channel id

	rconPassword string
	budget       float64

	// mu guards everything below, which the bridge and the status loop both
	// touch.
	mu sync.Mutex
	// uuids maps a player name to the uuid Mojang authenticated it with. The
	// join line carries only a name, and first-join detection needs the uuid to
	// look for a stats file, so the Authenticated line that always precedes it
	// is where the pairing comes from.
	uuids map[string]string
	// up and since are the lifecycle, learned from the log rather than guessed
	// from whether RCON answers: a server that is still starting refuses a
	// connection exactly like one that is down.
	up    bool
	since time.Time
	// pack is the published pack version, cached: the board refreshes every
	// minute and a release happens weekly.
	pack   string
	packAt time.Time
}

func runServe(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(out)
	guildPath := fs.String("config", "guild.toml", "the declared server")
	wlyPath := fs.String("wly", "wly.toml", "the daemon config")
	once := fs.Bool("once", false, "refresh the surfaces once and exit, rather than running")
	if err := fs.Parse(args); err != nil {
		return err
	}

	d, err := newDaemon(*guildPath, *wlyPath, out)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		d.refreshStatus(ctx)
		d.refreshSpend(ctx)
		return nil
	}

	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){d.bridge, d.statusLoop, d.spendLoop} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loop(ctx)
		}()
	}
	fmt.Fprintln(out, "wly is watching the server. Ctrl-C to stop.")
	wg.Wait()
	fmt.Fprintln(out, "stopped")
	return nil
}

func newDaemon(guildPath, wlyPath string, out io.Writer) (*daemon, error) {
	g, err := discord.Load(guildPath)
	if err != nil {
		return nil, err
	}
	var cfg serveConfig
	if _, err := toml.DecodeFile(wlyPath, &cfg); err != nil {
		return nil, fmt.Errorf("daemon config: %w", err)
	}

	token := os.Getenv("WLY_DISCORD_TOKEN")
	if token == "" {
		token = fromEnvFile("deploy/.env", "WLY_DISCORD_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("WLY_DISCORD_TOKEN is not set")
	}
	// The RCON password is read from the environment and NEVER from wly.toml,
	// which is committed and mounted read-only into the container. It is also
	// never put in an error string, because error strings reach Discord.
	pw := os.Getenv("WLY_RCON_PASSWORD")
	if pw == "" {
		pw = fromEnvFile("deploy/.env", "RCON_PASSWORD")
	}

	d := &daemon{
		cfg: cfg, guild: g, out: out,
		api:          newDiscordClient(token),
		rconPassword: pw,
		channel:      map[string]string{},
		uuids:        map[string]string{},
		budget:       envFloat("WLY_COST_BUDGET", 5),
	}
	for _, ch := range g.Channels {
		if ch.Surface != "" {
			d.channel[ch.Surface] = ch.ID
		}
	}

	var emojis []struct{ ID, Name string }
	if err := d.api.do("GET", "/guilds/"+g.Meta.ID+"/emojis", nil, &emojis); err != nil {
		return nil, err
	}
	d.emoji = map[string]string{}
	for _, e := range emojis {
		d.emoji[e.Name] = e.ID
	}
	return d, nil
}

func envFloat(key string, def float64) float64 {
	var v float64
	if _, err := fmt.Sscanf(os.Getenv(key), "%f", &v); err != nil {
		return def
	}
	return v
}

// dial opens an RCON connection. Every caller does this per use rather than
// holding one open: a Minecraft restart drops the socket, and a long-lived
// connection that silently died is how a status board freezes on stale numbers
// while looking perfectly healthy.
func (d *daemon) dial() (*rcon.Conn, error) {
	return rcon.Dial(d.cfg.Server.RCONAddr, d.rconPassword, 5*time.Second)
}

// ---------------------------------------------------------------------------
// The bridge
// ---------------------------------------------------------------------------

// bridge follows latest.log and turns it into feed posts.
//
// It restarts itself on any error. The log does not exist while the container is
// being recreated, and a bridge that exits on ENOENT is a bridge that is dead
// after the first deploy.
func (d *daemon) bridge(ctx context.Context) {
	feed := d.channel["events"]
	posts := make(chan discord.Payload, 64)
	go d.feedWorker(ctx, feed, posts)

	t := &logtail.Tailer{Path: d.cfg.Server.LogPath}
	for ctx.Err() == nil {
		err := t.Follow(ctx, func(line string) {
			ev, ok := mcevents.Parse(line)
			if !ok {
				return
			}
			d.handle(ctx, ev, posts)
		})
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(d.out, "bridge: %v, retrying in 5s\n", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *daemon) handle(ctx context.Context, ev mcevents.Event, posts chan<- discord.Payload) {
	switch ev.Kind {
	case mcevents.Authenticated:
		d.mu.Lock()
		d.uuids[ev.Player] = ev.UUID
		d.mu.Unlock()

	case mcevents.ServerReady:
		d.mu.Lock()
		d.up, d.since = true, time.Now()
		d.mu.Unlock()
		d.refreshStatus(ctx)

	case mcevents.ServerStopping:
		d.mu.Lock()
		d.up = false
		d.mu.Unlock()
		d.refreshStatus(ctx)

	case mcevents.Joined:
		// The board shows who is on, so a join changes it immediately rather
		// than up to a minute later.
		d.refreshStatus(ctx)
		if d.firstJoin(ev.Player) {
			send(posts, discord.Event(discord.EventData{
				Kind: discord.EventFirstJoin, Player: ev.Player,
			}))
		}

	case mcevents.Left:
		d.refreshStatus(ctx)

	case mcevents.Died:
		e := discord.EventData{
			Kind: discord.EventDeath, Player: ev.Player, Detail: ev.Detail,
			Strip: d.cfg.Surfaces.FeedStrip,
		}
		// Coordinates come from RCON, because the log line says who and how but
		// never where, and "x 214, z -88" is the difference between a feed post
		// and a story. Best effort on purpose: the player may already have
		// respawned, and none of that is worth losing the post over.
		if c, err := d.dial(); err == nil {
			e.WorldDay, _ = rcon.WorldDay(c)
			e.X, e.Z, e.HasWhere = rcon.Position(c, ev.Player)
			_ = c.Close()
		}
		send(posts, discord.Event(e))

	case mcevents.Advancement:
		e := discord.EventData{
			Kind: discord.EventAdvancement, Player: ev.Player, Detail: ev.Detail,
			Strip: d.cfg.Surfaces.FeedStrip,
		}
		if c, err := d.dial(); err == nil {
			e.WorldDay, _ = rcon.WorldDay(c)
			_ = c.Close()
		}
		send(posts, discord.Event(e))
	}
}

// send never blocks. A full queue drops the event and says so, which is the
// right trade for a feed: falling behind a flood is survivable, and a bridge
// blocked on Discord stops reading the log, which loses everything after it.
func send(posts chan<- discord.Payload, p discord.Payload) {
	select {
	case posts <- p:
	default:
	}
}

// feedWorker posts at a pace Discord accepts.
//
// Five messages per five seconds per channel is the documented ceiling, so this
// paces at one per 1.2 seconds and never bursts. A creeper killing four people
// at once produces four posts over five seconds rather than a 429 and a gap.
func (d *daemon) feedWorker(ctx context.Context, channelID string, posts <-chan discord.Payload) {
	tick := time.NewTicker(1200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-posts:
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			resolved, err := discord.ResolveEmoji(p, d.emoji)
			if err != nil {
				fmt.Fprintf(d.out, "feed: %v\n", err)
				continue
			}
			if err := d.api.do("POST", "/channels/"+channelID+"/messages", resolved, nil); err != nil {
				fmt.Fprintf(d.out, "feed: %v\n", err)
			}
		}
	}
}

// firstJoin reports whether this is the player's first time on the server.
//
// Minecraft answers this itself: it writes world/stats/<uuid>.json the first
// time a player disconnects, so a player with no stats file has never been here
// before. That beats keeping a list, because the server's own files cannot
// disagree with the server.
//
// Unknown uuid means unknown answer, and the honest response is "no": a wrong
// "welcome" to a regular is worse than a missed one to a newcomer.
func (d *daemon) firstJoin(player string) bool {
	d.mu.Lock()
	uuid := d.uuids[player]
	d.mu.Unlock()
	if uuid == "" {
		return false
	}
	_, err := os.Stat(statsPath(uuid))
	return os.IsNotExist(err)
}

// statsPath is where the server keeps a player's own statistics. wly mounts
// mc-data read-only at /mc, so this is a read and can never be anything else.
func statsPath(uuid string) string { return "/mc/world/stats/" + uuid + ".json" }

// ---------------------------------------------------------------------------
// The status board
// ---------------------------------------------------------------------------

func (d *daemon) statusLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	d.refreshStatus(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refreshStatus(ctx)
		}
	}
}

func (d *daemon) refreshStatus(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	d.mu.Lock()
	since := d.since
	d.mu.Unlock()

	data := discord.StatusData{
		MapURL:      d.cfg.Surfaces.MapURL,
		MCVersion:   d.cfg.Channels["stable"].MCVersion,
		PackVersion: d.packVersion(),
		Since:       since,
		NextBackup:  nextBackup(time.Now()),
	}

	c, err := d.dial()
	if err != nil {
		// RCON refusing is the clearest "not serving" signal available. It
		// covers stopped, starting and wedged alike, and all three are "you
		// cannot join right now", which is what a player needs to know.
		data.Up = false
		d.postStatus(data)
		return
	}
	defer func() { _ = c.Close() }()

	data.Up = true
	if lat, err := rcon.ServerThreadLatency(c); err == nil {
		data.Latency = lat
		// A server thread taking more than a quarter second to answer a
		// trivial command is not keeping up, whatever it claims elsewhere.
		data.Degraded = lat > 250*time.Millisecond
	}
	if online, err := rcon.Players(c); err == nil {
		data.Online = online.Players
	}
	if day, err := rcon.WorldDay(c); err == nil {
		data.WorldDay = day
	}
	if data.Since.IsZero() {
		// The daemon started after the server did, so the log's own "Done"
		// line is in the past and its timestamp carries no date. The container
		// knows, and that is what the Docker API read is for.
		if started, err := d.containerStarted(); err == nil {
			data.Since = started
			d.mu.Lock()
			d.since = started
			d.mu.Unlock()
		}
	}
	d.postStatus(data)
}

func (d *daemon) postStatus(data discord.StatusData) {
	p, err := discord.ResolveEmoji(discord.Status(data), d.emoji)
	if err != nil {
		fmt.Fprintf(d.out, "status: %v\n", err)
		return
	}
	if _, err := d.api.upsertSurface(d.channel["status"], p); err != nil {
		fmt.Fprintf(d.out, "status: %v\n", err)
	}
}

// packVersion reads the published pack.toml, which is the same URL the server
// itself fetches on restart. internal/bench already parses that file for the
// benchmark harness, so this is a reuse rather than a second parser that can
// disagree with the first about what version is live.
//
// Cached for an hour: the board refreshes every minute and a pack release
// happens weekly, so fetching it each time would be sixty HTTP requests an hour
// to learn the same string. An unreachable pack site returns the cached value,
// or empty, and the board says unknown rather than inventing one.
func (d *daemon) packVersion() string {
	d.mu.Lock()
	cached, at := d.pack, d.packAt
	d.mu.Unlock()
	if cached != "" && time.Since(at) < time.Hour {
		return cached
	}

	url := d.cfg.Channels["stable"].PackURL
	if url == "" {
		return cached
	}
	p, err := bench.FetchPack(url)
	if err != nil {
		return cached
	}
	d.mu.Lock()
	d.pack, d.packAt = p.Version, time.Now()
	d.mu.Unlock()
	return p.Version
}

// nextBackup is when deploy/backup.sh next runs: nightly, and the timer is the
// authority. Computed rather than read because the timer lives on the host and
// wly is in a container that deliberately cannot see it.
func nextBackup(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// ---------------------------------------------------------------------------
// The spend post
// ---------------------------------------------------------------------------

// costReport is /var/lib/wly/cost.json, written daily on the box by
// cost-report.sh. The amounts are pointers because null and zero are different
// facts: a broken report must never read as a free day.
type costReport struct {
	Generated   time.Time           `json:"generated"`
	Yesterday   *float64            `json:"yesterday"`
	MonthToDate *float64            `json:"month_to_date"`
	Currency    string              `json:"currency"`
	ByService   map[string]*float64 `json:"by_service"`
}

func (d *daemon) spendLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	d.refreshSpend(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refreshSpend(ctx)
		}
	}
}

func (d *daemon) refreshSpend(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	data := discord.SpendData{
		Day:           now,
		Budget:        d.budget,
		Currency:      "EUR",
		CreditsExpire: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC),
	}

	raw, err := os.ReadFile(d.cfg.Cost.ReportPath)
	if err != nil {
		// Missing report IS the alert. cost-push.sh already treats it that way
		// on the box and the surface has to agree, or two things report the
		// same numbers against different thresholds.
		data.ReportedMissed = true
		d.postSpend(data)
		return
	}
	var r costReport
	if err := json.Unmarshal(raw, &r); err != nil {
		data.ReportedMissed = true
		d.postSpend(data)
		return
	}

	data.Yesterday, data.MonthToDate = r.Yesterday, r.MonthToDate
	if r.Currency != "" {
		data.Currency = r.Currency
	}
	if !r.Generated.IsZero() {
		data.ReportAge = now.Sub(r.Generated)
	}
	if r.MonthToDate != nil {
		day := now.Day()
		inMonth := daysInMonth(now)
		avg := *r.MonthToDate / float64(day)
		projected := avg * float64(inMonth)
		data.AverageDaily, data.Projected = avg, &projected
	}
	d.postSpend(data)
}

func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

func (d *daemon) postSpend(data discord.SpendData) {
	p, err := discord.ResolveEmoji(discord.Spend(data), d.emoji)
	if err != nil {
		fmt.Fprintf(d.out, "spend: %v\n", err)
		return
	}
	if _, err := d.api.upsertSurface(d.channel["spend"], p); err != nil {
		fmt.Fprintf(d.out, "spend: %v\n", err)
	}
}
