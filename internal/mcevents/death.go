package mcevents

import (
	"regexp"
	"strings"
)

// Death messages, which are the hard ones.
//
// Every other event in latest.log carries a marker: "joined the game", "has made
// the advancement", "UUID of player". A death has NONE. Minecraft writes the
// rendered sentence and nothing else, so "kon fell from a high place" is
// structurally identical to any other line a mod might print, and there is no
// prefix to key on.
//
// So the table is the vocabulary itself: the fixed opening of each vanilla death
// message. A player name cannot contain a space (Mojang allows [A-Za-z0-9_]),
// which is what makes "one non-space token, then a known death phrase" a safe
// shape to match.
//
// Chat is NOT at risk of matching, and that is worth stating because it is the
// obvious way this goes wrong: player chat is logged as "<kon> hello", with the
// angle brackets, so a message reading "was slain by a zombie" cannot be
// mistaken for a death. The patterns below anchor on the player name having no
// brackets around it.
//
// Adding 1.22's new death messages should be adding strings to this slice. If it
// ever needs a new function, the shape was wrong.
var deathPhrases = []string{
	// falling, and the many ways to arrange it
	"fell from a high place",
	"fell off a ladder",
	"fell off some vines",
	"fell off some weeping vines",
	"fell off some twisting vines",
	"fell off scaffolding",
	"fell while climbing",
	"fell out of the water",
	"fell into a patch of fire",
	"fell into a patch of cacti",
	"was doomed to fall",
	"was impaled on a stalagmite",
	"hit the ground too hard",
	"was squashed by a falling anvil",
	"was squashed by a falling block",
	"was skewered by a falling stalactite",

	// fire, lava, freezing
	"went up in flames",
	"burned to death",
	"was burnt to a crisp whilst fighting",
	"walked into a fire whilst fighting",
	"tried to swim in lava",
	"discovered the floor was lava",
	"walked into the danger zone due to",
	"froze to death",
	"was frozen to death by",

	// explosions and projectiles
	"blew up",
	"was blown up by",
	"went off with a bang",
	"was shot by",
	"was fireballed by",
	"was shot by a skull from",
	"was struck by lightning",
	"was obliterated by a sonically-charged shriek",

	// mobs and players
	"was slain by",
	"was stung to death",
	"was pummeled by",
	"was killed by",
	"was killed while trying to hurt",
	"was killed trying to hurt",
	"was impaled by",
	"was poked to death by a sweet berry bush",
	"was pricked to death",
	"walked into a cactus while trying to escape",
	"was roasted in dragon's breath",
	"was squished too much",
	"was squashed by",
	"didn't want to live in the same world as",

	// the environment
	"drowned",
	"suffocated in a wall",
	"was squeezed too tightly",
	"starved to death",
	"withered away",
	"experienced kinetic energy",
	"left the confines of this world",
	"fell out of the world",
	"was killed by even more magic",
	"was killed by magic",
	"died",
	"died because of",
}

// reDeath is built once from the table. Sorted longest-first so a specific
// phrase wins over a prefix of itself: "died because of" must not be reported as
// "died" with the reason dropped, and Go's alternation is leftmost-first rather
// than leftmost-longest, so the ORDER here is load-bearing rather than tidy.
var reDeath = buildDeathPattern()

func buildDeathPattern() *regexp.Regexp {
	phrases := append([]string(nil), deathPhrases...)
	// Longest first. sort.Slice would do, but this runs once at init and the
	// list is short enough that an insertion sort keeps the dependency list at
	// what it already is.
	for i := 1; i < len(phrases); i++ {
		for j := i; j > 0 && len(phrases[j]) > len(phrases[j-1]); j-- {
			phrases[j], phrases[j-1] = phrases[j-1], phrases[j]
		}
	}
	for i, p := range phrases {
		phrases[i] = regexp.QuoteMeta(p)
	}
	// [A-Za-z0-9_]{1,16} is exactly what Mojang permits in a name. Matching
	// \S+ instead would let a mod's bracketed prefix through and turn a log
	// line about a block entity into somebody's death.
	return regexp.MustCompile(
		`^\[[^\]]+\] \[[^\]]+\]: ([A-Za-z0-9_]{1,16}) ((?:` +
			strings.Join(phrases, "|") + `)\b.*)$`)
}

// parseDeath reads a death line. Detail is the whole sentence minus the name,
// because that sentence is the entire charm of the feed and rewriting it into a
// category would throw away the part players actually quote at each other.
func parseDeath(line string) (Event, bool) {
	m := reDeath.FindStringSubmatch(line)
	if m == nil {
		return Event{}, false
	}
	return Event{Kind: Died, Player: m[1], Detail: strings.TrimSpace(m[2])}, true
}
