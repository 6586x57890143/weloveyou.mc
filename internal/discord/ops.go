package discord

import (
	"regexp"
	"strings"
)

// The admin vocabulary for #ops.
//
// This exists because Discord buttons are interactions and nothing handles
// interactions yet: there is no gateway and no interactions endpoint. A button
// in #ops would fail exactly the way the "link my account" button on the welcome
// card failed. Polling the channel for a typed reply needs neither, and it
// reuses the reading wly already does to find its own posts.
//
// Deliberately tiny. An admin should not have to remember a syntax, so anything
// that reads as agreement is accepted and the player name is the only argument.
//
// A REGULAR PLAYER STILL TYPES NOTHING. This is the admin half of the flow, in
// a channel players cannot see.

// opsName is what Mojang permits in a name, and it is the ONLY thing allowed to
// reach the console. The command runs with operator rights, so this is the trust
// boundary rather than a formatting nicety: no spaces, no quotes, no newline, no
// section sign, nothing that could end one command and start another.
var opsName = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// opsVerbs are the ways of saying yes. Order does not matter; the longest match
// is not needed because each is a whole first word.
var opsVerbs = map[string]bool{
	"ok": true, "yes": true, "y": true, "add": true,
	"whitelist": true, "approve": true, "allow": true, "let": true,
}

// ParseOpsCommand reads an admin's message and returns the player to whitelist.
//
// Accepts "ok denwa", "yes denwa", "whitelist denwa", "+denwa", and "let denwa
// in", because an admin typing in a chat window is not filling in a form. Every
// other message in the channel is ignored, which is almost all of them: #ops
// carries the spend post and the health cards too.
func ParseOpsCommand(content string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(strings.ToLower(content)))
	if len(fields) == 0 {
		return "", false
	}

	// "+denwa" with no space, which is how people actually type it.
	if strings.HasPrefix(fields[0], "+") && len(fields[0]) > 1 {
		return validOpsName(strings.TrimPrefix(fields[0], "+"))
	}
	if !opsVerbs[fields[0]] || len(fields) < 2 {
		return "", false
	}
	// An instruction is short. "let denwa in" is three words; anything longer is
	// somebody talking, and "ok so who is this" would otherwise whitelist a
	// player called "so". This also rejects a multi-line message whose first
	// line happens to read as a command: Fields splits on the newline too, so
	// the extra words are still counted.
	if len(fields) > 3 {
		return "", false
	}
	return validOpsName(fields[1])
}

func validOpsName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !opsName.MatchString(s) {
		return "", false
	}
	return s, true
}

// WhitelistResultData is what happened, said back in the channel so the admin
// does not have to go and look.
type WhitelistResultData struct {
	Player string
	Err    string // empty when it worked
	Strip  string
}

// WhitelistResult confirms an approval, or explains why it did not happen.
//
// It always answers. An admin who types "ok denwa" and hears nothing cannot
// tell the difference between a name wly ignored and a server that refused, and
// will type it again.
func WhitelistResult(d WhitelistResultData) Payload {
	if d.Err != "" {
		return Container(AccentLose,
			Text("<:skull:> could not whitelist **%s**\n-# %s", d.Player, d.Err))
	}
	return Container(AccentWin,
		Text("<:heart:> **%s** is on the whitelist\n-# they can join now", d.Player))
}
