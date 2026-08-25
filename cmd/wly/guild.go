package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

	// Bounded: an unbounded read means a hostile or broken response can balloon
	// a 512m container. 4 MiB is far past anything Discord returns for these.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
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
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       int            `json:"type"` // 0 text, 4 category
	Topic      string         `json:"topic"`
	ParentID   string         `json:"parent_id"`
	Overwrites []apiOverwrite `json:"permission_overwrites"`
}

// apiOverwrite is one permission overwrite. Allow and Deny are strings because
// Discord sends permission bitfields that way: they exceed what a JSON number
// is guaranteed to carry, so decoding them as numbers loses the high bits.
type apiOverwrite struct {
	ID    string `json:"id"`
	Type  int    `json:"type"`
	Allow string `json:"allow"`
	Deny  string `json:"deny"`
}

type apiEmoji struct {
	Name string `json:"name"`
}

type apiMember struct {
	Roles []string `json:"roles"`
	User  struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"user"`
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
	roleName := map[string]string{}
	for _, r := range roles {
		byID[r.ID] = r
		roleName[r.ID] = r.Name
	}
	sortRolesHighestFirst(roles)
	for _, r := range roles {
		live.Roles = append(live.Roles, discord.LiveRole{
			ID: r.ID, Name: r.Name, Color: r.Color, Position: r.Position,
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
		lc := discord.LiveChannel{
			ID: ch.ID, Name: ch.Name, Topic: ch.Topic, Category: catName[ch.ParentID],
		}
		for _, o := range ch.Overwrites {
			// A bitfield Discord cannot parse is not a reason to refuse the
			// whole reconcile; it reads as no bits, which the diff then reports.
			allow, _ := strconv.ParseInt(o.Allow, 10, 64)
			deny, _ := strconv.ParseInt(o.Deny, 10, 64)
			lc.Overwrites = append(lc.Overwrites, discord.LiveOverwrite{
				ID: o.ID, Type: o.Type, Role: roleName[o.ID], Allow: allow, Deny: deny,
			})
		}
		live.Channels = append(live.Channels, lc)
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
	//
	// Two calls, because `@me` is not a snowflake here. GET
	// /guilds/{id}/members/@me returns 400 NUMBER_TYPE_COERCE: only PATCH takes
	// @me on that route, and the GET form that does take it,
	// /users/@me/guilds/{id}/member, needs the guilds.members.read OAuth2 scope
	// that a bot token does not carry. So: ask who we are, then look ourselves up.
	var self struct {
		ID string `json:"id"`
	}
	if err := c.do("GET", "/users/@me", nil, &self); err != nil {
		return live, err
	}
	var me apiMember
	if err := c.do("GET", "/guilds/"+guildID+"/members/"+self.ID, nil, &me); err != nil {
		return live, err
	}
	// Members, so the bot gate can report an application already wearing a
	// managed role. Needs the Server Members privileged intent, which is on.
	// A failure here is NOT fatal: it costs the warning, not the reconcile, and
	// refusing to plan because one optional read failed would be worse than
	// planning without it. The plan says so rather than going quiet.
	var members []apiMember
	if err := c.do("GET", "/guilds/"+guildID+"/members?limit=1000", nil, &members); err != nil {
		live.MembersUnavailable = err.Error()
	} else {
		for _, m := range members {
			lm := discord.LiveMember{ID: m.User.ID, Name: m.User.Username, Bot: m.User.Bot}
			for _, id := range m.Roles {
				if n, ok := roleName[id]; ok {
					lm.Roles = append(lm.Roles, n)
				}
			}
			live.Members = append(live.Members, lm)
		}
	}

	best := -1
	for _, id := range me.Roles {
		if r, ok := byID[id]; ok && r.Position > best {
			best, live.BotHighestRole = r.Position, r.Name
			live.BotHighestPosition = r.Position
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
	roles := fs.Bool("roles", false, "print the live role list with positions and stop")
	icons := fs.String("icons", "icons.toml", "the pixel icon set uploaded as emoji")
	if err := fs.Parse(args); err != nil {
		return err
	}

	want, err := discord.Load(*config)
	if err != nil {
		return err
	}

	token := os.Getenv("WLY_DISCORD_TOKEN")
	if token == "" {
		token = fromEnvFile("deploy/.env", "WLY_DISCORD_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("WLY_DISCORD_TOKEN is not set. Put the bot token in " +
			"deploy/.env, which is gitignored, or export it in your shell. It is " +
			"never read from guild.toml, because that file is committed and a token " +
			"in it would be public")
	}

	live, err := newDiscordClient(token).fetchLive(want.Meta.ID)
	if err != nil {
		return err
	}
	if *roles {
		fmt.Fprintf(out, "%-28s %8s  %s\n", "ROLE", "POSITION", "NOTE")
		for _, r := range live.Roles {
			note := ""
			if r.Managed {
				note = "managed by Discord"
			}
			if r.Name == live.BotHighestRole {
				note = "<- the bot's highest"
			}
			fmt.Fprintf(out, "%-28s %8d  %s\n", r.Name, r.Position, note)
		}
		return nil
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
	if plan.Empty() {
		return nil
	}
	fmt.Fprintln(out, "\napplying:")
	// applyPlan reads the plan for what to do, so an empty one is genuinely
	// nothing to do. Role ORDER used to be the exception: it was applied as a
	// side effect of creating roles rather than being in the plan, so once
	// everything else matched, a wrong hierarchy could never be corrected. It
	// is a ReorderRoles action now, which is why the shortcut above is safe.
	return newDiscordClient(token).applyPlan(want, live, plan, *icons, out)
}

// fromEnvFile reads one key out of a compose .env file.
//
// This exists because the error message told people to put the token in
// deploy/.env and then did not read it: inside the container compose passes it
// through as a real environment variable, but `wly guild` is run by hand from a
// checkout, where nothing had. An instruction that does not work is worse than
// no instruction.
//
// Deliberately not a dotenv library, and deliberately not exported: it reads
// KEY=VALUE, skips blanks and comments, strips one layer of matching quotes, and
// does not expand anything. A value with a `#` in it survives, because a bot
// token can contain almost anything and stripping trailing comments would eat it.
func fromEnvFile(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		return val
	}
	return ""
}

// roleBody is the JSON for one role, shared by create and update so the two
// cannot describe the same role differently.
//
// Permissions are deliberately not sent. The only role in guild.toml declaring
// any is `admin`, which is `manual`, and a committed config file is the wrong
// authority on who is an administrator.
func roleBody(r discord.Role, gradients bool) map[string]any {
	colour, _ := discord.ParseColor(r.Color)
	body := map[string]any{"name": r.Name, "color": colour,
		"hoist": r.Hoist, "mentionable": r.Mentionable}
	if r.Colors != nil && gradients {
		cols := map[string]any{}
		for k, v := range map[string]string{"primary_color": r.Colors.Primary,
			"secondary_color": r.Colors.Secondary, "tertiary_color": r.Colors.Tertiary} {
			if v == "" {
				continue
			}
			n, _ := discord.ParseColor(v)
			cols[k] = n
		}
		body["colors"] = cols
	}
	return body
}

// overwriteBody is what a channel's declared visibility becomes on the wire,
// merged over whatever the channel already carries.
//
// The merge is the point. Discord's PATCH replaces the entire overwrite set, so
// sending only the declared ones would strip a moderator role's access, or one
// person's, as a side effect of correcting a topic. Apply never deletes.
func overwriteBody(ch discord.Channel, cur discord.LiveChannel, roleID map[string]string, botRole string) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, o := range discord.MergeOverwrites(ch.Overwrites(botRole), cur.Overwrites) {
		id := o.ID
		if id == "" {
			var ok bool
			if id, ok = roleID[o.Role]; !ok {
				return nil, fmt.Errorf("channel %s names role %q, which has no id", ch.Name, o.Role)
			}
		}
		out = append(out, map[string]any{
			"id": id, "type": o.Type,
			"allow": strconv.FormatInt(o.Allow, 10),
			"deny":  strconv.FormatInt(o.Deny, 10),
		})
	}
	return out, nil
}

// applyPlan performs the plan. It creates and updates; it never deletes, which
// is enforced upstream by drift being a separate field on the Plan that this
// function cannot see.
//
// Order matters and is not the order the plan prints in:
//  1. roles, because a channel's permission overwrites name them
//  2. role positions, because Discord puts every new role at the bottom and the
//     hierarchy is guild.toml's order, not creation order
//  3. categories, because channels reference them by id
//  4. channels, with their overwrites resolved from the role ids created in 1
func (c *discordClient) applyPlan(g *discord.Guild, live discord.Live, p *discord.Plan, iconPath string, out io.Writer) error {
	roleID := map[string]string{discord.Everyone: g.Meta.ID} // @everyone's id IS the guild id
	for _, r := range live.Roles {
		roleID[r.Name] = r.ID
	}
	catID := map[string]string{}
	// Resolved the way the planner resolved it, by id first, so apply and plan
	// can never disagree about which channel a declared one means.
	liveChan := map[string]discord.LiveChannel{}
	for _, ch := range g.Channels {
		if cur, ok := discord.MatchChannel(ch, live); ok {
			liveChan[ch.Name] = cur
		}
	}

	todo := map[discord.Kind]map[string]bool{}
	for _, a := range p.Actions {
		if todo[a.Kind] == nil {
			todo[a.Kind] = map[string]bool{}
		}
		todo[a.Kind][a.Target] = true
	}

	// 1. roles
	for _, r := range g.Roles {
		if !todo[discord.CreateRole][r.Name] {
			continue
		}
		var created apiRole
		if err := c.do("POST", "/guilds/"+g.Meta.ID+"/roles",
			roleBody(r, p.GradientsAvailable), &created); err != nil {
			return fmt.Errorf("create role %s: %w", r.Name, err)
		}
		roleID[r.Name] = created.ID
		fmt.Fprintf(out, "  created role %s\n", r.Name)
	}

	// 1b. roles that exist and differ. This branch used to be missing: Compute
	// emitted UpdateRole actions and Render printed them, but apply read the
	// same map and had no case for them, so --apply announced work and did
	// none. A colour or hoist change to a role that already existed could not be
	// made by this tool at all, and nothing said so.
	for _, r := range g.Roles {
		if !todo[discord.UpdateRole][r.Name] {
			continue
		}
		id, ok := roleID[r.Name]
		if !ok {
			return fmt.Errorf("update role %s: it has no id, which cannot happen "+
				"for a role the plan says already exists", r.Name)
		}
		if err := c.do("PATCH", "/guilds/"+g.Meta.ID+"/roles/"+id,
			roleBody(r, p.GradientsAvailable), nil); err != nil {
			return fmt.Errorf("update role %s: %w", r.Name, err)
		}
		fmt.Fprintf(out, "  updated role %s\n", r.Name)
	}

	// 2. hierarchy. guild.toml is highest first, and Discord's position is
	// higher-is-higher, so the first declared role gets the largest number. The
	// bot's own role must stay above all of them, so counting starts below it.
	// Discord gives every newly created role position 1 and does not renumber
	// the rest, so a fresh guild reports a pile of ties and any arithmetic on
	// "the bot's position" is reading noise. An earlier version refused to
	// reorder based on exactly that number and was wrong on a real server.
	//
	// So: attempt it, and let Discord be the authority. It refuses to move any
	// role above the caller's own highest, and the PATCH is all-or-nothing, so a
	// bot that is not on top changes nothing and says so. That is a better test
	// than anything guessable from here.
	var positions []map[string]any
	for i, r := range g.Roles {
		if id, ok := roleID[r.Name]; ok {
			positions = append(positions, map[string]any{"id": id, "position": len(g.Roles) - i})
		}
	}
	if len(positions) > 0 {
		if err := c.do("PATCH", "/guilds/"+g.Meta.ID+"/roles", positions, nil); err != nil {
			fmt.Fprintf(out, "  ! could not set role order: %v\n"+
				"    The roles exist and work; only their order is pending. Discord\n"+
				"    refuses to move a role above the bot's own, so drag %q to the top\n"+
				"    of Server Settings -> Roles and re-run.\n", err, live.BotHighestRole)
		} else {
			fmt.Fprintln(out, "  set role order from guild.toml")
		}
	}

	// 3. categories
	for _, ch := range live.Channels {
		if ch.Category != "" {
			catID[ch.Category] = "" // filled below only for ones we create
		}
	}
	for _, cat := range g.Categories() {
		if !todo[discord.CreateCategory][cat] {
			continue
		}
		var created apiChannel
		body := map[string]any{"name": cat, "type": 4}
		if err := c.do("POST", "/guilds/"+g.Meta.ID+"/channels", body, &created); err != nil {
			return fmt.Errorf("create category %s: %w", cat, err)
		}
		catID[cat] = created.ID
		fmt.Fprintf(out, "  created category %s\n", cat)
	}

	// 4. channels
	for _, ch := range g.Channels {
		overwrites, err := overwriteBody(ch, liveChan[ch.Name], roleID, live.BotHighestRole)
		if err != nil {
			return err
		}

		if todo[discord.CreateChannel][ch.Name] {
			body := map[string]any{"name": ch.Name, "type": 0, "topic": ch.Topic}
			if id := catID[ch.Category]; id != "" {
				body["parent_id"] = id
			}
			if len(overwrites) > 0 {
				body["permission_overwrites"] = overwrites
			}
			var created apiChannel
			if err := c.do("POST", "/guilds/"+g.Meta.ID+"/channels", body, &created); err != nil {
				return fmt.Errorf("create channel %s: %w", ch.Name, err)
			}
			fmt.Fprintf(out, "  created channel %s, id %s. Put that in guild.toml: until it is there, a rename cannot be told from a new channel.\n",
				ch.Name, created.ID)
			continue
		}

		if todo[discord.UpdateChannel][ch.Name] {
			// name is sent because a rename is a real diff now that channels
			// are matched by id. Without it, renaming one in guild.toml would
			// plan a change that apply then quietly did not make.
			body := map[string]any{"name": ch.Name, "topic": ch.Topic}
			if id := catID[ch.Category]; id != "" {
				body["parent_id"] = id
			}
			if len(overwrites) > 0 {
				body["permission_overwrites"] = overwrites
			}
			if err := c.do("PATCH", "/channels/"+liveChan[ch.Name].ID, body, nil); err != nil {
				// Not fatal, for the same reason a refused reorder is not: one
				// channel wly cannot reach must not stop the seven it can. The
				// advice is chosen from the error, because a fixed block of it
				// under the wrong failure is worse than none.
				fmt.Fprintf(out, "  ! could not update %s: %v\n", ch.Name, err)
				switch {
				case strings.Contains(err.Error(), "50001"):
					// wly is locked out and cannot grant itself access to a channel
					// it cannot already see: the request that would fix it is the
					// one being refused. Channels created from here on carry the
					// grant from birth, so this only means one that predates it.
					fmt.Fprintf(out, "    Open that channel's Permissions, add the %q\n"+
						"    role and allow View Channel, then re-run.\n", live.BotHighestRole)
				case strings.Contains(err.Error(), "rate limited"):
					// Measured on the live guild: renaming a channel is limited to
					// TWO changes per ten minutes PER CHANNEL, and the wait is
					// minutes rather than seconds. Nothing is broken and nothing
					// needs fixing; the plan is simply not finished yet.
					fmt.Fprint(out, "    Renaming a channel is limited to two changes per\n"+
						"    ten minutes, per channel. Nothing is wrong: wait out the\n"+
						"    retry_after above and run --apply again.\n")
				}
				continue
			}
			fmt.Fprintf(out, "  updated channel %s\n", ch.Name)
		}
	}

	// 5. emoji. The grids live in icons.toml, read by both this and
	// scripts/pixelicons.py, so what the pages draw and what Discord shows
	// cannot drift apart.
	if n := len(todo[discord.UploadEmoji]); n > 0 {
		ic, err := discord.LoadIcons(iconPath)
		if err != nil {
			return fmt.Errorf("%d emoji not uploaded: %w", n, err)
		}
		for _, name := range g.Emojis.Upload {
			if !todo[discord.UploadEmoji][name] {
				continue
			}
			uri, err := ic.DataURI(name, 128)
			if err != nil {
				return fmt.Errorf("render emoji %s: %w", name, err)
			}
			body := map[string]any{"name": name, "image": uri}
			if err := c.do("POST", "/guilds/"+g.Meta.ID+"/emojis", body, nil); err != nil {
				return fmt.Errorf("upload emoji %s: %w", name, err)
			}
			fmt.Fprintf(out, "  uploaded emoji %s\n", name)
		}
	}
	return nil
}
