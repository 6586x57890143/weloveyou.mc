// Package discord holds the pure half of the Discord layer: what the server
// should look like, what it does look like, and the difference between them.
//
// Nothing here talks to Discord. No HTTP, no token, no gateway. That lives in
// cmd/wly, so the part that decides to change a live community is testable
// without one, which is the same split internal/bench already uses for the
// container it drives. Dependencies point inward.
package discord

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Guild is guild.toml.
//
// Roles and Channels are slices rather than maps because order carries meaning
// and a map has none: role order is the hierarchy, channel order is display
// order. An earlier draft used named tables and claimed an ordering it could
// not keep.
type Guild struct {
	Meta     Meta      `toml:"guild"`
	Roles    []Role    `toml:"roles"`
	Channels []Channel `toml:"channels"`
	Emojis   Emojis    `toml:"emojis"`
}

type Meta struct {
	Name string `toml:"name"`
	ID   string `toml:"id"`
}

// Role is one declared role. Position is its index, so the first entry is the
// highest and the reconciler never has to be told a number that can go stale.
type Role struct {
	Name        string   `toml:"name"`
	Color       string   `toml:"color"`
	Colors      *Colors  `toml:"colors"`
	Hoist       bool     `toml:"hoist"`
	Mentionable bool     `toml:"mentionable"`
	Permissions []string `toml:"permissions"`

	// Manual roles are reported and never edited. A config file is the wrong
	// place to be the authority on who is an administrator.
	Manual bool `toml:"manual"`

	GrantedBy   string `toml:"granted_by"`
	MetadataKey string `toml:"metadata_key"`
	Threshold   int    `toml:"threshold"`
}

// Colors is the gradient. Secondary alone is a gradient, secondary plus
// tertiary is holographic, and both need the ENHANCED_ROLE_COLORS guild
// feature, which is boost-gated and therefore not guaranteed.
type Colors struct {
	Primary   string `toml:"primary"`
	Secondary string `toml:"secondary"`
	Tertiary  string `toml:"tertiary"`
}

type Channel struct {
	// ID is the Discord channel id, and it is what makes a rename possible.
	// Matching on the name alone means changing one reads as "create a second
	// channel and abandon the first", which is how a config file destroys a
	// channel of history without ever issuing a delete. Empty is fine: a
	// channel that does not exist yet has no id, and matching falls back to the
	// name until it does.
	ID        string   `toml:"id"`
	Name      string   `toml:"name"`
	Category  string   `toml:"category"`
	Topic     string   `toml:"topic"`
	Surface   string   `toml:"surface"`
	ReadOnly  bool     `toml:"readonly"`
	VisibleTo []string `toml:"visible_to"`
}

type Emojis struct {
	Source string   `toml:"source"`
	Upload []string `toml:"upload"`
}

// Load reads and validates guild.toml. Validation is not decoration: every rule
// here is one the reconciler would otherwise discover against a live community.
func Load(path string) (*Guild, error) {
	var g Guild
	if _, err := toml.DecodeFile(path, &g); err != nil {
		return nil, fmt.Errorf("guild config: %w", err)
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return &g, nil
}

// Validate reports the first thing wrong with a parsed config.
func (g *Guild) Validate() error {
	if strings.TrimSpace(g.Meta.Name) == "" {
		return fmt.Errorf("guild config: [guild] name is empty")
	}

	seen := map[string]bool{}
	for i, r := range g.Roles {
		switch {
		case strings.TrimSpace(r.Name) == "":
			return fmt.Errorf("guild config: role %d has no name", i)
		case seen[r.Name]:
			return fmt.Errorf("guild config: role %q declared twice", r.Name)
		}
		seen[r.Name] = true

		if _, err := ParseColor(r.Color); err != nil {
			return fmt.Errorf("guild config: role %q: %w", r.Name, err)
		}
		if r.Colors != nil {
			for label, c := range map[string]string{
				"primary": r.Colors.Primary, "secondary": r.Colors.Secondary,
				"tertiary": r.Colors.Tertiary,
			} {
				if c == "" {
					continue
				}
				if _, err := ParseColor(c); err != nil {
					return fmt.Errorf("guild config: role %q colors.%s: %w", r.Name, label, err)
				}
			}
			// Discord reads tertiary only alongside secondary, and a lone
			// tertiary is silently ignored rather than rejected, which is the
			// worst of both.
			if r.Colors.Tertiary != "" && r.Colors.Secondary == "" {
				return fmt.Errorf("guild config: role %q sets colors.tertiary "+
					"without colors.secondary, which Discord ignores", r.Name)
			}
			// The flat colour is the fallback for a guild without the feature.
			// Without it such a guild gets Discord's default grey.
			if r.Color == "" {
				return fmt.Errorf("guild config: role %q sets colors but no flat "+
					"color, which is the fallback when ENHANCED_ROLE_COLORS is absent", r.Name)
			}
		}
	}

	seen = map[string]bool{}
	seenID := map[string]string{}
	for i, c := range g.Channels {
		switch {
		case strings.TrimSpace(c.Name) == "":
			return fmt.Errorf("guild config: channel %d has no name", i)
		case seen[c.Name]:
			return fmt.Errorf("guild config: channel %q declared twice", c.Name)
		case c.Name != strings.ToLower(c.Name) || strings.Contains(c.Name, " "):
			// Discord lowercases and hyphenates text channel names on its own,
			// so a name with capitals or spaces never matches what comes back
			// and the reconciler would recreate it on every run.
			return fmt.Errorf("guild config: channel %q must be lowercase with "+
				"no spaces, or it will not match what Discord stores", c.Name)
		}
		seen[c.Name] = true

		if c.ID != "" {
			if _, err := strconv.ParseUint(c.ID, 10, 64); err != nil {
				return fmt.Errorf("guild config: channel %q has id %q, which is "+
					"not a Discord snowflake", c.Name, c.ID)
			}
			if seenID[c.ID] != "" {
				return fmt.Errorf("guild config: channels %q and %q both claim "+
					"id %s", seenID[c.ID], c.Name, c.ID)
			}
			seenID[c.ID] = c.Name
		}

		for _, role := range c.VisibleTo {
			if !g.hasRole(role) {
				return fmt.Errorf("guild config: channel %q is visible_to %q, "+
					"which is not a declared role", c.Name, role)
			}
		}
	}
	return nil
}

func (g *Guild) hasRole(name string) bool {
	for _, r := range g.Roles {
		if r.Name == name {
			return true
		}
	}
	return false
}

// Categories returns the category names in the order their first channel
// appears, so the declared file order decides the sidebar.
func (g *Guild) Categories() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range g.Channels {
		if c.Category == "" || seen[c.Category] {
			continue
		}
		seen[c.Category] = true
		out = append(out, c.Category)
	}
	return out
}

// Surfaces maps a surface name to the channel that owns it. A surface declared
// on two channels is a conflict rather than a preference, because wly edits one
// pinned message per surface and could not choose.
func (g *Guild) Surfaces() (map[string]string, error) {
	out := map[string]string{}
	for _, c := range g.Channels {
		if c.Surface == "" {
			continue
		}
		if prev, dup := out[c.Surface]; dup {
			return nil, fmt.Errorf("guild config: surface %q is claimed by both "+
				"%q and %q", c.Surface, prev, c.Name)
		}
		out[c.Surface] = c.Name
	}
	return out, nil
}

// ParseColor turns "#RRGGBB" into the integer Discord wants. An empty string is
// zero, which is Discord's "no colour" and a legitimate choice.
func ParseColor(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	h := strings.TrimPrefix(s, "#")
	if len(h) != 6 {
		return 0, fmt.Errorf("colour %q is not #RRGGBB", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("colour %q is not hexadecimal", s)
	}
	return int(v), nil
}

// sortedNames is a stable order for reporting things that have no inherent one.
func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
