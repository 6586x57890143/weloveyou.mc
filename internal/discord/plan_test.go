package discord

import (
	"strings"
	"testing"
)

func base() *Guild {
	return &Guild{
		Meta: Meta{Name: "wly", ID: "42"},
		Roles: []Role{
			{Name: "admin", Color: "#C4705C", Hoist: true, Manual: true},
			{Name: "supporter", Color: "#E39AAE", Hoist: true,
				Colors: &Colors{Primary: "#E39AAE", Secondary: "#D8A657"}},
			{Name: "player", Color: "#8E8677"},
		},
		Channels: []Channel{
			{Name: "general", Category: "the server", Topic: "talk"},
			{Name: "feed", Category: "the world", Topic: "events", VisibleTo: []string{"player"}},
		},
		Emojis: Emojis{Source: "scripts/pixelicons.py", Upload: []string{"heart", "skull"}},
	}
}

func liveMatching() Live {
	return Live{
		ID: "42", Name: "wly", BotHighestRole: "wly",
		Features: []string{"ENHANCED_ROLE_COLORS"},
		Roles: []LiveRole{
			{Name: "wly", Managed: true},
			{Name: "admin", Color: 0xC4705C, Hoist: true},
			{Name: "supporter", Color: 0xE39AAE, Hoist: true},
			{Name: "player", Color: 0x8E8677},
			{Name: "@everyone"},
		},
		Channels: []LiveChannel{
			{Name: "general", Category: "the server", Topic: "talk"},
			// feed is visible_to player, so a matching server carries the two
			// overwrites that means. Without them here the fixture would be
			// claiming a private channel is correct while it is world-readable.
			{Name: "feed", Category: "the world", Topic: "events", Overwrites: []LiveOverwrite{
				{ID: "42", Type: OverwriteRole, Role: Everyone, Deny: PermViewChannel},
				{ID: "p", Type: OverwriteRole, Role: "player", Allow: PermViewChannel},
			}},
		},
		Emojis: []string{"heart", "skull"},
	}
}

func TestComputeNoChanges(t *testing.T) {
	p, err := Compute(base(), liveMatching())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Fatalf("wanted no changes, got:\n%s", Render(p))
	}
	if len(p.Drift) != 0 {
		t.Errorf("unexpected drift: %v", p.Drift)
	}
	if !strings.Contains(Render(p), "no changes") {
		t.Errorf("render = %q", Render(p))
	}
}

// The rule that costs the most to discover late: Discord refuses the edit and
// does not say so usefully, so a plan that emitted these would look like it
// worked and change nothing.
func TestComputeRefusesRoleAboveBot(t *testing.T) {
	live := liveMatching()
	live.Roles = []LiveRole{
		{Name: "supporter", Color: 0xE39AAE, Hoist: true}, // above the bot
		{Name: "wly", Managed: true},
		{Name: "admin", Color: 0xC4705C, Hoist: true},
		{Name: "player", Color: 0x8E8677},
	}
	_, err := Compute(base(), live)
	if err == nil {
		t.Fatal("planned against a role above the bot")
	}
	for _, want := range []string{"supporter", "strictly below", "Server Settings"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %q", err, want)
		}
	}
}

// A manual role above the bot is fine: the reconciler never edits it, so the
// hierarchy cannot bite. admin is exactly this case.
func TestComputeAllowsManualRoleAboveBot(t *testing.T) {
	live := liveMatching()
	live.Roles = []LiveRole{
		{Name: "admin", Color: 0xC4705C, Hoist: true},
		{Name: "wly", Managed: true},
		{Name: "supporter", Color: 0xE39AAE, Hoist: true},
		{Name: "player", Color: 0x8E8677},
	}
	if _, err := Compute(base(), live); err != nil {
		t.Fatalf("refused a manual role above the bot: %v", err)
	}
}

func TestComputeWrongGuild(t *testing.T) {
	live := liveMatching()
	live.ID = "999"
	_, err := Compute(base(), live)
	if err == nil || !strings.Contains(err.Error(), "refusing to reconcile") {
		t.Fatalf("error = %v, want a refusal", err)
	}
}

func TestComputeEmptyIDRefuses(t *testing.T) {
	g := base()
	g.Meta.ID = ""
	_, err := Compute(g, liveMatching())
	if err == nil || !strings.Contains(err.Error(), "id is empty") {
		t.Fatalf("error = %v, want a refusal naming the live id", err)
	}
	if !strings.Contains(err.Error(), "42") {
		t.Errorf("error %v should name the live guild id so it can be pasted in", err)
	}
}

func TestComputeCreatesMissing(t *testing.T) {
	live := liveMatching()
	live.Roles = []LiveRole{{Name: "wly", Managed: true}, {Name: "@everyone"}}
	live.Channels = nil
	live.Emojis = nil

	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[Kind]int{}
	for _, a := range p.Actions {
		counts[a.Kind]++
	}
	if counts[CreateRole] != 3 {
		t.Errorf("create role = %d, want 3", counts[CreateRole])
	}
	if counts[CreateChannel] != 2 {
		t.Errorf("create channel = %d, want 2", counts[CreateChannel])
	}
	if counts[CreateCategory] != 2 {
		t.Errorf("create category = %d, want 2", counts[CreateCategory])
	}
	if counts[UploadEmoji] != 2 {
		t.Errorf("upload emoji = %d, want 2", counts[UploadEmoji])
	}
}

func TestComputeUpdatesChanged(t *testing.T) {
	live := liveMatching()
	live.Roles[3].Color = 0x111111  // player recoloured by hand
	live.Roles[2].Hoist = false     // supporter un-hoisted
	live.Channels[0].Topic = "old"  // general's topic drifted
	live.Channels[1].Category = "x" // feed moved

	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	out := Render(p)
	for _, want := range []string{"update role player", "update role supporter",
		"update channel general", "update channel feed", "topic", "category"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan does not mention %q:\n%s", want, out)
		}
	}
}

// Apply never deletes. Anything present and undeclared is reported instead, and
// the wording has to say so, because that promise is the whole safety story.
func TestComputeReportsDriftAndNeverDeletes(t *testing.T) {
	live := liveMatching()
	live.Roles = append(live.Roles, LiveRole{Name: "leftover"})
	live.Channels = append(live.Channels, LiveChannel{Name: "old-chat", Category: "the server"})

	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Drift) != 2 {
		t.Fatalf("drift = %v, want role leftover and channel old-chat", p.Drift)
	}
	out := Render(p)
	if !strings.Contains(out, "NEVER removes") {
		t.Errorf("render does not promise non-deletion:\n%s", out)
	}
	for _, a := range p.Actions {
		if a.Kind == Drift {
			t.Errorf("drift leaked into actions: %v", a)
		}
	}
}

// A managed role belongs to Discord or another integration. Reporting it as
// drift every run would train the reader to ignore the drift list.
func TestComputeIgnoresManagedAndEveryone(t *testing.T) {
	live := liveMatching()
	live.Roles = append(live.Roles,
		LiveRole{Name: "Server Booster", Managed: true},
		LiveRole{Name: "some-other-bot", Managed: true})
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Drift) != 0 {
		t.Errorf("managed roles reported as drift: %v", p.Drift)
	}
}

func TestComputeManualRoleNeverUpdated(t *testing.T) {
	live := liveMatching()
	live.Roles[1].Color = 0x000000 // admin recoloured; manual, so leave it
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Actions {
		if a.Target == "admin" {
			t.Fatalf("planned an edit to a manual role: %v", a)
		}
	}
}

func TestGradientsDowngradeWithoutFeature(t *testing.T) {
	live := liveMatching()
	live.Features = nil
	live.Roles = []LiveRole{{Name: "wly", Managed: true}, {Name: "@everyone"}}
	live.Channels, live.Emojis = nil, nil

	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	if p.GradientsAvailable {
		t.Fatal("claimed gradients on a guild without the feature")
	}
	out := Render(p)
	if !strings.Contains(out, "ENHANCED_ROLE_COLORS is not on this guild") {
		t.Errorf("render does not explain the downgrade:\n%s", out)
	}
	if !strings.Contains(out, "using flat colour") {
		t.Errorf("create action does not note the downgrade:\n%s", out)
	}
}

func TestNoBotRoleKnownSkipsHierarchyCheck(t *testing.T) {
	live := liveMatching()
	live.BotHighestRole = ""
	if _, err := Compute(base(), live); err != nil {
		t.Fatalf("refused when the bot role was unknown: %v", err)
	}
	live.BotHighestRole = "not-in-the-list"
	if _, err := Compute(base(), live); err != nil {
		t.Fatalf("refused when the bot role was not found: %v", err)
	}
}

func TestKindString(t *testing.T) {
	if got := CreateRole.String(); got != "create role" {
		t.Errorf("CreateRole = %q", got)
	}
	if got := Kind(99).String(); !strings.Contains(got, "99") {
		t.Errorf("unknown kind = %q, want it to name the number", got)
	}
}

func TestActionString(t *testing.T) {
	if got := (Action{CreateRole, "a", ""}).String(); got != "create role a" {
		t.Errorf("got %q", got)
	}
	if got := (Action{UpdateRole, "a", "x"}).String(); got != "update role a: x" {
		t.Errorf("got %q", got)
	}
}

func TestChannelOverwrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		ch   Channel
		want []Overwrite
	}{
		{"open channel has none", Channel{Name: "general"}, nil},
		{"readonly denies send to everyone", Channel{Name: "a", ReadOnly: true},
			[]Overwrite{{Role: Everyone, Deny: PermSendMessages}}},
		{"private denies view to everyone and grants it back",
			Channel{Name: "ops", VisibleTo: []string{"admin"}},
			[]Overwrite{
				{Role: Everyone, Deny: PermViewChannel},
				{Role: "admin", Allow: PermViewChannel},
			}},
		{"private and readonly denies both, grants only view",
			Channel{Name: "feed", ReadOnly: true, VisibleTo: []string{"player"}},
			[]Overwrite{
				{Role: Everyone, Deny: PermViewChannel | PermSendMessages},
				{Role: "player", Allow: PermViewChannel},
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.ch.Overwrites()
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("overwrite %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The bit that matters most: a role allowed to see a readonly channel must not
// also gain the ability to post in it.
func TestOverwritesNeverGrantSendBack(t *testing.T) {
	for _, o := range (Channel{ReadOnly: true, VisibleTo: []string{"player"}}).Overwrites() {
		if o.Allow&PermSendMessages != 0 {
			t.Fatalf("%s was granted send in a readonly channel", o.Role)
		}
	}
}

// #ops carries spend and health. If this ever stops producing a deny, that
// channel is public.
func TestRealOpsChannelIsPrivate(t *testing.T) {
	g, err := Load("../../guild.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Channels {
		if c.Name != "ops" {
			continue
		}
		ow := c.Overwrites()
		if len(ow) == 0 || ow[0].Role != Everyone || ow[0].Deny&PermViewChannel == 0 {
			t.Fatalf("ops is not private: %+v", ow)
		}
		return
	}
	t.Fatal("no ops channel in guild.toml")
}

// wly never grants a managed role to an application, but any other bot with
// Manage Roles can. `player` gates the private half of the server, so one
// wearing it is inside, and the reconciler has to say so.
func TestComputeWarnsAboutBotsHoldingManagedRoles(t *testing.T) {
	live := liveMatching()
	live.Members = []LiveMember{
		{ID: "1", Name: "kon", Roles: []string{"player", "admin"}},
		{ID: "2", Name: "SomeOtherBot", Bot: true, Roles: []string{"player", "supporter"}},
		{ID: "3", Name: "HarmlessBot", Bot: true, Roles: []string{"undeclared-role"}},
	}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one about SomeOtherBot", p.Warnings)
	}
	w := p.Warnings[0]
	for _, want := range []string{"SomeOtherBot", "player", "supporter", "Manage Roles"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning does not mention %q: %s", want, w)
		}
	}
	// A human holding the roles is the entire point and must never warn.
	if strings.Contains(w, "kon") {
		t.Errorf("warned about a person: %s", w)
	}
	// It reports, it does not act. Stripping a role unasked is the same
	// overreach as deleting an undeclared channel.
	for _, a := range p.Actions {
		if strings.Contains(a.Target, "SomeOtherBot") {
			t.Errorf("planned an action against a bot: %v", a)
		}
	}
	if !strings.Contains(Render(p), "should not be true") {
		t.Errorf("render does not surface the warning:\n%s", Render(p))
	}
}

func TestNoWarningsOnACleanServer(t *testing.T) {
	live := liveMatching()
	live.Members = []LiveMember{{ID: "1", Name: "kon", Roles: []string{"player"}}}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings on a clean server: %v", p.Warnings)
	}
}

// Role ORDER is part of the diff, not a side effect of applying something else.
// It used to be applied only while creating roles, which meant that once every
// other thing matched, a wrong hierarchy could never be corrected: the plan came
// back empty and apply never ran.
func TestComputeDiffsRoleOrder(t *testing.T) {
	live := liveMatching()
	// supporter and player swapped on the server.
	live.Roles = []LiveRole{
		{Name: "wly", Managed: true},
		{Name: "admin", Color: 0xC4705C, Hoist: true},
		{Name: "player", Color: 0x8E8677},
		{Name: "supporter", Color: 0xE39AAE, Hoist: true},
		{Name: "@everyone"},
	}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range p.Actions {
		if a.Kind == ReorderRoles {
			found = true
			if !strings.Contains(a.Detail, "player, supporter") ||
				!strings.Contains(a.Detail, "supporter, player") {
				t.Errorf("detail does not show both orders: %q", a.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no reorder action for a swapped hierarchy:\n%s", Render(p))
	}
}

// A manual role sits wherever its owner put it. Dragging it around to satisfy
// this file is exactly the overreach `manual` exists to prevent.
func TestOrderIgnoresManualRoles(t *testing.T) {
	live := liveMatching()
	live.Roles = []LiveRole{
		{Name: "wly", Managed: true},
		{Name: "supporter", Color: 0xE39AAE, Hoist: true},
		{Name: "player", Color: 0x8E8677},
		{Name: "admin", Color: 0xC4705C, Hoist: true},
		{Name: "@everyone"},
	}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Actions {
		if a.Kind == ReorderRoles {
			t.Fatalf("reordered because of a manual role: %s", a.Detail)
		}
	}
}

// On a first run the roles do not exist yet. Comparing a short live list to a
// full declared one would report a reorder every time and bury the real signal.
func TestNoReorderWhenRolesAreMissing(t *testing.T) {
	live := liveMatching()
	live.Roles = []LiveRole{{Name: "wly", Managed: true}, {Name: "@everyone"}}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Actions {
		if a.Kind == ReorderRoles {
			t.Fatalf("reordered on a server with no roles yet: %s", a.Detail)
		}
	}
}

// The gap this closes: topic and category matched, so a channel whose privacy
// had been changed by hand read as correct. #feed is the private half of the
// server and #ops carries spend.
func TestComputeSeesPermissionDrift(t *testing.T) {
	live := liveMatching()
	for i, c := range live.Channels {
		if c.Name == "feed" {
			live.Channels[i].Overwrites = nil // someone cleared them in the client
		}
	}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	got := Render(p)
	if !strings.Contains(got, "update channel feed") {
		t.Fatalf("permission drift on a private channel went unreported:\n%s", got)
	}
	if !strings.Contains(got, "deny none -> view") {
		t.Errorf("the diff does not say what changes in words:\n%s", got)
	}
}

// Only the bits guild.toml decides are compared. A moderator role with Manage
// Messages, or an extra bit someone set on @everyone, is not this file's
// business and must not be reported as drift on every single run.
func TestPermissionDiffIgnoresUnmanagedBits(t *testing.T) {
	const manageMessages int64 = 1 << 13
	live := liveMatching()
	for i, c := range live.Channels {
		if c.Name == "feed" {
			live.Channels[i].Overwrites[0].Deny |= manageMessages
		}
	}
	p, err := Compute(base(), live)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Fatalf("an unmanaged bit was reported as drift:\n%s", Render(p))
	}
}

// Discord's PATCH replaces the whole overwrite set, so the merge is the only
// thing standing between "fix a topic" and "revoke a moderator".
func TestMergeOverwritesPreservesWhatGuildTomlDoesNotDeclare(t *testing.T) {
	const manageMessages int64 = 1 << 13
	live := []LiveOverwrite{
		{ID: "42", Type: OverwriteRole, Role: Everyone, Deny: manageMessages},
		{ID: "mod", Type: OverwriteRole, Role: "moderator", Allow: PermViewChannel},
		{ID: "kon", Type: 1, Allow: PermViewChannel}, // one person, by hand
	}
	want := Channel{Name: "feed", VisibleTo: []string{"player"}}.Overwrites()
	got := MergeOverwrites(want, live)

	by := map[string]LiveOverwrite{}
	for _, o := range got {
		by[o.ID] = o
	}
	if len(got) != 4 {
		t.Fatalf("merged set = %v, want the three live ones plus player", got)
	}
	if by["mod"].Allow != PermViewChannel {
		t.Errorf("the moderator role lost its access: %v", by["mod"])
	}
	if by["kon"].Type != 1 || by["kon"].Allow != PermViewChannel {
		t.Errorf("a member overwrite was rewritten as a role one: %v", by["kon"])
	}
	// @everyone keeps the unmanaged bit AND gains the declared one.
	if e := by["42"]; e.Deny != manageMessages|PermViewChannel {
		t.Errorf("@everyone deny = %d, want the hand-set bit kept and view added", e.Deny)
	}
	if by[""].Role != "player" || by[""].Allow != PermViewChannel {
		t.Errorf("player was not added with an unresolved id: %v", by[""])
	}
}

func TestPermNames(t *testing.T) {
	for bits, want := range map[int64]string{
		0:                                  "none",
		PermViewChannel:                    "view",
		PermSendMessages:                   "send",
		PermViewChannel | PermSendMessages: "view+send",
	} {
		if got := permNames(bits); got != want {
			t.Errorf("permNames(%d) = %q, want %q", bits, got, want)
		}
	}
}
