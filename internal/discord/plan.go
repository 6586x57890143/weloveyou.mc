package discord

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Live is the guild as it exists now, narrowed to what reconciliation reads.
// cmd/wly fills it from the API; nothing in this package fetches it.
type Live struct {
	ID       string
	Name     string
	Roles    []LiveRole // highest first, the order Discord reports
	Channels []LiveChannel
	Emojis   []string
	Features []string // guild feature flags, e.g. ENHANCED_ROLE_COLORS
	Members  []LiveMember

	// MembersUnavailable explains why Members is empty, when it is empty
	// because the read failed rather than because there are none. Silence
	// would look identical to a clean server, which is the wrong default for
	// a check whose whole job is noticing something bad.
	MembersUnavailable string

	// BotHighestRole is the name of the highest role the bot itself holds, and
	// BotHighestPosition its Discord position. Everything wly manages must sit
	// strictly below it, and the reorder counts down from this rather than from
	// the number of declared roles: counting from the top would place them ABOVE
	// the bot on a server where nobody moved it, which is the exact hierarchy
	// trap the planner refuses to plan around.
	BotHighestRole     string
	BotHighestPosition int
}

type LiveRole struct {
	ID          string
	Name        string
	Position    int
	Color       int
	Hoist       bool
	Mentionable bool
	Managed     bool // Discord owns it: a bot's own role, a boost role
}

// LiveMember is a guild member, narrowed to what the bot gate reads.
type LiveMember struct {
	ID    string
	Name  string
	Bot   bool
	Roles []string // role names, resolved by the caller
}

type LiveChannel struct {
	ID       string
	Name     string
	Category string
	Topic    string

	// Overwrites is the channel's permission overwrites as Discord reports
	// them, with role names resolved by the caller. Without them a channel
	// whose privacy was changed by hand reads as matching guild.toml, because
	// topic and category still do. #ops carries spend and health and #feed is
	// the private half of the server, so that is not a cosmetic gap.
	Overwrites []LiveOverwrite
}

// LiveOverwrite is one permission overwrite on a live channel. Type is
// Discord's: 0 role, 1 member. Role is the resolved name when Type is
// OverwriteRole and the role is known, "" otherwise.
type LiveOverwrite struct {
	ID    string
	Type  int
	Role  string
	Allow int64
	Deny  int64
}

// OverwriteRole is Discord's overwrite type for a role. Member overwrites are
// type 1: read and preserved, never written.
const OverwriteRole = 0

// Kind is what an action does. Ordered so a plan reads roles before channels,
// because a channel's permissions reference roles that may not exist yet.
type Kind int

const (
	CreateRole Kind = iota
	UpdateRole
	ReorderRoles
	CreateCategory
	CreateChannel
	UpdateChannel
	UploadEmoji
	Drift
)

var kindNames = map[Kind]string{
	CreateRole: "create role", UpdateRole: "update role",
	ReorderRoles:   "reorder roles",
	CreateCategory: "create category", CreateChannel: "create channel",
	UpdateChannel: "update channel", UploadEmoji: "upload emoji",
	Drift: "drift",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Action is one thing apply would do, or one thing it deliberately will not.
type Action struct {
	Kind   Kind
	Target string
	Detail string
}

func (a Action) String() string {
	if a.Detail == "" {
		return fmt.Sprintf("%s %s", a.Kind, a.Target)
	}
	return fmt.Sprintf("%s %s: %s", a.Kind, a.Target, a.Detail)
}

// Plan is the whole diff. Drift is kept apart from Actions because it is
// reported and never acted on: apply never deletes.
type Plan struct {
	Actions []Action
	Drift   []Action

	// Warnings are findings that are neither a change to make nor drift to
	// tolerate: something is true on the server that should not be. They are
	// kept apart from Drift because "an undeclared channel exists" and "an
	// application holds the role that gates the private half of the server" do
	// not deserve the same line in the same list.
	Warnings []string

	// GradientsAvailable records whether the guild can render the declared
	// gradients. When false they are downgraded to the flat colour rather than
	// failing, and the plan says so once instead of per role.
	GradientsAvailable bool
}

// Empty reports whether reality already matches the file.
func (p *Plan) Empty() bool { return len(p.Actions) == 0 }

// Compute diffs a declared guild against a live one.
//
// It returns an error rather than a plan in exactly two cases, both of which
// would otherwise produce changes that silently do nothing:
//
//   - the live guild is not the declared one, so a token pointed at the wrong
//     server cannot rewrite it
//   - a managed role sits at or above the bot's own highest role, where every
//     edit to it is refused by Discord with no error the caller would notice
func Compute(want *Guild, live Live) (*Plan, error) {
	if want.Meta.ID == "" {
		return nil, fmt.Errorf("guild config: [guild] id is empty, so nothing "+
			"can be reconciled; set it to %q to allow it", live.ID)
	}
	if want.Meta.ID != live.ID {
		return nil, fmt.Errorf("guild config declares id %s but the token is on "+
			"%s (%q); refusing to reconcile a guild this file does not describe",
			want.Meta.ID, live.ID, live.Name)
	}
	if err := checkHierarchy(want, live); err != nil {
		return nil, err
	}

	p := &Plan{GradientsAvailable: slices.Contains(live.Features, "ENHANCED_ROLE_COLORS")}
	byName := map[string]LiveRole{}
	for _, r := range live.Roles {
		byName[r.Name] = r
	}

	for _, want := range want.Roles {
		cur, exists := byName[want.Name]
		if !exists {
			p.Actions = append(p.Actions, Action{CreateRole, want.Name, roleSummary(want, p)})
			continue
		}
		if want.Manual {
			continue // reported by drift if it differs, never edited
		}
		if d := roleDiff(want, cur, p); d != "" {
			p.Actions = append(p.Actions, Action{UpdateRole, want.Name, d})
		}
	}

	declared := map[string]bool{}
	for _, r := range want.Roles {
		declared[r.Name] = true
	}
	for _, r := range live.Roles {
		// @everyone is the guild id and is never declared; managed roles belong
		// to Discord or to another integration and are not ours to touch.
		if declared[r.Name] || r.Managed || r.Name == "@everyone" || r.Name == live.BotHighestRole {
			continue
		}
		p.Drift = append(p.Drift, Action{Drift, "role " + r.Name,
			"present on the server, absent from guild.toml"})
	}

	// Role ORDER is part of the diff, not a side effect of applying something
	// else. It used to be applied only while creating roles, which meant that
	// once every other thing matched, a wrong hierarchy could never be
	// corrected: the plan came back empty and apply never ran. On a real server
	// the order was right purely because creation order happened to match the
	// file, which is luck, not configuration.
	if d := orderDiff(want, live); d != "" {
		p.Actions = append(p.Actions, Action{ReorderRoles, "hierarchy", d})
	}

	checkBotsHoldingRoles(want, live, p)
	planChannels(want, live, p)
	planEmojis(want, live, p)
	return p, nil
}

// checkHierarchy enforces the rule that costs the most to discover late: a bot
// can only manage roles strictly below its own highest role. Discord does not
// error helpfully here, it simply refuses the edit.
func checkHierarchy(want *Guild, live Live) error {
	if live.BotHighestRole == "" {
		return nil // caller could not determine it; not this function's failure
	}
	botAt := -1
	for i, r := range live.Roles {
		if r.Name == live.BotHighestRole {
			botAt = i
			break
		}
	}
	if botAt < 0 {
		return nil
	}
	declared := map[string]bool{}
	for _, r := range want.Roles {
		if !r.Manual {
			declared[r.Name] = true
		}
	}
	var above []string
	for i := 0; i < botAt; i++ {
		if declared[live.Roles[i].Name] {
			above = append(above, live.Roles[i].Name)
		}
	}
	if len(above) == 0 {
		return nil
	}
	return fmt.Errorf("these managed roles sit above the bot's own %q role: %s.\n"+
		"A bot can only manage roles strictly below its highest one, so every edit "+
		"to them would be refused with no visible error. Drag %q to the top of "+
		"Server Settings -> Roles and run this again",
		live.BotHighestRole, strings.Join(above, ", "), live.BotHighestRole)
}

// MatchChannel finds the live channel a declared one refers to.
//
// The id wins when it is set, because the name is the field most likely to
// change and matching on it means a rename reads as "create a second channel
// and abandon the first". That is how a config file destroys a channel of
// history without ever issuing a delete, which is the one thing apply promises
// it cannot do. Name is the fallback, for a channel that has no id yet because
// it has never been created.
func MatchChannel(w Channel, live Live) (LiveChannel, bool) {
	if w.ID != "" {
		for _, c := range live.Channels {
			if c.ID == w.ID {
				return c, true
			}
		}
		// Declared with an id that is not there: the channel was deleted, or
		// the id is wrong. Either way it is not "the one with the same name",
		// because that may be a different channel entirely.
		return LiveChannel{}, false
	}
	for _, c := range live.Channels {
		if c.Name == w.Name {
			return c, true
		}
	}
	return LiveChannel{}, false
}

// liveKey identifies a live channel for bookkeeping. The id when there is one,
// the name otherwise, so a guild whose channels have not been created yet still
// gets an honest drift report rather than one where every unnamed channel
// collides on the empty string.
func liveKey(c LiveChannel) string {
	if c.ID != "" {
		return "id:" + c.ID
	}
	return "name:" + c.Name
}

func planChannels(want *Guild, live Live, p *Plan) {
	haveCategory := map[string]bool{}
	for _, c := range live.Channels {
		if c.Category != "" {
			haveCategory[c.Category] = true
		}
	}
	for _, cat := range want.Categories() {
		if !haveCategory[cat] {
			p.Actions = append(p.Actions, Action{CreateCategory, cat, ""})
			haveCategory[cat] = true
		}
	}

	matched := map[string]bool{} // live channels a declared one accounts for
	for _, w := range want.Channels {
		cur, exists := MatchChannel(w, live)
		if !exists {
			d := "in " + w.Category
			if w.Surface != "" {
				d += ", pinned surface " + w.Surface
			}
			p.Actions = append(p.Actions, Action{CreateChannel, w.Name, d})
			continue
		}
		matched[liveKey(cur)] = true
		var diffs []string
		if cur.Name != w.Name {
			diffs = append(diffs, fmt.Sprintf("name %q -> %q", cur.Name, w.Name))
		}
		if cur.Topic != w.Topic {
			diffs = append(diffs, fmt.Sprintf("topic %q -> %q", cur.Topic, w.Topic))
		}
		if cur.Category != w.Category {
			diffs = append(diffs, fmt.Sprintf("category %q -> %q", cur.Category, w.Category))
		}
		if d := overwriteDiff(w, cur, live.BotHighestRole); d != "" {
			diffs = append(diffs, d)
		}
		if len(diffs) > 0 {
			p.Actions = append(p.Actions, Action{UpdateChannel, w.Name, strings.Join(diffs, ", ")})
		}
	}

	// Drift is what nothing declared accounts for. Matching by id rather than
	// by name is what keeps a renamed channel out of this list: on the name
	// alone, every rename would report the original as drift at the same moment
	// it created its replacement.
	for _, c := range live.Channels {
		if !matched[liveKey(c)] {
			p.Drift = append(p.Drift, Action{Drift, "channel " + c.Name,
				"present on the server, absent from guild.toml"})
		}
	}
}

// overwriteDiff describes how a live channel's permissions differ from what its
// declared readonly and visible_to flags mean, or "" when they do not.
//
// Only the bits guild.toml decides are compared, and only for the roles it
// names. Everything else on the channel belongs to whoever put it there.
func overwriteDiff(want Channel, cur LiveChannel, botRole string) string {
	live := map[string]LiveOverwrite{}
	for _, o := range cur.Overwrites {
		if o.Type == OverwriteRole {
			live[o.Role] = o
		}
	}
	var out []string
	for _, w := range want.Overwrites(botRole) {
		got := live[w.Role] // absent reads as no bits, which is the right diff
		if got.Allow&ManagedPerms == w.Allow && got.Deny&ManagedPerms == w.Deny {
			continue
		}
		out = append(out, fmt.Sprintf("%s allow %s -> %s, deny %s -> %s", w.Role,
			permNames(got.Allow&ManagedPerms), permNames(w.Allow),
			permNames(got.Deny&ManagedPerms), permNames(w.Deny)))
	}
	return strings.Join(out, "; ")
}

// permNames renders the managed bits. A raw 1024 in a plan is unreadable, and
// this is the line someone reads while deciding whether #ops is exposed.
func permNames(bits int64) string {
	var out []string
	if bits&PermViewChannel != 0 {
		out = append(out, "view")
	}
	if bits&PermSendMessages != 0 {
		out = append(out, "send")
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, "+")
}

// MergeOverwrites returns the overwrite set to send, given what guild.toml
// declares and what the channel carries now.
//
// It merges rather than replaces because Discord's PATCH replaces the whole
// set. Sending only the declared overwrites would strip a moderator role's
// access, or one member's, as a side effect of fixing a topic. Apply never
// deletes, and this is where that rule meets an API that would.
//
// A new entry has no ID; the caller resolves it from the role name, the same
// split every other id in this package uses.
func MergeOverwrites(want []Overwrite, live []LiveOverwrite) []LiveOverwrite {
	out := append([]LiveOverwrite(nil), live...)
	at := map[string]int{}
	for i, o := range out {
		if o.Type == OverwriteRole {
			at[o.Role] = i
		}
	}
	for _, w := range want {
		i, ok := at[w.Role]
		if !ok {
			out = append(out, LiveOverwrite{Type: OverwriteRole, Role: w.Role,
				Allow: w.Allow, Deny: w.Deny})
			continue
		}
		out[i].Allow = out[i].Allow&^ManagedPerms | w.Allow
		out[i].Deny = out[i].Deny&^ManagedPerms | w.Deny
	}
	return out
}

func planEmojis(want *Guild, live Live, p *Plan) {
	have := map[string]bool{}
	for _, e := range live.Emojis {
		have[e] = true
	}
	missing := map[string]bool{}
	for _, e := range want.Emojis.Upload {
		if !have[e] {
			missing[e] = true
		}
	}
	for _, name := range sortedNames(missing) {
		p.Actions = append(p.Actions, Action{UploadEmoji, name, "from " + want.Emojis.Source})
	}
}

// roleDiff describes what would change, or "" when nothing would.
func roleDiff(want Role, cur LiveRole, p *Plan) string {
	var diffs []string
	if c, err := ParseColor(want.Color); err == nil && c != cur.Color {
		diffs = append(diffs, fmt.Sprintf("color #%06X -> %s", cur.Color, want.Color))
	}
	if want.Hoist != cur.Hoist {
		diffs = append(diffs, fmt.Sprintf("hoist %t -> %t", cur.Hoist, want.Hoist))
	}
	if want.Mentionable != cur.Mentionable {
		diffs = append(diffs, fmt.Sprintf("mentionable %t -> %t", cur.Mentionable, want.Mentionable))
	}
	// Gradients are not read back from the live role here: Discord reports them
	// in a shape this narrowed Live does not carry, so claiming they match would
	// be a guess. They are applied with the rest of the update when present.
	if want.Colors != nil && p.GradientsAvailable && len(diffs) > 0 {
		diffs = append(diffs, "gradient "+want.Colors.Primary+" -> "+want.Colors.Secondary)
	}
	return strings.Join(diffs, ", ")
}

func roleSummary(r Role, p *Plan) string {
	parts := []string{r.Color}
	if r.Colors != nil {
		if p.GradientsAvailable {
			parts[0] = r.Colors.Primary + " -> " + r.Colors.Secondary
		} else {
			parts = append(parts, "gradient declared but ENHANCED_ROLE_COLORS is absent, using flat colour")
		}
	}
	if r.Hoist {
		parts = append(parts, "hoisted")
	}
	if r.GrantedBy != "" {
		parts = append(parts, "granted by "+r.GrantedBy)
	}
	return strings.Join(parts, ", ")
}

// Render prints a plan the way a human reads one: what changes, then what is
// there that the file does not describe.
func Render(p *Plan) string {
	var b strings.Builder
	if p.Empty() {
		b.WriteString("no changes: the server matches guild.toml\n")
	} else {
		fmt.Fprintf(&b, "%d change(s):\n", len(p.Actions))
		acts := append([]Action(nil), p.Actions...)
		sort.SliceStable(acts, func(i, j int) bool { return acts[i].Kind < acts[j].Kind })
		for _, a := range acts {
			fmt.Fprintf(&b, "  + %s\n", a)
		}
	}
	if !p.GradientsAvailable {
		b.WriteString("\nENHANCED_ROLE_COLORS is not on this guild, so gradients " +
			"fall back to the flat colour. That is a boost level, not a bug.\n")
	}
	if len(p.Warnings) > 0 {
		fmt.Fprintf(&b, "\n%d thing(s) that should not be true:\n", len(p.Warnings))
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "  ! %s\n", w)
		}
	}
	if len(p.Drift) > 0 {
		fmt.Fprintf(&b, "\n%d thing(s) on the server that guild.toml does not "+
			"describe. Apply NEVER removes these:\n", len(p.Drift))
		for _, d := range p.Drift {
			fmt.Fprintf(&b, "  ? %s\n", d.Target)
		}
	}
	return b.String()
}

// Discord permission bits, only the two this needs. Named because 1<<10 in a
// call site is unreadable and one bit wrong here is a channel exposed.
const (
	PermViewChannel  int64 = 1 << 10
	PermSendMessages int64 = 1 << 11
)

// ManagedPerms is every bit guild.toml gets to decide. Diffing and applying are
// both masked to it, so a bit a human set by hand -- Manage Messages on a
// moderator role, say -- is neither reported as drift on every run nor wiped by
// the next apply.
const ManagedPerms = PermViewChannel | PermSendMessages

// Everyone is the @everyone role. Discord gives it the guild's own id, which
// the caller substitutes; naming it here keeps that trick out of the API layer.
const Everyone = "@everyone"

// Overwrite is one permission overwrite, by role name rather than id, because
// ids do not exist until the roles do.
type Overwrite struct {
	Role  string
	Allow int64
	Deny  int64
}

// Overwrites is what a channel's visibility and readonly flags mean in
// permission bits.
//
// This is pure on purpose. A channel declared visible_to ["admin"] that gets
// created without these is readable by everyone, and #ops carries spend and
// health. Getting it wrong is not a cosmetic bug, so it is decided here where
// it is tested, not inline in an HTTP call.
//
// botRole is the role wly itself wears, and it is not decoration. A restriction
// applies to the bot like it applies to anyone: denying @everyone view denies it
// to wly too, and wly is the only thing that ever writes the pinned surface in
// there. Applying such a channel without this locks the bot out of exactly the
// channels it exists to maintain, and the bill arrives as a 403 on some LATER
// run, far from the change that caused it. Learned that way, on the live guild,
// renaming #feed. An empty botRole skips the grant, for a caller that does not
// know the name yet.
func (c Channel) Overwrites(botRole string) []Overwrite {
	var out []Overwrite
	deny := int64(0)
	if len(c.VisibleTo) > 0 {
		deny |= PermViewChannel
	}
	if c.ReadOnly {
		deny |= PermSendMessages
	}
	if deny != 0 {
		out = append(out, Overwrite{Role: Everyone, Deny: deny})
		// Whatever was taken from @everyone, wly keeps. Send included: a
		// readonly channel is readonly for people, not for the thing whose
		// surface it holds.
		if botRole != "" {
			out = append(out, Overwrite{Role: botRole,
				Allow: PermViewChannel | PermSendMessages})
		}
	}
	for _, r := range c.VisibleTo {
		// Granted the view back, but NOT send: a readonly channel stays readonly
		// for the roles that can see it, which is the point of a pinned surface.
		out = append(out, Overwrite{Role: r, Allow: PermViewChannel})
	}
	return out
}

// checkBotsHoldingRoles reports any application wearing a role this file
// manages.
//
// `player` gates the in-game channels and everything later is built on it, so an
// application holding it is inside the private half of the server. MayLink stops
// wly ever granting one; this catches a grant that came from somewhere else,
// which on a Discord server is entirely possible: any other bot with Manage
// Roles and a high enough position can hand out roles, including to itself.
//
// It reports rather than removes. Stripping a role from an application without
// being asked is the same overreach as deleting an undeclared channel, and the
// right response depends on which bot it is and why it is there.
func checkBotsHoldingRoles(want *Guild, live Live, p *Plan) {
	if live.MembersUnavailable != "" {
		p.Warnings = append(p.Warnings, "could not check whether any application "+
			"holds a managed role: "+live.MembersUnavailable)
		return
	}
	managed := map[string]bool{}
	for _, r := range want.Roles {
		managed[r.Name] = true
	}
	for _, m := range live.Members {
		if !m.Bot {
			continue
		}
		var held []string
		for _, r := range m.Roles {
			if managed[r] {
				held = append(held, r)
			}
		}
		if len(held) > 0 {
			sort.Strings(held)
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"the application %q holds %s. wly never grants a managed role to a "+
					"bot, so this came from elsewhere: check which other app has "+
					"Manage Roles", m.Name, strings.Join(held, " and ")))
		}
	}
}

// orderDiff describes how the live hierarchy differs from the declared one, or
// "" when it does not.
//
// Only declared, non-manual roles are compared. Everything else on the server
// sits wherever its owner put it, and dragging someone's integration role
// around to satisfy this file would be exactly the overreach `manual` exists to
// prevent.
func orderDiff(want *Guild, live Live) string {
	declared := map[string]bool{}
	var wantOrder []string
	for _, r := range want.Roles {
		if r.Manual {
			continue
		}
		declared[r.Name] = true
		wantOrder = append(wantOrder, r.Name)
	}

	var liveOrder []string
	for _, r := range live.Roles {
		if declared[r.Name] {
			liveOrder = append(liveOrder, r.Name)
		}
	}
	// A role that does not exist yet is a create, not a reorder. Comparing a
	// short list against a long one would report a difference on every first
	// run and bury the real signal.
	if len(liveOrder) != len(wantOrder) {
		return ""
	}
	if slices.Equal(liveOrder, wantOrder) {
		return ""
	}
	return fmt.Sprintf("%s -> %s", strings.Join(liveOrder, ", "), strings.Join(wantOrder, ", "))
}
