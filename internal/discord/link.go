package discord

import (
	"fmt"
	"strings"
	"time"
)

// The link gate: proving a Discord account owns a Minecraft account.
//
// DEPENDS ENTIRELY ON ONLINE_MODE BEING TRUE. Mojang authenticating the session
// is the only thing that makes a join attempt evidence of ownership rather than
// evidence that somebody knows a username. deploy/docker-compose.yml sets
// ONLINE_MODE: "true"; turning it off would silently convert this gate into an
// impersonation vector, because the log line looks identical either way.
//
// And note what the line proves and does not: that an authenticated client
// reached the server, not that a particular human is present. Anyone can
// trigger a join attempt for any username. What cannot be forged is Mojang
// signing off on the session.
//
// The proof is the join attempt itself. `ONLINE_MODE` is true, so Mojang
// authenticates a player BEFORE the server consults its whitelist, and the
// server logs the authenticated name and uuid either way:
//
//	[10:32:42] [User Authenticator #2/INFO]: UUID of player denwa is 6dc57b83-...
//
// Verified on the production server, 2026-08-24. That line is the whole gate.
// Only the owner of an account can make Mojang produce it, so nobody has to be
// whitelisted, given a temporary role, or trusted in advance to be verified.
// They try the door, we see who knocked, and the door stays shut until we say.
//
// The alternative designs and why they lost:
//
//   - A code typed in game needs the player already whitelisted, which is the
//     access the gate exists to grant. Chicken and egg.
//   - A temporary whitelist entry grants real access before any proof, on the
//     word of whoever typed the command.
//   - Checking a Mojang username exists proves nothing at all: names are public.

// LinkState is why a link attempt was refused, or that it was not.
type LinkState int

const (
	LinkOK LinkState = iota
	LinkNoRequest
	LinkExpired
	LinkWrongAccount
	LinkAlreadyClaimed
)

var linkReasons = map[LinkState]string{
	LinkOK:             "linked",
	LinkNoRequest:      "nobody asked to link this account",
	LinkExpired:        "the request expired before they tried to join",
	LinkWrongAccount:   "the name matched but the account behind it did not",
	LinkAlreadyClaimed: "that Minecraft account is already linked to someone else",
}

func (s LinkState) String() string {
	if r, ok := linkReasons[s]; ok {
		return r
	}
	return fmt.Sprintf("linkstate(%d)", int(s))
}

// Request is an open `/link` waiting for its join attempt.
type Request struct {
	DiscordID string
	MCName    string // as typed, for display
	MCUUID    string // resolved from Mojang when the request was made
	ExpiresAt time.Time
}

// Link is a settled association. One Minecraft account belongs to exactly one
// Discord account, which is what stops two people claiming the same player and
// what makes `player`, playtime and deathless mean anything.
type Link struct {
	DiscordID string
	MCUUID    string
	MCName    string
}

// Normalise is how a Minecraft name is compared. Names preserve case but are
// unique without it, so "Denwa" and "denwa" are the same player and a
// case-sensitive compare would refuse the right person.
func Normalise(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Match decides whether an authenticated join attempt completes a request.
//
// It takes the uuid and name as plain strings rather than a log event, so this
// package does not have to know what a Minecraft log looks like. cmd/wly hands
// it whatever internal/mcevents parsed.
func Match(uuid, name string, reqs []Request, existing []Link, now time.Time) (Request, LinkState) {
	uuid = strings.ToLower(strings.TrimSpace(uuid))

	// Already settled beats everything. Re-linking an account that belongs to
	// someone else is the one outcome that would let a person take over another
	// player's history, so it is checked before anything can succeed.
	for _, l := range existing {
		if strings.EqualFold(l.MCUUID, uuid) {
			for _, r := range reqs {
				if r.DiscordID == l.DiscordID && strings.EqualFold(r.MCUUID, uuid) {
					// The same person re-linking themselves is harmless.
					return r, LinkOK
				}
			}
			return Request{}, LinkAlreadyClaimed
		}
	}

	// THE MATCH IS ON UUID, NEVER ON NAME. A Minecraft name can be changed and
	// then claimed by somebody else; the uuid behind it cannot. The name is used
	// only to tell the difference between "nobody asked" and "you asked under a
	// name that now belongs to another account", which is a materially better
	// thing to say to someone staring at a kick screen.
	var expired bool
	for _, r := range reqs {
		if !strings.EqualFold(r.MCUUID, uuid) {
			continue
		}
		if !now.Before(r.ExpiresAt) {
			expired = true
			continue
		}
		return r, LinkOK
	}
	if expired {
		return Request{}, LinkExpired
	}
	for _, r := range reqs {
		if Normalise(r.MCName) == Normalise(name) {
			// Same name, different account: renamed since the request, or the
			// requester never owned it and the real owner just knocked.
			return Request{}, LinkWrongAccount
		}
	}
	return Request{}, LinkNoRequest
}

// CanRequest reports whether a new `/link` may be opened, and why not.
//
// Refusing at request time rather than at join time means the person finds out
// while they are still looking at Discord, instead of after a failed join.
func CanRequest(discordID, uuid string, reqs []Request, existing []Link, now time.Time) LinkState {
	for _, l := range existing {
		if strings.EqualFold(l.MCUUID, uuid) && l.DiscordID != discordID {
			return LinkAlreadyClaimed
		}
	}
	for _, r := range reqs {
		if now.Before(r.ExpiresAt) && strings.EqualFold(r.MCUUID, uuid) &&
			r.DiscordID != discordID {
			// Someone else is mid-flight for this account. Letting a second
			// request open would mean whoever joined first won the account.
			return LinkAlreadyClaimed
		}
	}
	return LinkOK
}

// Actor is whoever triggered an interaction, narrowed to what the gate reads.
//
// Discord puts a `bot` flag on the user object of every interaction, so this is
// information we are handed rather than something to infer.
type Actor struct {
	ID     string
	Bot    bool // an application, not a person
	System bool // Discord's own system account
}

// GateState is why an actor was refused, or that it was not.
type GateState int

const (
	GateOK GateState = iota
	GateIsBot
	GateIsSystem
	GateNoActor
)

var gateReasons = map[GateState]string{
	GateOK:       "allowed",
	GateIsBot:    "applications do not get player access",
	GateIsSystem: "system accounts do not get player access",
	GateNoActor:  "the interaction carried no user",
}

func (s GateState) String() string {
	if r, ok := gateReasons[s]; ok {
		return r
	}
	return fmt.Sprintf("gatestate(%d)", int(s))
}

// MayLink is the bot gate: who is allowed to start a link at all.
//
// An application must never obtain `player`. The role gates the in-game
// channels and is the thing every later grant is built on, so a bot that could
// award it to itself would be inside the server's private half and holding the
// identity of whatever Minecraft account it named. Nothing about the link flow
// requires a bot to use it, so the whole class is refused rather than reasoned
// about case by case.
//
// This is cheap because Discord tells us. It is not a heuristic on the name,
// the avatar or the behaviour, all of which are forgeable; `bot` is set by
// Discord on the interaction payload and an application cannot clear it.
//
// A missing actor is refused too. An interaction with no user is malformed, and
// the safe reading of a malformed request for access is no.
func MayLink(a Actor) GateState {
	switch {
	case strings.TrimSpace(a.ID) == "":
		return GateNoActor
	case a.Bot:
		return GateIsBot
	case a.System:
		return GateIsSystem
	}
	return GateOK
}

// GrantableTo is the same rule applied at the other end: whatever the link
// table says, a managed role is never put on an application.
//
// Two checks rather than one, deliberately. MayLink stops a bot starting a
// link; this stops a role reaching one by any other route, including a row
// written before this gate existed, a hand-edited database, or a future code
// path that forgets to ask. The expensive half of a security check is knowing
// where it was skipped, so it is applied at the point of the grant as well.
func GrantableTo(a Actor) GateState { return MayLink(a) }
