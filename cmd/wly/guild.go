package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"weloveyou-mc/internal/discord"
)

// The Discord REST layer, in stdlib net/http for the same reason wly speaks the
// Docker Engine API that way rather than pulling in the docker client library:
// this is eleven plain JSON endpoints behind one header. A library earns its
// place when the gateway does, because a websocket with heartbeats, resume and
// interaction routing is genuinely worth not writing. Reconciling roles is not.
//
// Every decision lives in internal/discord, tested without a token. This file
// only fetches and applies, which is why cmd/wly's coverage floor is low.

// A var so tests can point it at httptest. No test touches the network.
var discordAPI = "https://discord.com/api/v10"

type discordClient struct {
	token string
	http  *http.Client
}

func newDiscordClient(token string) *discordClient {
	return &discordClient{token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// do performs one API call. The token goes in a header and is never logged;
// errors quote the response body, which Discord fills with useful detail, but
// the body of a request is not echoed back for the same reason.
func (c *discordClient) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, discordAPI+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/6586x57890143/weloveyou.mc, 0.2)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("rate limited by Discord on %s %s: %s", method, path, raw)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// The narrow shapes this needs. Discord returns far more; decoding only what is
// used means a new field upstream cannot break the reconciler.
type apiGuild struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Features []string `json:"features"`
}

type apiRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       int    `json:"color"`
	Hoist       bool   `json:"hoist"`
	Mentionable bool   `json:"mentionable"`
	Managed     bool   `json:"managed"`
	Position    int    `json:"position"`
}

type apiChannel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"` // 0 text, 4 category
	Topic    string `json:"topic"`
	ParentID string `json:"parent_id"`
}

type apiEmoji struct {
	Name string `json:"name"`
}

type apiMember struct {
	Roles []string `json:"roles"`
}

// fetchLive assembles the narrowed view internal/discord reconciles against.
func (c *discordClient) fetchLive(guildID string) (discord.Live, error) {
	var live discord.Live

	var g apiGuild
	if err := c.do("GET", "/guilds/"+guildID, nil, &g); err != nil {
		return live, err
	}
	live.ID, live.Name, live.Features = g.ID, g.Name, g.Features

	var roles []apiRole
	if err := c.do("GET", "/guilds/"+guildID+"/roles", nil, &roles); err != nil {
		return live, err
	}
	// Discord returns roles unordered with a position field, higher meaning
	// higher. internal/discord expects highest first, because that is how the
	// hierarchy reads in the client and in guild.toml.
	byID := map[string]apiRole{}
	for _, r := range roles {
		byID[r.ID] = r
	}
	sortRolesHighestFirst(roles)
	for _, r := range roles {
		live.Roles = append(live.Roles, discord.LiveRole{
			ID: r.ID, Name: r.Name, Color: r.Color,
			Hoist: r.Hoist, Mentionable: r.Mentionable, Managed: r.Managed,
		})
	}

	var chans []apiChannel
	if err := c.do("GET", "/guilds/"+guildID+"/channels", nil, &chans); err != nil {
		return live, err
	}
	catName := map[string]string{}
	for _, ch := range chans {
		if ch.Type == 4 {
			catName[ch.ID] = ch.Name
		}
	}
	for _, ch := range chans {
		if ch.Type == 4 {
			continue // categories are structure, not channels
		}
		live.Channels = append(live.Channels, discord.LiveChannel{
			ID: ch.ID, Name: ch.Name, Topic: ch.Topic, Category: catName[ch.ParentID],
		})
	}

	var emojis []apiEmoji
	if err := c.do("GET", "/guilds/"+guildID+"/emojis", nil, &emojis); err != nil {
		return live, err
	}
	for _, e := range emojis {
		live.Emojis = append(live.Emojis, e.Name)
	}

	// The bot's own highest role. Without this the hierarchy check cannot run,
	// and the hierarchy check is the one that stops a plan that silently no-ops.
	var me apiMember
	if err := c.do("GET", "/guilds/"+guildID+"/members/@me", nil, &me); err != nil {
		return live, err
	}
	best := -1
	for _, id := range me.Roles {
		if r, ok := byID[id]; ok && r.Position > best {
			best, live.BotHighestRole = r.Position, r.Name
		}
	}
	return live, nil
}

func sortRolesHighestFirst(roles []apiRole) {
	for i := 1; i < len(roles); i++ {
		for j := i; j > 0 && roles[j].Position > roles[j-1].Position; j-- {
			roles[j], roles[j-1] = roles[j-1], roles[j]
		}
	}
}

func runGuild(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("guild", flag.ContinueOnError)
	fs.SetOutput(out)
	config := fs.String("config", "guild.toml", "the declared server")
	apply := fs.Bool("apply", false, "make the changes, rather than only printing them")
	if err := fs.Parse(args); err != nil {
		return err
	}

	want, err := discord.Load(*config)
	if err != nil {
		return err
	}

	token := os.Getenv("WLY_DISCORD_TOKEN")
	if token == "" {
		return fmt.Errorf("WLY_DISCORD_TOKEN is not set. Put the bot token in " +
			"deploy/.env or export it in your shell; it is never read from guild.toml, " +
			"because that file is committed and a token in it would be public")
	}

	live, err := newDiscordClient(token).fetchLive(want.Meta.ID)
	if err != nil {
		return err
	}
	plan, err := discord.Compute(want, live)
	if err != nil {
		return err
	}
	fmt.Fprint(out, discord.Render(plan))

	if !*apply {
		if !plan.Empty() {
			fmt.Fprintln(out, "\nThis printed the plan and changed nothing. "+
				"Re-run with --apply to do it.")
		}
		return nil
	}
	return fmt.Errorf("--apply is not implemented yet: plan against the real server " +
		"first and read what it says before anything writes")
}
