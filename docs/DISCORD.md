# The Discord layer 💖

The design reference for `wly`'s Discord half: what the surfaces are, what
colour means, what a supporter can be sold, and what the API will and will not
let us build.

**Nothing here is implemented yet.** This is phase D0, the design pass, and the
master plan is explicit that it writes no Go. What exists today is this file,
[`../guild.toml`](../guild.toml), the payloads under
`../internal/discord/testdata/surfaces/`, and
[`../scripts/discord-mocks.py`](../scripts/discord-mocks.py) which renders and
validates them.

*Last swept 2026-08-24.*

## The idea in one line

The Discord server stops being where people talk about the Minecraft server and
becomes the client for it.

## Limits that shape everything

Checked against the current Discord API reference on 2026-08-24, not recalled.
Every number here is enforced by the API, and each one already decided something
below.

| | limit |
|---|---|
| **`IS_COMPONENTS_V2`** | `1 << 15` = `32768`, and **irreversible once set on a message**. The status board is edited in place forever, so this is a one-way door on its very first post. |
| **What V2 disables** | `content`, `embeds`, `poll`, `stickers`. No embed sidebar, no field grid, no footer. `Container.accent_color` is the only colour lever in a message. |
| **Component budget** | The reference says 40 total; the changelog says 10 top-level / 30 total. We design to the tighter pair and the validator enforces it. |
| **Section** | 1–3 Text Display children, and **exactly one** accessory: a Button or a Thumbnail, nothing else. |
| **Text** | 4000 characters total across every Text Display in the message. |
| **Markdown** | `#`/`##`/`###`, `-#` subtext, `>` quotes, lists, code fences. **Tables do not render.** Alignment comes from a code fence or dotted leaders. |
| **Linked roles** | **Maximum 5 metadata records per application.** Needs `role_connections.write` and a verification URL. |
| **Gradient roles** | `colors{primary,secondary,tertiary}`. secondary makes a gradient, tertiary makes it holographic. Both need the `ENHANCED_ROLE_COLORS` guild feature, which is boost-gated. Flat `color` is deprecated but works, so it is the fallback. |
| **Activities** | Sandboxed. Need an HTTPS origin, URL mappings in the dev portal, and Activities enabled on the app. External fetches hit `blocked:csp` unless proxied through `patchUrlMappings`. |

## Colour means state, never decoration

That is the whole colour system. The palette is not new. It is the one
[`../scripts/bench-site.py`](../scripts/bench-site.py) already reasons about at
lines 31–52: Minecraft's own chat colours, desaturated for reading on a dark
background. A second identity for Discord would just be a second thing to keep
in sync.

| token | hex | `accent_color` | means |
|---|---|---|---|
| `--heart` | `#E39AAE` | `14916270` | the welcome, and supporters. The only pink. |
| `--info` | `#84A69C` | `8693404` | ambient, healthy, never urgent |
| `--base` | `#D8A657` | `14198359` | something changed, or a warning |
| `--win` | `#8FA860` | `9414752` | good news: an advancement, a passing check |
| `--lose` | `#C4705C` | `12873820` | a death, a failure, the server being down |
| `--dim` | `#8E8677` | `9340535` | routine, nothing to see |

Applied to the six surfaces:

| surface | accent | when |
|---|---|---|
| get started | `--heart` | always |
| status board | `--info` → `--base` → `--lose` | healthy · degraded · down |
| pack release | `--base` | always: a release always means read me |
| event feed | `--win` / `--lose` / `--heart` | advancement · death · first join |
| map card | `--info` | always |
| daily spend | `--dim` → `--base` → `--lose` | fine · over budget · stale report |

The MOTD in [`../deploy/docker-compose.yml`](../deploy/docker-compose.yml)
already fixes the voice: lowercase, warm, a heart, and a joke at the end.

## The six surfaces

Five come from the master plan. **The sixth, the daily spend post, does not.**
CLAUDE.md requires `wly` to surface `/var/lib/wly/cost.json` in Discord and the
plan's list quietly omitted it. It is designed here so the bot has nothing to
invent, and it reuses the escalation rules `cost-push.sh` already implements on
the box: high priority on a missing or null report, a report older than 36h, a
day above 2× the running average, or a month-end projection above
`WLY_COST_BUDGET`. A null amount is *unknown*, never zero.

Each surface is a JSON payload under `../internal/discord/testdata/surfaces/`,
which is also its golden file. `scripts/discord-mocks.py` renders every one of
them into a single comparable page and refuses to render anything Discord would
reject.

**The get-started card is designed first and in three directions**, because it
is where a new player either joins or gives up, and because the rest of the bot
ships against it.

## Where every number comes from

The anti-invention gate. A surface that shows a figure nothing can produce is
the same failure as a JVM flag the JVM accepts and then ignores.

| field | source | exists today |
|---|---|---|
| pack version, index hash, MC, loader | `pack.toml`, and **`bench.Pack` already parses all four** (`internal/bench/pack.go`) | yes, reuse it |
| players online | RCON `list` | yes |
| TPS / MSPT | spark, which **answers on a worker thread, so RCON returns nothing and the numbers land in `latest.log`** | yes, via the log tail |
| uptime | Docker Engine API inspect over the mounted socket | yes |
| world day | RCON `time query day` | yes |
| map link and tiles | squaremap, `/data/squaremap/web/tiles` | yes, verified live 2026-08-23 |
| spend, MTD, currency | `/var/lib/wly/cost.json` | yes |
| playtime, deaths, distance, blocks | **`world/stats/<uuid>.json`, written by Minecraft itself** | **verify on the box** |
| advancements earned | `world/advancements/<uuid>.json` | **verify on the box** |
| joins, deaths, advancements live | `latest.log` → `internal/mcevents` | no, not written yet |

**The leaderboard needs no pipeline.** Minecraft already writes per-player stats
and advancement JSON, and `wly` already mounts `mc-data:/mc:ro`. Playtime,
deaths, mob kills, distance walked, blocks mined are a file read.
`internal/mcevents` stays scoped to *realtime* events off the log tail; the
totals come from the files.

> Confirm `/mc/world/stats/` exists and is populated, and check the
> tick-to-hours conversion against a real file, before anything is built on it.

## Linked roles: five keys, and they are all spent

| key | type | source | grants |
|---|---|---|---|
| `playtime_hours` | integer | `world/stats` | `settled` (10h), `rooted` (100h) |
| `has_joined` | boolean | first appearance in stats | `player` |
| `donor_tier` | integer | the payment rail | `supporter` |
| `first_joined` | datetime | first log join, recorded once | `day-one` |
| `deaths` | integer | `world/stats` | `deathless`, at zero |

**There is no sixth slot.** A new gate replaces one of these.

**Leaderboards do not use linked roles.** Five keys cannot carry arbitrary
stats, and role metadata is pushed per user over OAuth. Leaderboards read `wly`'s
own store and render in the Activity, where there is no component budget at all.

## The Activity

An Embedded App SDK activity that frames squaremap live inside a Discord
channel: pan the world, watch player dots move, click one for their card. It is
the centrepiece, and it is the part nobody expects a Discord server to do.

```
┌─ wly · the world ───────────────────────────── day 412 · 3 online ─┐
│                                                    │              │
│                                                    │  ▸ online    │
│              [ live squaremap canvas ]             │    kon    ♥  │
│              pan · zoom · player dots              │    ellis     │
│                                                    │    m         │
│                                                    │              │
│                                                    │  ▸ top 5     │
│                                                    │    hours     │
│                                                    │    deaths    │
│                                                    │    walked    │
├────────────────────────────────────────────────────┴──────────────┤
│  19.98 TPS · 8.4ms p95 · 6G · fabric 0.19.3 · pack v0.1.7          │
└───────────────────────────────────────────────────────────────────┘
```

**Serving it fixes an existing wart.** compose publishes
`${MC_MAP_PORT:-8123}:8080` on `0.0.0.0`, a public port on a box whose entire
design is tailnet-only, which HANDOFF already lists as noticed-and-not-acted-on.
A **Cloudflare Tunnel** to squaremap supplies the HTTPS origin the Activity
requires *and* lets that published port close. One change, two problems.

**This revives the Worker.** CLAUDE.md marks it `SUPERSEDED (commit c98aefb)`.
That reversal gets marked in place there rather than silently undone, and the
rule the Worker existed to encode, read-only at the edge with no authenticated
write path, is relaxed for exactly one thing: the signed payment webhook below.

## Supporters

### The payment rail

Lemon Squeezy or Polar. Both are Merchant of Record, which for a European hobby
project means they own the VAT problem. Both are webhook-driven, and this box
has no inbound path by design.

**The Worker is the receiver.** It verifies the HMAC signature, and on a valid
event writes an entitlement record to KV. **`wly` polls KV** and grants the role
on its normal reconcile loop. No inbound path to the box, and nothing granting
roles behind the reconciler's back. Swapping one provider for the other is one
signature check and one event-name table.

> The exact signature header and payload shape get looked up against the chosen
> provider's current docs when it is built, not guessed now.

### What a supporter can actually be sold

**Mojang's EULA forbids selling gameplay advantage on a Minecraft server.** That
is not a footnote, it is the constraint that writes this list. Cosmetic, social
and identity perks only.

- the gradient `supporter` role, hoisted, and the only gradient on the server
- a custom join line in the event feed
- a name on the supporters card
- a custom pin and marker colour on the map, in the Activity
- an Activity theme
- the `#supporters` channel
- pack release notes early

**Nothing that changes what a player can do in the world.** No kits, no
currency, no claims, no priority slot, no `/fly`. Getting this wrong is not a
design mistake; it is the kind that ends a server.

## The MOTD

**A rotating MOTD has no free mechanism.** Minecraft reads `motd` from
`server.properties` once at boot and there is no RCON command to change it, so
anything that varies per ping needs something answering the ping.

**Decided 2026-08-24: MiniMOTD**, `pack/stable`, `side = "server"`, MIT, and by
jpenilla, who also wrote the squaremap the pack already ships. It rotates a list
natively, so rotation costs no code at all. It does contradict the log-bridge
decision's "no mod to keep ported across MC versions", knowingly: 2.1.3 is the
1.21.1 Fabric build and it is one more thing to re-check at every Minecraft bump.
The alternatives were a status-ping proxy, rejected as a new single point of
failure in front of the only port players connect to, and rotating on restart,
rejected because the server restarts too rarely to be a rotation.

The compose `MOTD` is now the **fallback** that shows only if MiniMOTD fails to
load, so it stays correct rather than being deleted.

**Rotation now, live numbers later.** MiniMOTD alone rotates the static lines.
Filling in `{day}`, `{online}` and the rest needs wly to rewrite MiniMOTD's
config, and wly mounts `mc-data` **read-only on purpose**, so that wants one
narrow read-write mount and is a later phase. Lines and their data sources are
in `../wly.toml` under `[motd]`.

> **Verify on the box before shipping a config override.** squaremap writes
> `/data/squaremap/config.yml`, not `config/squaremap/` where every other mod
> puts its config, and that cost real time. Boot MiniMOTD once and look at where
> it actually writes before guessing a path.

## Branding: it is `wly`

`wly` everywhere a player sees a name. The long form is the project's origin, not
its label, and a heart carries the warmth without a word doing it. The pixel
`<:heart:>` is the mark and sits next to the name.

What is NOT renamed, because these are identifiers rather than branding: the box
hostname `weloveyou`, the ssh key, both repo names, the `weloveyou-pack.pages.dev`
host, and the published `weloveyou-stable.zip` filename. Renaming a published
artefact breaks every instance already pointing at it.

**Do not sell the server.** The surfaces are an interface, not a pitch. No
"a small friendly server", no mod-list flavour, no reasons to join. Say what the
thing is and what to do next. Someone reading the get-started card has already
decided; the card's only job is to not lose them.

## How this sounds

The surfaces are read by players, not by us. Rules, and they apply from here on:

- **No em dashes.** A colon, a comma or a full stop always works.
- **No oxford comma.** "terralith, oritech and two cores".
- **Cut anything a player does not need.** Heap size, loader version, index
  hash, mod counts and container tags are our problem, not theirs. Minecraft
  version and "whitelist only" are theirs.
- **Lowercase and warm**, the way the MOTD already reads. Short sentences.
- **Say the thing plainly.** "the first launch pulls everything down, so give it
  a minute" beats "initial synchronisation may take some time".
- **Emoji: yes, sparingly.** One or two per surface, where they carry something.
  A card covered in them reads as a bot wrote it, which is the thing to avoid.

## The icons

`scripts/pixelicons.py` holds the set: 8x8 grids, because that is exactly how
big a head is in a Minecraft skin file. `bench-site.py` and `discord-mocks.py`
both import it, so there is one source rather than two that drift.

**Discord cannot render SVG in a message.** The icons reach a player as **custom
guild emoji**: the same grids uploaded at 128x128 and written inline as
`<:heart:>`. `guild.toml` declares which ones get uploaded, and the reconciler
does it in D1. Until then `discord-mocks.py` draws the SVG in their place, and
refuses to render a surface that names an icon which does not exist.

The palette, the header and the heart live in `../scripts/brand.py`, which
`bench-site.py` and `discord-mocks.py` both import, so the two pages cannot drift.
`pack-site.py` is in the other repo and carries a copy with a comment naming
`brand.py` as the source, the same deliberate duplication as the pack URL.

**The heart is three glyphs, on purpose.** Web pages use `💖`, Discord
uses the custom `<:heart:>` from `pixelicons.py` so it matches the icon set, and
the Minecraft MOTD uses `♥` because Minecraft's default font has no emoji and an
unrenderable glyph shows as a box. Each medium gets the heart it can draw.

## What the browser check found

Checked at 1400, 900, 640, 420 and 360px on 2026-08-24. Two things, neither of
which was visible in the source:

- **Dotted leaders in a code fence do not fit a phone.** Directions A and C
  scroll sideways inside the message at 420px: A by 46px, C by 90px on its fact
  block and 368px on the java command line. B has none at any width, which is a
  second reason it won. Do not put a leader table or a long command line in a
  surface; a Section with an accessory reflows, a code fence does not.
- **The page needs `<meta charset="utf-8">` in its first 1024 bytes.** Without
  it, served over plain HTTP with no charset header, every heart, middot and
  arrow came out as mojibake. Publishing as an Artifact hides this, because that
  wraps the file in its own head, so it only shows up opening the file directly.

The page body itself never scrolls sideways at any width.

**Icons need to survive 18px.** The first shovel was drawn on the diagonal and
read as a smudge at the size an inline emoji actually renders. Redrawn head-on.
Anything going in `pixelicons.py` gets looked at inline before it counts as done.

## Working on this

```bash
python scripts/discord-mocks.py --check                    # validate, write nothing
python scripts/discord-mocks.py --out /tmp/mocks.html      # render everything
python scripts/discord-mocks.py --surface 'getstarted.*' --out /tmp/gs.html
```

`ci.yml` runs `--check` on every PR. That is deliberate: nothing ran
`bench-site.py` until `pages.yml` ran it on `main`, so a crash in it surfaced as
a red deploy *after* the merge that was meant to publish the numbers. The same
mistake was available here and is pre-empted.

## Phasing

| | phase | ends when |
|---|---|---|
| **D0** | this design pass, no Go | a direction is picked |
| **D1** | guild-as-code | `guild.toml` reconciles the real server, drift reported |
| **D2** | surfaces + the log bridge | six surfaces live, edited in place |
| **D3** | identity, linked roles, leaderboards | roles grant themselves from playtime |
| **D4** | the Activity | squaremap is live inside Discord |
| **D5** | supporters | a supporter gets their role without anyone touching it |
