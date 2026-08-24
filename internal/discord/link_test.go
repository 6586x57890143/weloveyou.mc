package discord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// The real values from the production server, so the test is about a player who
// exists rather than a shape invented here.
const (
	denwaUUID = "6dc57b83-7a60-4b47-baf4-f5ce8c501953"
	denwaName = "denwa"
)

func open(discordID, name, uuid string, in time.Duration) Request {
	return Request{DiscordID: discordID, MCName: name, MCUUID: uuid,
		ExpiresAt: now.Add(in)}
}

func TestMatchLinks(t *testing.T) {
	reqs := []Request{open("111", denwaName, denwaUUID, 10*time.Minute)}
	got, state := Match(denwaUUID, denwaName, reqs, nil, now)
	if state != LinkOK {
		t.Fatalf("state = %v, want linked", state)
	}
	if got.DiscordID != "111" {
		t.Errorf("linked the wrong request: %+v", got)
	}
}

// Minecraft names preserve case but are unique without it, so a case-sensitive
// compare would refuse exactly the right person.
func TestMatchIsCaseInsensitiveOnNameAndUUID(t *testing.T) {
	reqs := []Request{open("111", "DenWa", "6DC57B83-7A60-4B47-BAF4-F5CE8C501953", time.Hour)}
	if _, state := Match(denwaUUID, "denwa", reqs, nil, now); state != LinkOK {
		t.Errorf("state = %v, want linked", state)
	}
}

func TestMatchNoRequest(t *testing.T) {
	if _, state := Match(denwaUUID, denwaName, nil, nil, now); state != LinkNoRequest {
		t.Errorf("state = %v, want no request", state)
	}
	// A request for a different player must not match this join.
	reqs := []Request{open("111", "someone-else", "1111", time.Hour)}
	if _, state := Match(denwaUUID, denwaName, reqs, nil, now); state != LinkNoRequest {
		t.Errorf("state = %v, want no request", state)
	}
}

func TestMatchExpired(t *testing.T) {
	reqs := []Request{open("111", denwaName, denwaUUID, -time.Second)}
	if _, state := Match(denwaUUID, denwaName, reqs, nil, now); state != LinkExpired {
		t.Errorf("state = %v, want expired", state)
	}
}

// Someone typed a name they do not own, and the real owner then tried to join.
// The name lines up and the account does not, which must never link.
func TestMatchWrongAccountBehindTheName(t *testing.T) {
	reqs := []Request{open("111", denwaName, "99999999-9999-9999-9999-999999999999", time.Hour)}
	got, state := Match(denwaUUID, denwaName, reqs, nil, now)
	if state != LinkWrongAccount {
		t.Fatalf("state = %v, want wrong account", state)
	}
	if got.DiscordID != "" {
		t.Errorf("returned a request it refused: %+v", got)
	}
}

// The outcome that would let someone take over another player's history, so it
// beats every other rule including a live, valid-looking request.
func TestMatchRefusesAnAccountSomeoneElseOwns(t *testing.T) {
	existing := []Link{{DiscordID: "222", MCUUID: denwaUUID, MCName: denwaName}}
	reqs := []Request{open("111", denwaName, denwaUUID, time.Hour)}
	if _, state := Match(denwaUUID, denwaName, reqs, existing, now); state != LinkAlreadyClaimed {
		t.Fatalf("state = %v, want already claimed", state)
	}
}

// The same person re-linking themselves is harmless and must not be refused,
// or anyone who reinstalls Discord is locked out of their own account.
func TestMatchAllowsTheSameOwnerToRelink(t *testing.T) {
	existing := []Link{{DiscordID: "111", MCUUID: denwaUUID, MCName: denwaName}}
	reqs := []Request{open("111", denwaName, denwaUUID, time.Hour)}
	if _, state := Match(denwaUUID, denwaName, reqs, existing, now); state != LinkOK {
		t.Fatalf("state = %v, want linked", state)
	}
}

func TestCanRequest(t *testing.T) {
	existing := []Link{{DiscordID: "222", MCUUID: denwaUUID}}

	if s := CanRequest("111", denwaUUID, nil, existing, now); s != LinkAlreadyClaimed {
		t.Errorf("state = %v, want refusal for someone else's account", s)
	}
	if s := CanRequest("222", denwaUUID, nil, existing, now); s != LinkOK {
		t.Errorf("state = %v, want the owner allowed to re-request", s)
	}
	if s := CanRequest("111", "aaaa", nil, existing, now); s != LinkOK {
		t.Errorf("state = %v, want a free account allowed", s)
	}

	// Two people mid-flight for one account would mean whoever joins first wins
	// it, which is a race decided by luck rather than ownership.
	inflight := []Request{open("333", denwaName, "bbbb", time.Hour)}
	if s := CanRequest("111", "bbbb", inflight, nil, now); s != LinkAlreadyClaimed {
		t.Errorf("state = %v, want refusal while another request is open", s)
	}
	// An expired one is not in flight and must not block.
	stale := []Request{open("333", denwaName, "bbbb", -time.Hour)}
	if s := CanRequest("111", "bbbb", stale, nil, now); s != LinkOK {
		t.Errorf("state = %v, want an expired request not to block", s)
	}
}

func TestNormalise(t *testing.T) {
	for _, in := range []string{"Denwa", "  denwa ", "DENWA"} {
		if got := Normalise(in); got != "denwa" {
			t.Errorf("Normalise(%q) = %q", in, got)
		}
	}
}

// Every refusal has to be explainable to the person at the kick screen, so none
// of them may render as a bare number.
func TestLinkStateStrings(t *testing.T) {
	for _, s := range []LinkState{LinkOK, LinkNoRequest, LinkExpired,
		LinkWrongAccount, LinkAlreadyClaimed} {
		if got := s.String(); got == "" || got[0] == 'l' && len(got) > 9 && got[:9] == "linkstate" {
			t.Errorf("state %d has no readable reason: %q", int(s), got)
		}
	}
	if got := LinkState(99).String(); got != "linkstate(99)" {
		t.Errorf("unknown state = %q", got)
	}
}

// The gate is only proof while Mojang is authenticating sessions. If anyone
// ever sets ONLINE_MODE to false, a join attempt stops meaning ownership and
// this test is where that shows up rather than in a stranger wearing someone
// else's name.
func TestOnlineModeIsStillOn(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `ONLINE_MODE: "true"`) {
		t.Fatal("deploy/docker-compose.yml no longer sets ONLINE_MODE true. " +
			"The link gate treats a join attempt as proof of account ownership, " +
			"which is only true while Mojang authenticates the session.")
	}
}
