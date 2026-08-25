package discord

import (
	"strings"
	"testing"
)

// An admin typing in a chat window is not filling in a form.
func TestParseOpsCommand(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ok denwa", "denwa"},
		{"OK Denwa", "denwa"},
		{"yes denwa", "denwa"},
		{"y denwa", "denwa"},
		{"add denwa", "denwa"},
		{"whitelist denwa", "denwa"},
		{"approve denwa", "denwa"},
		{"+denwa", "denwa"},
		{"  ok   denwa  ", "denwa"},
		{"let denwa in", "denwa"},
	} {
		got, ok := ParseOpsCommand(tc.in)
		if !ok || got != tc.want {
			t.Errorf("ParseOpsCommand(%q) = %q,%v want %q", tc.in, got, ok, tc.want)
		}
	}
}

// #ops carries the spend post and the health cards. Almost every message in it
// is not a command and must be left alone.
func TestParseOpsCommandIgnoresOrdinaryChat(t *testing.T) {
	for _, in := range []string{
		"", "   ", "ok", "yes", "+",
		"looks fine to me",
		"the spend is up again",
		"ok so who is this",     // talking, not instructing
		"denwa",                 // a bare name is not an instruction
		"okdenwa",               // no separator, not a verb
		"yes i think we should", // long enough to be a sentence
	} {
		if got, ok := ParseOpsCommand(in); ok {
			t.Errorf("ParseOpsCommand(%q) = %q, want it ignored", in, got)
		}
	}
}

// The command runs with operator rights, so the name is the trust boundary.
//
// The guarantee is not "hostile input is rejected", it is "whatever comes back
// is a Mojang name and nothing else". A newline ends one console command and
// starts another, so what matters is that the newline can never be inside the
// string handed to RCON.
func TestParseOpsCommandOnlyEverReturnsAName(t *testing.T) {
	for _, in := range []string{
		"ok denwa; op attacker",
		"ok denwa\nop attacker",
		"ok \"denwa\"",
		"ok §4denwa",
		"ok " + strings.Repeat("a", 17), // longer than Mojang allows
		"ok ../../etc/passwd",
		"ok *",
		"+denwa;op me",
		"ok denwa op attacker",
	} {
		got, ok := ParseOpsCommand(in)
		if !ok {
			continue // refused outright, which is fine
		}
		if !opsName.MatchString(got) {
			t.Errorf("ParseOpsCommand(%q) returned %q, which is not a Mojang name", in, got)
		}
	}
}

// An admin who types "ok denwa" and hears nothing cannot tell a name wly
// ignored from a server that refused, and will type it again.
func TestWhitelistResultAlwaysAnswers(t *testing.T) {
	good := WhitelistResult(WhitelistResultData{Player: "denwa"})
	if got := *good.Components[0].AccentColor; got != AccentWin {
		t.Errorf("success accent = #%06X", got)
	}
	if c := good.Components[0].Components[0].Content; !strings.Contains(c, "denwa") {
		t.Errorf("success does not name the player: %q", c)
	}

	bad := WhitelistResult(WhitelistResultData{Player: "denwa", Err: "rcon refused"})
	if got := *bad.Components[0].AccentColor; got != AccentLose {
		t.Errorf("failure accent = #%06X", got)
	}
	if c := bad.Components[0].Components[0].Content; !strings.Contains(c, "rcon refused") {
		t.Errorf("failure hides the reason: %q", c)
	}
}
