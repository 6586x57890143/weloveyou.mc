package discord

import (
	"fmt"
	"strings"
	"time"
)

// The six surfaces, each built from a Data struct and nothing else.
//
// The Data structs are the anti-invention gate. A surface that shows a figure
// nothing can produce is the same failure as a JVM flag the JVM accepts and then
// ignores, so every number here has to be handed in by a caller that got it from
// somewhere real. docs/DISCORD.md carries the table of where each one comes from.
//
// The voice rules apply to every string below and are not stylistic garnish:
// no em dashes, no oxford comma, lowercase and warm, and nothing that sells the
// server. Someone reading the get-started card has already decided.

// GetStartedData is everything the welcome card says.
type GetStartedData struct {
	MinecraftVersion string
	InstanceZipURL   string
	PackPageURL      string
	ServerAddress    string
	PrismImageURL    string
}

// GetStarted is direction B, chosen 2026-08-24: a Section per step, each with
// its own accessory.
//
// A regular player types nothing, and step 3 is where that is actually earned:
// the join attempt itself is the proof, so there is no name to give and no
// button to press. A server whose onboarding is "type this exactly" loses people
// at the point it can least afford to.
func GetStarted(d GetStartedData) Payload {
	return Container(AccentHeart,
		Text("# wly <:heart:>\n-# minecraft %s · whitelist only", d.MinecraftVersion),
		Separator(true, 1),
		Section(LinkButton("prism launcher", "https://prismlauncher.org/download/"),
			Text("### 1. install prism launcher\nany launcher will run the pack. only prism keeps it up to date for you.")),
		// The label says to COPY rather than naming the file, because this
		// button is a link to a .zip: clicking it downloads the thing, and the
		// step needs the URL pasted into Prism instead. "the .zip" invited
		// exactly the wrong action.
		Section(LinkButton("right click, copy link", d.InstanceZipURL),
			Text("### 2. import the instance\n`Add Instance` → `Import from zip` → paste the link.")),
		Gallery(d.PrismImageURL,
			"Prism Launcher's Add Instance window, Import from zip selected, with the link field outlined."),
		// NO BUTTON HERE, and that is a correction rather than a simplification.
		//
		// This step used to be a primary button opening a modal. Nothing handles
		// interactions: there is no gateway and no interactions endpoint, so
		// every player who pressed it got "This interaction failed". guild.toml
		// states the rule it broke: a button that does nothing when pressed is
		// worse than an absent one, because a player has already committed to
		// it. It was live in #start-here for exactly that long.
		//
		// The flow needs no interaction anyway, which is what makes this the
		// right shape rather than a stopgap. Mojang authenticates a join attempt
		// BEFORE the whitelist is consulted, so the attempt itself proves who
		// they are and wly reads it off the log. A player still types nothing.
		Text("### 3. just try to join\n```\n%s\n```\nyou will be turned away the first time, that is expected. wly sees the attempt and asks an admin to let you in, so there is nothing to fill in and nothing to send.", d.ServerAddress),
		Separator(true, 1),
		// The pre-launch disclosure is non-negotiable. Importing a Prism
		// instance runs its pre-launch command with no warning from Prism, and
		// that is how the auto-update works, so the card says so out loud.
		Text("-# **worth knowing:** importing this instance lets prism run a small updater before every launch. that is what keeps your mods in sync, so a pack update never asks anything of you. the updater ships inside the zip and we check it on the way in, nothing gets downloaded fresh at launch. if you would rather skip all that, grab the .mrpack from the pack page and update by hand."),
	)
}

// StatusData is the board. Down is not an error: a server that is off still has
// a status, and saying so is the whole job.
type StatusData struct {
	Up          bool
	Degraded    bool
	Since       time.Time
	Online      []string
	MapURL      string
	WorldDay    int
	PackVersion string
	MCVersion   string
	NextBackup  time.Time

	// Strip is the heart divider, which is also what makes every card the same
	// width. See divider().
	Strip string

	// TPS and MSPTp95 are only real when HasTick is true, which means something
	// on the server actually answered for tick health.
	//
	// spark ships in pack/stable from v0.1.8 and these are live. They were NOT
	// for the first weeks of this board: `spark tps` answered "Unknown or
	// incomplete command", Fabric has no vanilla equivalent, and rather than
	// print a confident 20.0 that nothing measured the board showed the RCON
	// round trip instead. HasTick is what made that possible and it stays, both
	// because spark can be removed again and because the numbers go stale the
	// moment the bridge stops reading the log.
	TPS      float64
	MSPTp95  float64
	HasTick  bool
	TickFrom string // "spark", say. Named on the board so the number has a source.

	// Latency is the RCON round trip, which is always available because RCON
	// commands run ON THE SERVER THREAD. It is not MSPT and is never labelled
	// as it, but it moves for the same reason MSPT moves, so it is the honest
	// thing to show when nothing better exists.
	Latency time.Duration
}

// Status is the one message that is edited in place forever and never reposted.
//
// The accent is the state: info healthy, base degraded, lose down. Nothing else
// in the card has to shout, because the colour already did.
func Status(d StatusData) Payload {
	accent := AccentInfo
	switch {
	case !d.Up:
		accent = AccentLose
	case d.Degraded:
		accent = AccentBase
	}

	// A zero time is "we do not know", not the year 1. Rel() on one renders as
	// "2026 years ago", which is what the first live post of this board actually
	// said. Uptime is genuinely unknown when the daemon starts after the server
	// and DOCKER_HOST is unset, and saying nothing is the honest version of that.
	when := "edited in place, never reposted"
	if !d.Since.IsZero() {
		when = "up " + Rel(d.Since) + " · edited in place, never reposted"
	}
	head := Text("# wly <:heart:>\n-# %s", when)
	if !d.Up {
		down := "down"
		if !d.Since.IsZero() {
			down = "down since " + Rel(d.Since)
		}
		head = Text("# wly <:heart:>\n-# %s", down)
	}

	who := "nobody right now"
	if n := len(d.Online); n > 0 {
		who = fmt.Sprintf("**%d** of us right now\n%s", n, strings.Join(d.Online, ", "))
	}

	// The world line says what it can actually measure. With a tick source it
	// is TPS and p95; without one it is the server's own response time, named
	// as response time. Printing an unmeasured 20.0 TPS would be exactly the
	// "flag the JVM accepts and then ignores" failure, in a place every player
	// reads.
	world := fmt.Sprintf("### the world\nday **%d** · **%dms** server response",
		d.WorldDay, d.Latency.Milliseconds())
	if d.HasTick {
		world = fmt.Sprintf("### the world\nday **%d** · **%.2f** TPS · **%.1fms** p95 tick",
			d.WorldDay, d.TPS, d.MSPTp95)
	}

	return Container(accent,
		head,
		divider(d.Strip),
		Section(LinkButton("open the map", d.MapURL), Text("### online\n%s", who)),
		Text("%s", world),
		// An unknown pack version renders as "**** · minecraft 1.21.1", where the
		// empty bold collapses into a stray glyph. Say unknown instead: the
		// first live post of this board showed the glyph and it read as
		// corruption rather than as a missing value.
		Text("### the pack\n%s · minecraft %s", boldOrUnknown(VersionLabel(d.PackVersion)), d.MCVersion),
		Separator(false, 2),
		Text("-# next backup %s. numbers refresh every minute.", Rel(d.NextBackup)),
	)
}

// divider is the rule between blocks.
//
// With a strip configured it IS the heart strip, which does two jobs at once: it
// draws the rule, and because a Media Gallery image is the only Components V2
// element with an intrinsic width, it makes every card carrying it the same
// width. A Container otherwise takes the width of its widest child, so cards
// sharing no image render ragged next to each other.
//
// Without one it falls back to a plain Separator, so a checkout with no strip
// published still renders correctly rather than losing its dividers.
func divider(strip string) Component {
	if strip == "" {
		return Separator(true, 1)
	}
	return Gallery(strip, "")
}

// VersionLabel is how a pack version is written wherever a player sees it.
//
// pack.toml carries a bare "0.1.8" while the tag, the docs and every human say
// "v0.1.8", and the design payloads were drawn with the v. Normalising in one
// place stops two surfaces disagreeing about the same release.
//
// Exported because the release check matches on the same string. Search for one
// form and print the other, and the bot reannounces the same version every ten
// minutes for ever.
func VersionLabel(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// boldOrUnknown bolds a value, or says unknown when there is none. Empty bold
// markers collapse into a stray glyph in Discord rather than into nothing.
func boldOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return "**" + s + "**"
}

// SpendData is /var/lib/wly/cost.json, plus the budget it is measured against.
//
// The amounts are pointers because null and zero are different facts and
// conflating them is how a broken report reads as a free day. cost-push.sh
// already makes that distinction on the box; this keeps it.
type SpendData struct {
	Day            time.Time
	Yesterday      *float64
	MonthToDate    *float64
	Projected      *float64
	Budget         float64
	Currency       string
	ReportAge      time.Duration
	CreditsExpire  time.Time
	AverageDaily   float64
	ReportedMissed bool // no report at all, or the file could not be read
	Strip          string
}

// Spend is the sixth surface, which the master plan omitted and CLAUDE.md
// requires. The escalation matches cost-push.sh exactly, because two things
// reporting the same numbers with different thresholds is worse than one.
func Spend(d SpendData) Payload {
	accent := AccentDim
	switch {
	case d.ReportedMissed || d.ReportAge > 36*time.Hour || d.Yesterday == nil:
		accent = AccentLose
	case d.Projected != nil && *d.Projected > d.Budget:
		accent = AccentBase
	case d.Yesterday != nil && d.AverageDaily > 0 && *d.Yesterday > 2*d.AverageDaily:
		accent = AccentBase
	}

	body := fmt.Sprintf("yesterday **%s**\nmonth to date **%s**\nprojected **%s** of %s",
		money(d.Yesterday, d.Currency), money(d.MonthToDate, d.Currency),
		money(d.Projected, d.Currency), amount(d.Budget, d.Currency))
	if d.ReportedMissed {
		body = "no cost report on the box at all. that is the alert, not the spend."
	}

	return Container(accent,
		Text("# spend <:coin:>\n-# for %s", Date(d.Day)),
		divider(d.Strip),
		Text("%s", body),
		Separator(false, 2),
		Text("-# credits expire %s. a null amount is reported as unknown, never as zero.",
			Date(d.CreditsExpire)),
	)
}

// money renders an amount that may not exist. A missing one is "unknown", and
// never a zero, because a zero is a claim that nothing was spent.
func money(v *float64, currency string) string {
	if v == nil {
		return "unknown"
	}
	return amount(*v, currency)
}

func amount(v float64, currency string) string {
	sym := map[string]string{"EUR": "€", "USD": "$", "GBP": "£"}[currency]
	if sym == "" {
		sym = currency + " " // an unknown currency is named, never assumed
	}
	// Three decimals below 0.10, two above. Daily spend on this box is around
	// €0.03, and at two decimals a real cost slides towards €0.00 and reads as
	// free, which is the one thing this surface exists to prevent.
	if v < 0.1 {
		return fmt.Sprintf("%s%.3f", sym, v)
	}
	return fmt.Sprintf("%s%.2f", sym, v)
}

// ReleaseData is a pack release. Added, Updated and Removed are already-worded
// lines rather than structured diffs: what changed in a pack is a human
// sentence, and inventing one from a file list produces the mod-list flavour the
// voice rules forbid.
type ReleaseData struct {
	Version     string
	MCVersion   string
	ModCount    int
	Added       []string
	Updated     []string
	Removed     []string
	PackPageURL string
	Strip       string
}

// Release is posted fresh each time rather than edited in place: a release is an
// event, and editing the previous one would erase that it happened.
//
// The notes block is OMITTED ENTIRELY when nothing was written for it, rather
// than rendered as three headings saying "nothing". A pack published without
// notes is the ordinary case for an automated post, and "removed: nothing" is a
// claim that somebody checked. Nobody did.
func Release(d ReleaseData) Payload {
	parts := []Component{
		Text("# pack %s <:heart:>\n-# minecraft %s · %d mods",
			VersionLabel(d.Version), d.MCVersion, d.ModCount),
		divider(d.Strip),
	}
	if notes := releaseNotes(d); notes != "" {
		parts = append(parts, Text("%s", notes), divider(d.Strip))
	}
	parts = append(parts, Section(LinkButton("pack page", d.PackPageURL),
		Text("### what you need to do\nnothing. it lands on your next launch.")))
	return Container(AccentBase, parts...)
}

// releaseNotes renders the three headings, or "" when there is nothing to say.
func releaseNotes(d ReleaseData) string {
	if len(d.Added) == 0 && len(d.Updated) == 0 && len(d.Removed) == 0 {
		return ""
	}
	return strings.Join([]string{
		block("added", d.Added),
		block("updated", d.Updated),
		block("removed", d.Removed),
	}, "\n\n")
}

// block renders one heading of the release notes. An empty list says "nothing"
// rather than disappearing, so a release that removed nothing looks different
// from one where nobody checked.
func block(heading string, lines []string) string {
	if len(lines) == 0 {
		return "**" + heading + "**\nnothing"
	}
	return "**" + heading + "**\n" + strings.Join(lines, "\n")
}

// MapData is the ambient card in the map channel. Info accent, never urgent.
type MapData struct {
	WorldDay int
	Rendered time.Time
	ImageURL string
	MapURL   string
}

func Map(d MapData) Payload {
	return Container(AccentInfo,
		Text("# the world <:map:>\n-# day %d · rendered %s", d.WorldDay, Rel(d.Rendered)),
		Gallery(d.ImageURL, "A crop of the live squaremap render around spawn."),
		Section(LinkButton("the map", d.MapURL),
			Text("### open it\npan, zoom, and watch everyone move.")),
	)
}

// Discord gives no way to make these cards the same width, and three were tried.
//
// A Components V2 Container has no width property: it takes the width of its
// widest child, capped at the message column. Measured in Discord's own message
// font on 2026-08-25, the event lines are 152px, 171px and 184px, so the text
// will never agree with itself. What was tried against the live guild:
//
//	a run of U+2007 FIGURE SPACE  -> STRIPPED. Discord removes it from message
//	                                content entirely; the posted cards contained
//	                                no figure space at all when read back
//	a Separator on every card     -> no effect. A separator has no intrinsic
//	                                width, so it cannot set one
//	a Section with an accessory   -> no effect. The button sits after the text
//	                                rather than pinning to a fixed column
//
// The only component with an intrinsic width is a Media Gallery, because an
// image has real dimensions. That is why the get-started card is uniformly wide:
// it embeds a 732px screenshot. Forcing it here would mean a fixed-width image
// fetched on every feed post, which is a network round trip per death and a
// visible strip whenever it fails to load. Not worth it for a chat feed, where
// content-sized cards read as normal.
//
// EventKind is what happened. The accent and the icon both come from it, so a
// death and an advancement cannot end up looking the same.
type EventKind int

const (
	EventDeath EventKind = iota
	EventAdvancement
	EventFirstJoin
)

// EventData is one line in the feed. HasWhere is separate from X and Z because
// 0,0 is a real place: a death at spawn must not be indistinguishable from a
// death whose coordinates nobody captured.
type EventData struct {
	Kind     EventKind
	Player   string
	Detail   string // "fell from a high place", "The End?", ""
	WorldDay int
	X, Z     int
	HasWhere bool

	// Strip is a fixed-size image placed at the top of the card, and it is the
	// only thing that makes every card the same width. See the note above Event.
	// Empty means no strip, and the cards size to their text.
	Strip string
}

// Event is a single feed post. These are not edited in place: the feed is a log,
// and a log that rewrites itself is not one.
//
// Each kind gets its own icon and accent so a death and an advancement can never
// be mistaken for each other at a glance, which is the only way a feed scrolling
// past actually gets read.
func Event(d EventData) Payload {
	accent, icon := AccentLose, "skull"
	line := fmt.Sprintf("**%s** %s", d.Player, d.Detail)
	foot := fmt.Sprintf("day %d", d.WorldDay)

	switch d.Kind {
	case EventAdvancement:
		accent, icon = AccentWin, "world"
		line = fmt.Sprintf("**%s** earned **%s**", d.Player, d.Detail)
	case EventFirstJoin:
		accent, icon = AccentHeart, "heart"
		line = fmt.Sprintf("**%s** joined for the first time", d.Player)
		// A first join is the one event that asks something of the room rather
		// than reporting a fact, so it does not get a day stamp.
		foot = "say hi"
	case EventDeath:
		if d.HasWhere {
			foot = fmt.Sprintf("day %d · x %d, z %d", d.WorldDay, d.X, d.Z)
		}
	}
	card := []Component{Text("<:%s:> %s\n-# %s", icon, line, foot)}
	if d.Strip != "" {
		card = append([]Component{Gallery(d.Strip, "")}, card...)
	}
	return Container(accent, card...)
}

// JoinRequestData is somebody who tried to get in and could not.
type JoinRequestData struct {
	Player string
	UUID   string
	Strip  string
}

// JoinRequest is how a newcomer reaches an admin without typing anything.
//
// It replaces the modal that never existed. Mojang authenticates a join attempt
// BEFORE the whitelist is consulted, so a rejected attempt still proves who the
// player is, and the log line carries the uuid. That is the whole registration
// flow: they try, wly sees it, an admin says yes.
//
// The command is spelled out in full because the admin reading this is on a
// phone half the time, and a card that says "whitelist them" and leaves the
// exact syntax as an exercise is a card that gets ignored until later.
func JoinRequest(d JoinRequestData) Payload {
	return Container(AccentBase,
		Text("<:player:> **%s** tried to join and is not on the whitelist\n-# %s",
			d.Player, d.UUID),
		divider(d.Strip),
		Text("```\nwhitelist add %s\n```\n-# mojang already proved this is them, "+
			"so the name is theirs. run it in game or over rcon.", d.Player),
	)
}
