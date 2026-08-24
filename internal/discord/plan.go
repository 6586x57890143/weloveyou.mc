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
}

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

func planChannels(want *Guild, live Live, p *Plan) {
	byName := map[string]LiveChannel{}
	for _, c := range live.Channels {
		byName[c.Name] = c
	}

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

	for _, w := range want.Channels {
		cur, exists := byName[w.Name]
		if !exists {
			d := "in " + w.Category
			if w.Surface != "" {
				d += ", pinned surface " + w.Surface
			}
			p.Actions = append(p.Actions, Action{CreateChannel, w.Name, d})
			continue
		}
		var diffs []string
		if cur.Topic != w.Topic {
			diffs = append(diffs, fmt.Sprintf("topic %q -> %q", cur.Topic, w.Topic))
		}
		if cur.Category != w.Category {
			diffs = append(diffs, fmt.Sprintf("category %q -> %q", cur.Category, w.Category))
		}
		if len(diffs) > 0 {
			p.Actions = append(p.Actions, Action{UpdateChannel, w.Name, strings.Join(diffs, ", ")})
		}
	}

	declared := map[string]bool{}
	for _, c := range want.Channels {
		declared[c.Name] = true
	}
	for _, c := range live.Channels {
		if !declared[c.Name] {
			p.Drift = append(p.Drift, Action{Drift, "channel " + c.Name,
				"present on the server, absent from guild.toml"})
		}
	}
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
func (c Channel) Overwrites() []Overwrite {
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
