// Package mcevents reads what the Minecraft server says about itself: lines
// from latest.log, and the per-player statistics it writes to disk anyway.
//
// The plan named this package for log events alone. It also holds the stats
// reader because that is the same domain, the server's own output, and because
// splitting two hundred lines across two packages to honour a name would be the
// wrong trade. Nothing here is Discord-aware and nothing here does I/O beyond
// reading a file handed to it.
//
// PURE by design: every regex and every conversion is testable against a fixed
// string, which is what lets the log formats be a table rather than a rewrite
// when Minecraft changes them.
package mcevents

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Kind is what happened.
type Kind int

const (
	// Authenticated is Mojang confirming an account before the whitelist is
	// even consulted. It is the whole basis of the link gate: only the owner
	// of an account can produce one, so an attempt to join IS the proof, and
	// nothing has to be granted in advance to collect it.
	Authenticated Kind = iota
	Joined
	Left
	Died
	Advancement
)

var kindNames = map[Kind]string{
	Authenticated: "authenticated", Joined: "joined", Left: "left",
	Died: "died", Advancement: "advancement",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Event is one thing the server reported.
type Event struct {
	Kind   Kind
	Player string
	UUID   string // Authenticated only
	Detail string // the death message, or the advancement title
}

// Verified against a real latest.log on the production server, 2026-08-24:
//
//	[10:32:42] [User Authenticator #2/INFO]: UUID of player denwa is 6dc57b83-...
//
// The thread name carries a counter, so it is matched loosely. The timestamp is
// skipped rather than parsed: the log has no date on it, so anything derived
// from it would be wrong across midnight, and the reader already knows when it
// read the line.
var patterns = []struct {
	kind Kind
	re   *regexp.Regexp
}{
	{Authenticated, regexp.MustCompile(
		`^\[[^\]]+\] \[User Authenticator[^\]]*\]: UUID of player (\S+) is ([0-9a-fA-F-]{36})$`)},
	{Joined, regexp.MustCompile(
		`^\[[^\]]+\] \[[^\]]+\]: (\S+) joined the game$`)},
	{Left, regexp.MustCompile(
		`^\[[^\]]+\] \[[^\]]+\]: (\S+) left the game$`)},
	{Advancement, regexp.MustCompile(
		`^\[[^\]]+\] \[[^\]]+\]: (\S+) has made the advancement \[(.+)\]$`)},
}

// Parse turns one log line into an event. The second return is false for the
// overwhelming majority of lines, which are not events.
func Parse(line string) (Event, bool) {
	line = strings.TrimRight(line, "\r\n")
	for _, p := range patterns {
		m := p.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		e := Event{Kind: p.kind, Player: m[1]}
		switch p.kind {
		case Authenticated:
			e.UUID = strings.ToLower(m[2])
		case Advancement:
			e.Detail = m[2]
		}
		return e, true
	}
	return Event{}, false
}

// Statistics is the slice of a player's stats file that anything here surfaces.
//
// Minecraft writes world/stats/<uuid>.json itself, so there is no pipeline to
// build and nothing to keep in sync: the numbers are the server's own. Verified
// against a real file on 2026-08-24.
type Statistics struct {
	PlayTimeTicks int
	Deaths        int
	MobKills      int
	WalkedCm      int
	Jumps         int
	DamageTaken   int
}

// PlayTime converts the tick counter Minecraft keeps. 20 ticks a second, which
// is the one conversion worth naming rather than open-coding at each call site.
func (s Statistics) PlayTime() time.Duration {
	return time.Duration(s.PlayTimeTicks) * time.Second / 20
}

// Hours is what the linked-role metadata carries, since Discord's integer
// metadata cannot hold a duration.
func (s Statistics) Hours() int { return int(s.PlayTime().Hours()) }

// Deathless reports whether the player has never died. It is a role, and it is
// lost the moment this stops being true.
func (s Statistics) Deathless() bool { return s.Deaths == 0 }

// ReadStats parses one world/stats/<uuid>.json.
//
// A missing key is zero rather than an error: a player who has never jumped has
// no jump key at all, and treating that as a parse failure would make the
// leaderboard fail on exactly the newest player.
func ReadStats(r io.Reader) (Statistics, error) {
	var doc struct {
		Stats struct {
			Custom map[string]int `json:"minecraft:custom"`
		} `json:"stats"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return Statistics{}, fmt.Errorf("stats: %w", err)
	}
	c := doc.Stats.Custom
	return Statistics{
		PlayTimeTicks: c["minecraft:play_time"],
		Deaths:        c["minecraft:deaths"],
		MobKills:      c["minecraft:mob_kills"],
		WalkedCm:      c["minecraft:walk_one_cm"],
		Jumps:         c["minecraft:jump"],
		DamageTaken:   c["minecraft:damage_taken"],
	}, nil
}
