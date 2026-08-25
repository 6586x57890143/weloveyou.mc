package rcon

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The commands wly actually issues, each next to the parser for its reply.
//
// Kept adjacent on purpose: the command string and the shape of its answer are
// the pair most likely to drift when Minecraft changes, and splitting them puts
// the regex somewhere nobody looks when the command is edited.
//
// EVERY FORMAT HERE WAS READ OFF THE LIVE SERVER on 2026-08-25, not recalled:
//
//	list             -> "There are 0 of a max of 20 players online: "
//	time query day   -> "The time is 484"
//
// Note the trailing space on an empty `list`. A parser that splits on ", " and
// does not trim reports one player named "".

var (
	reList = regexp.MustCompile(`^There are (\d+) of a max of (\d+) players online:\s*(.*)$`)
	reDay  = regexp.MustCompile(`^The time is (\d+)$`)
	rePos  = regexp.MustCompile(`\[([-\d.]+)d, ([-\d.]+)d, ([-\d.]+)d\]`)
)

// Online is who is on the server.
type Online struct {
	Count   int
	Max     int
	Players []string
}

// Players asks who is connected.
func Players(c *Conn) (Online, error) {
	out, err := c.Exec("list")
	if err != nil {
		return Online{}, err
	}
	m := reList.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return Online{}, fmt.Errorf("rcon: could not read the player list from %q", out)
	}
	o := Online{}
	o.Count, _ = strconv.Atoi(m[1])
	o.Max, _ = strconv.Atoi(m[2])
	for _, name := range strings.Split(m[3], ",") {
		if name = strings.TrimSpace(name); name != "" {
			o.Players = append(o.Players, name)
		}
	}
	// The count is the server's, not len(Players): a name containing a comma is
	// impossible in Minecraft, but trusting the server's own number costs
	// nothing and cannot disagree with what the game thinks.
	return o, nil
}

// WorldDay is the in-game day number, which is the only clock players actually
// share and the one every surface stamps things with.
func WorldDay(c *Conn) (int, error) {
	out, err := c.Exec("time query day")
	if err != nil {
		return 0, err
	}
	m := reDay.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return 0, fmt.Errorf("rcon: could not read the world day from %q", out)
	}
	return strconv.Atoi(m[1])
}

// Position reads where a player is.
//
// Used on a death, because the log line says who died and how but never where,
// and "x 214, z -88" is the difference between a feed post and a story. It is
// deliberately best-effort: the player may have already respawned, disconnected
// or never existed, and none of those is worth failing a feed post over.
func Position(c *Conn, player string) (x, z int, ok bool) {
	out, err := c.Exec("data get entity " + player + " Pos")
	if err != nil {
		return 0, 0, false
	}
	m := rePos.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	fx, err1 := strconv.ParseFloat(m[1], 64)
	fz, err2 := strconv.ParseFloat(m[3], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int(fx), int(fz), true
}

// ServerThreadLatency measures how long the server takes to answer.
//
// This exists because for weeks there was NO TPS SOURCE ON THIS SERVER at all:
// spark was pinned in the bench harness and absent from pack/stable, and Fabric
// has no vanilla tick command. Rather than invent the numbers the board was
// designed around, wly measured what it could actually see.
//
// spark ships from pack v0.1.8 and the board prefers its figures now. This stays
// because it is the fallback whenever they are missing or stale, and because it
// measures something spark does not: whether the server answers AT ALL.
//
// It is a real signal and not a substitute dressed up as one: RCON commands are
// executed ON THE SERVER THREAD, so a server that cannot keep up cannot answer
// promptly either. It moves for the same reason MSPT moves. What it is not is
// MSPT, and it is labelled as response time everywhere it is shown, because a
// number presented as something it is not is the failure this whole project
// keeps writing rules about.
func ServerThreadLatency(c *Conn) (time.Duration, error) {
	start := time.Now()
	// `list` rather than an empty command: it is cheap, it runs on the server
	// thread, and an empty command is answered by the RCON thread alone, which
	// would measure the network and nothing else.
	if _, err := c.Exec("list"); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// Save flushes the world. Used before anything that risks the container, and
// paired with SaveOn by a caller that must not leave saving disabled.
func Save(c *Conn) error {
	_, err := c.Exec("save-all flush")
	return err
}

// Say puts a line in game chat, which is the other half of the bridge: Discord
// messages reaching players who are not looking at Discord.
//
// The message is sanitised rather than trusted. It arrives from Discord, so it
// is attacker-controlled in the ordinary case of a stranger in a public channel,
// and an unescaped quote or newline here is command injection into a console
// that has operator rights.
func Say(c *Conn, who, message string) error {
	_, err := c.Exec("say " + SanitiseChat(who) + ": " + SanitiseChat(message))
	return err
}

// SanitiseChat strips everything that could change the meaning of a console
// command or the rendering of a chat line.
//
// Newlines end a command, so one embedded in a Discord message would let the
// sender run a second command as the console. The section sign is Minecraft's
// colour escape and lets a message impersonate server output or another player.
// Quotes and backslashes are dropped because they are how JSON text components
// get out of their own string.
func SanitiseChat(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == 0:
			b.WriteByte(' ')
		case r == '§' || r == '"' || r == '\\':
			continue
		case r < 0x20:
			continue
		default:
			b.WriteRune(r)
		}
	}
	// Length-capped so one Discord message cannot flood the game chat.
	out := strings.TrimSpace(b.String())
	if len(out) > 256 {
		out = out[:256]
	}
	return out
}
