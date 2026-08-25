package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/BurntSushi/toml"

	"weloveyou-mc/internal/discord"
)

// `wly surfaces` puts the pinned messages in the channels and keeps them there.
//
// One message per surface, edited in place forever. That is not a preference:
// Components V2 sets IS_COMPONENTS_V2 irreversibly on a message, and a channel
// full of superseded status boards is worse than no status board, so the first
// post is a one-way door walked through on purpose.
//
// Which message to edit is not stored anywhere. Discord already knows: the
// surface is the message in that channel authored by wly. A database row holding
// a message id would be a second source of truth that can disagree with the
// channel, and the failure mode is editing a message that no longer exists while
// a stale one sits pinned in front of everyone.

// surfaceConfig is the handful of facts the surfaces state, read from wly.toml.
//
// They live in config rather than in the builders because a surface that
// hardcodes a URL cannot be corrected without a release, and the addresses here
// are exactly the ones that move.
type surfaceConfig struct {
	Surfaces struct {
		ServerAddress string `toml:"server_address"`
		MapURL        string `toml:"map_url"`
		InstanceZip   string `toml:"instance_zip"`
		PackPage      string `toml:"pack_page"`
		PrismImage    string `toml:"prism_image"`
		MapImage      string `toml:"map_image"`
	} `toml:"surfaces"`
	Channels map[string]struct {
		MCVersion string `toml:"mc_version"`
		PackURL   string `toml:"pack_url"`
	} `toml:"channels"`
}

func runSurfaces(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("surfaces", flag.ContinueOnError)
	fs.SetOutput(out)
	guildPath := fs.String("config", "guild.toml", "the declared server")
	wlyPath := fs.String("wly", "wly.toml", "where the surface facts live")
	apply := fs.Bool("apply", false, "post and edit, rather than only printing what would change")
	only := fs.String("only", "", "one surface name, rather than all of them")
	if err := fs.Parse(args); err != nil {
		return err
	}

	g, err := discord.Load(*guildPath)
	if err != nil {
		return err
	}
	var cfg surfaceConfig
	if _, err := toml.DecodeFile(*wlyPath, &cfg); err != nil {
		return fmt.Errorf("surface config: %w", err)
	}

	token := os.Getenv("WLY_DISCORD_TOKEN")
	if token == "" {
		token = fromEnvFile("deploy/.env", "WLY_DISCORD_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("WLY_DISCORD_TOKEN is not set; see `wly guild` for where it goes")
	}
	c := newDiscordClient(token)

	// Emoji ids, so <:heart:> becomes something Discord renders rather than
	// literal text in front of everyone.
	var emojis []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do("GET", "/guilds/"+g.Meta.ID+"/emojis", nil, &emojis); err != nil {
		return err
	}
	ids := map[string]string{}
	for _, e := range emojis {
		ids[e.Name] = e.ID
	}

	built, skipped := buildSurfaces(g, cfg)
	for _, s := range skipped {
		fmt.Fprintf(out, "  - %s: %s\n", s.name, s.why)
	}

	for _, s := range built {
		if *only != "" && s.name != *only {
			continue
		}
		payload, err := discord.ResolveEmoji(s.payload, ids)
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		if !*apply {
			fmt.Fprintf(out, "  + %s -> %s\n", s.name, s.channel)
			continue
		}
		action, err := c.upsertSurface(s.channelID, payload)
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		fmt.Fprintf(out, "  %s %s in %s\n", action, s.name, s.channel)
	}
	if !*apply && len(built) > 0 {
		fmt.Fprintln(out, "\nThis printed what it would post and posted nothing. "+
			"Re-run with --apply to do it.")
	}
	return nil
}

type builtSurface struct {
	name      string
	channel   string
	channelID string
	payload   discord.Payload
}

type skippedSurface struct{ name, why string }

// buildSurfaces turns the declared channels into payloads, and says out loud
// which surfaces it cannot build yet.
//
// Skipping loudly matters more than it looks. A surface that silently does not
// appear is indistinguishable from one that failed to post, and half of these
// are waiting on data sources that genuinely do not exist yet (RCON, the log
// bridge, the cost report, which is on the box and not here).
func buildSurfaces(g *discord.Guild, cfg surfaceConfig) ([]builtSurface, []skippedSurface) {
	var built []builtSurface
	var skipped []skippedSurface

	stable := cfg.Channels["stable"]
	for _, ch := range g.Channels {
		if ch.Surface == "" {
			continue
		}
		add := func(p discord.Payload) {
			built = append(built, builtSurface{ch.Surface, ch.Name, ch.ID, p})
		}
		switch ch.Surface {
		case "getstarted":
			add(discord.GetStarted(discord.GetStartedData{
				MinecraftVersion: stable.MCVersion,
				InstanceZipURL:   cfg.Surfaces.InstanceZip,
				PackPageURL:      cfg.Surfaces.PackPage,
				ServerAddress:    cfg.Surfaces.ServerAddress,
				PrismImageURL:    cfg.Surfaces.PrismImage,
			}))
		case "map":
			// The card is a Media Gallery around a render that nothing publishes
			// yet. Posting it would put a broken image in front of every player,
			// so it waits for the renderer rather than shipping a hole.
			if cfg.Surfaces.MapImage == "" {
				skipped = append(skipped, skippedSurface{ch.Surface,
					"no map render is published yet, so the card would be a broken image"})
				continue
			}
			add(discord.Map(discord.MapData{
				ImageURL: cfg.Surfaces.MapImage,
				MapURL:   cfg.Surfaces.MapURL,
			}))
		case "status":
			skipped = append(skipped, skippedSurface{ch.Surface,
				"needs RCON for players and the world day, and the log tail for TPS " +
					"and MSPT. Neither is written yet, and a board that invents them is worse than none"})
		case "spend":
			skipped = append(skipped, skippedSurface{ch.Surface,
				"reads /var/lib/wly/cost.json, which exists on the box and not in a checkout"})
		case "release":
			skipped = append(skipped, skippedSurface{ch.Surface,
				"a release is an event, so it is posted by the release path rather than reconciled"})
		case "events":
			skipped = append(skipped, skippedSurface{ch.Surface,
				"the feed is a log of posts, not one pinned message, and it needs the log bridge"})
		default:
			skipped = append(skipped, skippedSurface{ch.Surface, "no builder for this surface"})
		}
	}
	return built, skipped
}

// upsertSurface posts the surface, or edits the one already there.
//
// It finds wly's own message rather than remembering an id. Discord is the
// store; anything else is a second source of truth that can disagree with what
// people are actually looking at.
func (c *discordClient) upsertSurface(channelID string, p discord.Payload) (string, error) {
	var self struct {
		ID string `json:"id"`
	}
	if err := c.do("GET", "/users/@me", nil, &self); err != nil {
		return "", err
	}

	var msgs []struct {
		ID     string `json:"id"`
		Author struct {
			ID string `json:"id"`
		} `json:"author"`
	}
	if err := c.do("GET", "/channels/"+channelID+"/messages?limit=50", nil, &msgs); err != nil {
		return "", err
	}
	// Oldest of ours, so a surface stays put rather than walking down the
	// channel if a second one ever gets posted by accident. Discord returns
	// newest first.
	mine := ""
	for _, m := range msgs {
		if m.Author.ID == self.ID {
			mine = m.ID
		}
	}

	if mine != "" {
		if err := c.do("PATCH", "/channels/"+channelID+"/messages/"+mine, p, nil); err != nil {
			return "", err
		}
		return "edited", nil
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := c.do("POST", "/channels/"+channelID+"/messages", p, &created); err != nil {
		return "", err
	}
	// Pinning is a convenience and not the mechanism: the surface is found by
	// author, not by pin. A guild at its pin limit must not fail the post.
	if err := c.do("PUT", "/channels/"+channelID+"/pins/"+created.ID, nil, nil); err != nil {
		return "posted (but could not pin: " + err.Error() + ")", nil
	}
	return "posted", nil
}
