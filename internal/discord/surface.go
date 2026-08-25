package discord

import (
	"fmt"
	"strings"
	"time"
)

// The surfaces: the messages wly owns and edits in place.
//
// Each one is built here, in the pure half, and compared in tests against the
// payload committed under testdata/surfaces. Those files are simultaneously the
// design mockup (scripts/discord-mocks.py renders them) and the golden file, so
// a layout change is a reviewable diff rather than a surprise in a live channel.
// D0 designed them; this is the "make Go emit these bytes" half.
//
// Nothing here fetches anything. Every number arrives as a field on a Data
// struct, which is what stops a surface showing a figure nothing can produce.

// FlagComponentsV2 is IS_COMPONENTS_V2. It is IRREVERSIBLE on a message: once
// set, that message can never carry content, embeds, polls or stickers again.
// Every surface sets it and every surface is edited in place forever, so the
// one-way door is walked through deliberately on the first post.
const FlagComponentsV2 = 1 << 15

// Component type numbers, named because a bare 9 in a constructor is unreadable
// and a wrong one is a message Discord rejects.
const (
	typeButton       = 2
	typeSection      = 9
	typeTextDisplay  = 10
	typeMediaGallery = 12
	typeSeparator    = 14
	typeContainer    = 17
)

// Button styles. 5 is a link: it carries a url and NO custom_id, so it needs no
// interaction handler at all. 1 is primary and does carry one, which commits the
// bot to handling it. An undeclared custom_id is a button that does nothing when
// pressed, and a player has already committed by then.
const (
	stylePrimary = 1
	styleLink    = 5
)

// Accent colours. The whole colour system is one rule: accent encodes state,
// never decoration. Values are the palette in scripts/brand.py, Minecraft's own
// chat colours desaturated for a dark background.
const (
	AccentHeart = 0xE39AAE // the welcome, and the only pink thing
	AccentInfo  = 0x84A69C // healthy, ambient, never urgent
	AccentBase  = 0xD8A657 // something changed, read it; also over budget
	AccentLose  = 0xC4705C // down, failed, dead
	AccentWin   = 0x8FA860 // an advancement
	AccentDim   = 0x8E8677 // fine, nothing to do
)

// Component is one Components V2 component.
//
// One struct for every type rather than an interface per type, because the API
// is a tagged union and this is the shape it serialises as. Every optional field
// is omitempty so a Text Display does not emit a null accessory, which Discord
// rejects.
type Component struct {
	Type        int         `json:"type"`
	AccentColor *int        `json:"accent_color,omitempty"`
	Content     string      `json:"content,omitempty"`
	Components  []Component `json:"components,omitempty"`
	Accessory   *Component  `json:"accessory,omitempty"`
	Divider     *bool       `json:"divider,omitempty"`
	Spacing     int         `json:"spacing,omitempty"`
	Style       int         `json:"style,omitempty"`
	Label       string      `json:"label,omitempty"`
	URL         string      `json:"url,omitempty"`
	CustomID    string      `json:"custom_id,omitempty"`
	Items       []MediaItem `json:"items,omitempty"`
}

type MediaItem struct {
	Media       Media  `json:"media"`
	Description string `json:"description,omitempty"`
}

type Media struct {
	URL string `json:"url"`
}

// Payload is what gets POSTed or PATCHed.
type Payload struct {
	Flags      int         `json:"flags"`
	Components []Component `json:"components"`
}

// Text is a Text Display.
func Text(format string, a ...any) Component {
	return Component{Type: typeTextDisplay, Content: fmt.Sprintf(format, a...)}
}

// Section is 1 to 3 Text Displays plus EXACTLY ONE accessory. Discord rejects
// any other shape, and a Section is the only way to put something on the
// right-hand side of a block.
func Section(accessory Component, texts ...Component) Component {
	return Component{Type: typeSection, Components: texts, Accessory: &accessory}
}

// Separator. A divider draws a line; spacing 1 is small, 2 is large. A
// non-divider separator with large spacing is how a footer gets air without a
// rule above it.
func Separator(divider bool, spacing int) Component {
	return Component{Type: typeSeparator, Divider: &divider, Spacing: spacing}
}

// Gallery is a Media Gallery. The description is alt text, and it is not
// optional politeness: a screenshot of a launcher window is load-bearing
// instruction for anyone who cannot see it.
func Gallery(url, description string) Component {
	return Component{Type: typeMediaGallery,
		Items: []MediaItem{{Media: Media{URL: url}, Description: description}}}
}

// LinkButton needs no interaction handler, which is why the surfaces reach for
// it first.
func LinkButton(label, url string) Component {
	return Component{Type: typeButton, Style: styleLink, Label: label, URL: url}
}

// ActionButton commits the bot to handling custom_id, and it must be declared in
// guild.toml's [interactions] or it is a button that does nothing.
func ActionButton(label, customID string) Component {
	return Component{Type: typeButton, Style: stylePrimary, Label: label, CustomID: customID}
}

// Container is the outer block, and its accent colour is the only colour lever a
// Components V2 message has.
func Container(accent int, children ...Component) Payload {
	return Payload{
		Flags:      FlagComponentsV2,
		Components: []Component{{Type: typeContainer, AccentColor: &accent, Components: children}},
	}
}

// Rel is Discord's relative timestamp. It ticks client-side, which is what keeps
// a board honest in between the edits that refresh it.
func Rel(t time.Time) string { return fmt.Sprintf("<t:%d:R>", t.Unix()) }

// Date is Discord's date-only timestamp, rendered in the reader's own locale.
func Date(t time.Time) string { return fmt.Sprintf("<t:%d:D>", t.Unix()) }

// ResolveEmoji rewrites every <:name:> placeholder into the <:name:id> form
// Discord actually renders.
//
// The payloads carry the placeholder because a design file cannot know the ids:
// they differ per guild and are assigned at upload. An unresolved placeholder is
// an error rather than something to send, because Discord renders it as that
// literal text. The failure mode is a surface that looks broken to every player,
// which is worse than one that did not post at all.
func ResolveEmoji(p Payload, ids map[string]string) (Payload, error) {
	var missing []string
	var walk func(cs []Component) []Component
	walk = func(cs []Component) []Component {
		out := make([]Component, len(cs))
		for i, c := range cs {
			for _, name := range placeholders(c.Content) {
				id, ok := ids[name]
				if !ok {
					missing = append(missing, name)
					continue
				}
				c.Content = strings.ReplaceAll(c.Content,
					"<:"+name+":>", "<:"+name+":"+id+">")
			}
			c.Components = walk(c.Components)
			if c.Accessory != nil {
				a := walk([]Component{*c.Accessory})[0]
				c.Accessory = &a
			}
			out[i] = c
		}
		return out
	}
	p.Components = walk(p.Components)
	if len(missing) > 0 {
		return p, fmt.Errorf("these custom emoji are not uploaded to the guild: %s. "+
			"Run `wly guild --apply` to upload them. Discord renders an unresolved "+
			"placeholder as literal text, so posting this would look broken to "+
			"everyone who reads it", strings.Join(sortedUnique(missing), ", "))
	}
	return p, nil
}

// placeholders finds every <:name:> in a string.
func placeholders(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "<:")
		if i < 0 {
			return out
		}
		s = s[i+2:]
		j := strings.Index(s, ":>")
		if j < 0 {
			return out
		}
		if name := s[:j]; name != "" && !strings.ContainsAny(name, " <>:") {
			out = append(out, name)
		}
		s = s[j+2:]
	}
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	for _, s := range in {
		seen[s] = true
	}
	return sortedNames(seen)
}
