# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## What this is 💖

`weloveyou.mc` is the machinery behind a Fabric modpack: a Dockerized Minecraft server,
a Discord bot doing all the front-end work, and a squaremap live map. It's a hobby
project for a small server, so most of the choices here optimise for "one person can
still understand this in six months" over anything else.

**The modpack itself lives next door in `weloveyou-pack`**: pack channels, the Prism
instance templates, and the publishing pipeline. Code here, content there. They're split
because they move at different speeds, a pack release happens most weeks and never
touches any Go.

**If you're picking this up cold, read [HANDOFF.md](HANDOFF.md) first.** It covers what's
actually running, how a change reaches players, and which traps have already eaten an
afternoon. This file is the day-to-day reference, that one gets you oriented.

Two Go binaries and one Cloudflare Worker. Plan lives at
`~/.claude/plans/we-re-planning-a-highly-mutable-wirth.md`.

**Status, in the MASTER plan's numbering (`we-re-planning-a-highly-mutable-wirth.md`):
0, 1 and 3 are done, and 2 is in progress.** Server is live and joinable, pack publishes
to Cloudflare Pages. The pack development loop lives in `weloveyou-pack` as
`scripts/pack-dev.sh`.

**Two plans number the same ground differently and it has already misled a reader.**
`scope-out-the-simulated-sleepy-canyon.md` (the 2026-08-22 reframe) renumbers 1-5 over
phases the master plan calls 1-6. Its "phase 2, make the server playable" - backups
(`deploy/backup.sh`, installed and restored once on 2026-08-23) plus the compose audit
fixes - is DONE, and is a different thing from the master plan's phase 2, the Discord
design passes, which is what is in progress now. When a phase number appears anywhere,
say which plan it belongs to.

*Last swept 2026-08-24.* See **Keeping these docs honest** at the end.

## Commands

```bash
go run ./cmd/wly guild                    # diff guild.toml against the real server
go run ./cmd/wly guild --apply            # ... and make the changes

go run ./cmd/wly serve                    # the daemon: log bridge, board, spend
go run ./cmd/wly serve --once             # refresh the surfaces once and exit

go run ./cmd/wly surfaces                 # what the pinned messages would say
go run ./cmd/wly surfaces --apply         # ... and post or edit them in place

go run ./cmd/wly bench --dry-run          # preflight only, measures nothing
go run ./cmd/wly bench --only j25-g1-coh --workload pack --runs 1

go build ./...
go test ./...
go vet ./...
gofmt -l cmd internal              # must print nothing

scripts/coverage.sh                # enforce .coverage-floors, the CI gate
scripts/coverage.sh --report       # same table, always exit 0

python scripts/discord-mocks.py --check              # Components V2 payloads are valid
python scripts/discord-mocks.py --out /tmp/m.html    # render them for review

go run ./cmd/wly version
go run ./cmd/wlyup version

docker compose -f deploy/docker-compose.yml up -d    # needs deploy/.env
```

`go test -race` needs a C toolchain and **there is no gcc on this machine**: it runs on the
CI runners, which have one. Do not take a local `-race` failure at face value.

Requires Go 1.26.6 (`go.mod` pins it; 1.26.4 and .5 carry `crypto/tls` and `net/http`
advisories that `govulncheck` fails CI on).

## Architecture

```
cmd/wly/            server daemon and bench harness. `serve` runs three loops: the
                    log bridge, the status board and the spend post. engine.go is a
                    READ-ONLY Docker inspect, through the socket proxy, for one fact
                    (when the container started)
cmd/wlyup/          player-side pack updater. STUB: only `version` works
internal/buildinfo/ version stamp injected by -ldflags, shared by both binaries
internal/bench/     JVM flag profiles, workload driver, result table
internal/discord/   PURE: guild.toml parsing, the reconciler diff, and the six
                    surfaces. No HTTP, no token, no gateway, so the code that
                    decides to change a live community is testable without one.
                    testdata/surfaces/ holds the Components V2 payloads, which are
                    mockup, golden file and the thing surfaces.go is tested
                    against, all at once. Every number a surface shows arrives on
                    a Data struct, so a surface cannot invent one.

PLANNED, NOT YET WRITTEN. This block described them as if they existed:
internal/packwiz/   PURE: pack.toml/index.toml parsing, resolution, hashing, diff, sync
internal/mcevents/  PURE: log line -> event, one regex table per MC generation.
                    death.go is a vocabulary rather than a pattern, because a death
                    line carries no marker at all
internal/rcon/      Source RCON, stdlib. Minecraft CLOSES THE CONNECTION on an empty
                    command, so replies end at the 4096-byte split, never a sentinel
internal/logtail/   tail -F, across rotation and truncation, starting at the end
worker/             Cloudflare Worker, read-only

deploy/             Dockerfile for wly, docker-compose.yml for mc + wly,
                    backup.sh + wly-backup.{service,timer}, and the copies of the
                    box-side scripts that are reviewable (provision-box, bench-reaper,
                    wly-health-gate, wly-triage)
scripts/            CI helpers that must also run by hand. brand.py holds the visual
                    identity (palette, heart, header) and pixelicons.py the 8x8 icon
                    set; bench-site.py and discord-mocks.py both import them, so the
                    pages cannot drift apart. weloveyou-pack's pack-site.py carries a
                    copy of the palette and a comment naming brand.py as the source.
docs/               DISCORD.md, the design reference for the bot half
guild.toml          the Discord server, declared. RECONCILED (D1): `wly guild` diffs
                    roles, hierarchy, categories, channels, channel permissions and
                    emoji against the live guild; `--apply` creates and updates and
                    never deletes
```

**Dependencies point inward.** `internal/packwiz` and `internal/mcevents` are pure: no
Discord, no Docker, no network of their own. That is what makes them cheap to test to 95%
and what would let `internal/packwiz` compile to `GOOS=js` if a browser use ever appears.

**SUPERSEDED (commit `c98aefb`): there is no R2 and no Worker.** The pack is served from
Cloudflare Pages and the map from squaremap's own webserver, so nothing was left for object
storage to do. `WLY_R2_*` still passes through compose and nothing consumes it. The rule the
Worker existed to encode survives and still applies if R2 ever returns: read-only at the
edge, no authenticated write path.

**PARTIALLY REVERSED, planned (phase D5, see `docs/DISCORD.md`): the Worker returns for one
job.** The supporter payment rail (Lemon Squeezy or Polar) is webhook-driven and this box has
no inbound path by design, so a Worker verifies the HMAC signature and writes an entitlement
to KV, which `wly` polls. That is one signed write path at the edge and a deliberate
relaxation of the rule above, taken because the alternative is opening a public port on a
tailnet-only box. Nothing else at the edge becomes writable.

## Decisions worth not relitigating

- **The guild reconciler speaks Discord's REST API with stdlib `net/http`**, for the
  same reason as the Docker decision below: it is eleven plain JSON endpoints behind
  one header, and `disgo` earns its place when the gateway does, because a websocket
  with heartbeats, resume and interaction routing is genuinely worth not writing.
  Every decision lives in `internal/discord` and is tested against `httptest`, so
  none of it needs a token.
- **wly speaks the Docker Engine API**, rather than shelling out to `docker compose`.
  The image is distroless (no shell, no docker CLI), and restart, stop and inspect are
  three endpoints over HTTP: about forty lines with stdlib `net/http` and a custom
  dialer. Cheaper than either fattening the image or adding the docker client library.
  This supersedes the plan, which said `os/exec`.

  **AMENDED 2026-08-24, security review: not over a mounted socket.** The bind mount was
  removed from `deploy/docker-compose.yml`. `/var/run/docker.sock` is host-root-equivalent
  and makes the `read_only`, `cap_drop: ALL` and `no-new-privileges` beside it decorative:
  anything that reaches the API can start a privileged container and mount the host. No Go
  code had used it yet, so it was mounted for nothing, and the first thing to use it would
  have been a daemon parsing Discord input off the internet. The forty-line scope was a
  comment, not a boundary. When that code lands it points at `tecnativa/docker-socket-proxy`
  on the compose network with `CONTAINERS=1, POST=1`, and wly gets
  `DOCKER_HOST=tcp://docker-proxy:2375`. Same design, same three endpoints, scope enforced
  by something other than good intentions.
- **The bridge tails `latest.log`** instead of using the Docker log API or a bridge mod. No
  Docker dependency for the hot path, and no mod to keep ported across MC versions.
- **Fabric-only, so no Create.** Create Fabric stopped at 1.20.1 and the 1.21.1 port branch
  died in March 2025. `stable` is 1.21.1 with Oritech as the tech spine. Create Fly is a live
  Fabric fork of Create for 26.2, but it has no addon ecosystem. That makes it an `edge` question, not a
  `stable` one.
- **Players install through Prism Launcher, not a binary we ship.** The instance zip carries a
  pinned `packwiz-installer.jar` run as a pre-launch step, so nothing unsigned is executed and
  SmartScreen never enters it. `cmd/wlyup` survives as an optional CLI and as insurance
  against packwiz-installer, which last shipped in April 2024.
- **Pack releases never deploy anything.** The pack lives on Cloudflare Pages; the server
  fetches it via `PACKWIZ_URL` on restart. Only `wly` and compose changes touch the box.

## Deployment

The box is an Oracle ARM VM (`weloveyou`, aarch64, 2 cores, 11G) on the tailnet at
`100.103.121.9`. `release.yml` builds a multi-arch image, pushes it to GHCR, joins the
tailnet with an ephemeral tagged auth key, and SSHes one command.

**CORRECTED 2026-08-24, verified against OCI and probed from off-tailnet: the posture is
real, but inverted.** "Reachable only over the tailnet" was wrong in the one direction that
matters and right about everything else.

One security list (`Default Security List for gate`) is attached to the one subnet (`gate`,
10.0.0.0/24), every instance has `nsg-ids []`, so that list is the entire cloud firewall.
Its whole ingress is:

```
tcp  22          from 0.0.0.0/0     <-- the internet, and the only thing that is
icmp all         from 0.0.0.0/0
icmp all         from 10.0.0.0/24
udp  41641       from 0.0.0.0/0     stateless, tailscale direct connections
```

Probed from a machine off the tailnet against `158.180.53.71`: **22 open, 25565, 8123 and
24454 all filtered.** The same ports answer immediately on `100.103.121.9`. Host `iptables`
agrees, `-i lo`, established, icmp, `NEW tcp dpt:22`, then REJECT.

So the box publishes SSH to the internet and keeps the game and the map on the tailnet.
Half of that is the plan and half of it is the bug, and they are easy to mix up:

- **The game and map ports staying shut is DELIBERATE, do not "fix" it.** Players reach
  25565 over the tailnet today. Opening it publicly waits on a domain, players will connect
  to a subdomain rather than a bare IP, and on a judgement that the platform is ready for
  strangers to point traffic at. Until then the closed port is the decision, not an
  oversight. Note that DNS is not a control: an A record pointing at `158.180.53.71` does
  nothing to who can reach it, the security list is the only gate, and behind that the
  protections are `ONLINE_MODE`, `ENABLE_WHITELIST` and `ENFORCE_WHITELIST` in
  `docker-compose.yml`.

  **CORRECTED 2026-08-25: `ENABLE_WHITELIST` was missing and the whitelist was
  therefore OFF.** The live server read `enforce-whitelist=true` with
  `white-list=false`, which means anyone reaching the port was let in.
  `ENFORCE_WHITELIST` alone governs only whether already-connected players are
  kicked when the list reloads; it cannot enforce a whitelist that is not on.
  Only the closed port made this survivable, and opening 25565 is the plan, so
  it would have become a public server with a whitelist that did nothing.
- **`docker-compose.yml` reads as though the ports were already public.** It publishes 25565
  and 24454/udp and carries a long comment about the voice-chat port, none of which is
  reachable from the internet. Those mappings are correct and are what will make the port
  work the day the security list opens; they simply are not evidence that it is open. A
  check run on the box (`serves 200 through this mapping`) cannot see a cloud firewall,
  which is how the difference stayed invisible.
- **Bench boxes inherit it.** `provision-box.sh` puts them in this same subnet with
  `--assign-public-ip true` and seeds production's `authorized_keys` onto them, so "no
  public SSH by convention" in that script, `add-bench-box.sh` and `bench-admin.yml` is
  wrong. It is not convention, it is one security-list rule away in the other direction.

The fix is to drop the `tcp 22 from 0.0.0.0/0` rule. Tailnet SSH does not traverse it,
peer traffic arrives inside WireGuard over `udp 41641`, which stays. **Do not do it blind:
if tailscale is unhealthy at that moment the box is unreachable, and recovery is the OCI
serial console.** Confirm `tailscale status` first, keep the console to hand.

Re-check with:

```bash
oci network subnet get --subnet-id "$S" --output json   # security-list-ids, nsg-ids
```

**What CI can do on that box is deliberately tiny.** The `deploy` user's key is a forced
command (`command="/opt/deploy/deploy.sh"`, `restrict`, `from="100.64.0.0/10,fd7a:...::/48"`),
so the SSH invocation cannot run anything but that script, which accepts `deploy [ref]` and
rejects everything else by regex. The privileged half is a separate root-owned script taking
no arguments, reachable through a single-command sudoers rule, so the compose file path cannot
be influenced by the caller. Verified: `rm -rf /`, `deploy ../../etc/passwd` and
`deploy $(whoami)` are all refused.

```
/opt/deploy/config           REPO_URL, APP_DIR=/srv/app, COMPOSE_FILE=deploy/docker-compose.yml
/opt/deploy/deploy.sh        forced command: parses a ref, git checkout, calls compose-up.sh,
                             rolls back to the previous SHA if compose fails
/opt/deploy/compose-up.sh    root half: pull --ignore-buildable, then up -d --build
/srv/app                     the checkout, cloned via a read-only GitHub deploy key
/srv/app/deploy/.env         secrets, mode 600. gitignored, so `git checkout --force`
                             never touches it. The mode is not asserted by anything, so
                             check it rather than assume it: `stat -c %a` on the box. It
                             holds RCON_PASSWORD and, from the guild reconciler on,
                             WLY_DISCORD_TOKEN, and every user in the `docker` group can
                             already read the container's environment regardless.
```

`MC_IMAGE` is the same lever for the Minecraft image. `itzg/minecraft-server:java25` is a
moving tag and `pull` runs before every `up`, so without it the JVM under the server can
change on a deploy that touched nothing.

Rolling back is `WLY_IMAGE=ghcr.io/…:vX.Y.Z` in `.env` plus a redeploy, no rebuild, no revert.

`docker compose pull` runs before `up` because `up` alone will happily keep a stale local
image that shares a tag with a newer remote one, which would make a deploy silently do nothing.

## Backups

`deploy/backup.sh` on a nightly timer: RCON `save-off`, `save-all flush`, tar from inside
the container, `save-on` from a trap, keep two archives on the box, rsync one to
`BACKUP_TARGET`. Four things it encodes:

- **`save-on` is a trap.** An interrupted backup that leaves saving off is silent until
  the next restart, and everything played in between is gone.
- **The tar runs in the container**, so nothing has to know the compose volume prefix.
- **tar exits non-zero routinely** on a live world ("file changed as we read it"). `gzip -t`
  is what separates that from a truncated archive.
- **Staleness is the alert, not failure.** The off-box destination is a desktop that is
  often off; two local copies mean that is not an incident. The newest archive passing 36h
  is, and it catches a failed run, a timer that never fired, and a dead container alike.

**The link database is dumped beside the world, not inside it.** `wly-db` gets its own
`pg_dump` to `db-<stamp>.sql.gz`, pruned, shipped and staleness-checked exactly like the
world archive. Three things that are not obvious:

- **It needs no save dance.** `pg_dump` takes its own consistent snapshot, which is the
  whole reason it is a dump rather than a tar of the volume. Copying a live Postgres data
  directory file by file produces the same torn result `save-off` exists to prevent.
- **A missing `wly-db` is not a failure, a failed dump is.** The service does not exist
  until the bot ships, so the script says so and moves on. Once the container is running,
  both a failed dump and a stale one escalate to high, because unlike the world there is
  no second copy of this anywhere and no way to rebuild it.
- **Losing it is not cosmetic.** Every player has to re-link their Discord account by
  hand, and nothing in the world archive can reconstruct that mapping.
- **The dump is self-sufficient, and that is deliberate.** It is taken with
  `--clean --if-exists`, so it drops before it creates and restores cleanly over a volume
  that has already run `schema.sql` from initdb. That is the normal case, not a corner
  one: a fresh volume always runs `schema.sql`, so a plain dump would fail halfway through
  the restore, at exactly the hour you would rather it did not.
  **Database restore rehearsed 2026-08-24**, twice and by two routes, which is why the
  flag is not optional. On the box: seed two rows, dump, drop and recreate the table the
  way initdb would, restore, `ON_ERROR_STOP=1` exit 0 and both rows back. Locally through
  the real initdb path: a plain `pg_dump` restored onto a fresh volume that had run
  `schema.sql` dies with `relation "link_requests" already exists`, psql exit 3. The same
  dump taken with `--clean --if-exists` then restores cleanly onto that same failed
  database AND into one that has never seen `schema.sql`.

  **Constraints survive the round trip, and that is the check worth keeping.** A restore
  that returns rows without them is worse than one that fails, because it looks fine: after
  restoring, inserting a second `players` row with an already-taken `mc_uuid` still fails
  on `players_mc_uuid_key`. One Minecraft account still belongs to exactly one person.

`BACKUP_TARGET` is one setting and deliberately temporary, not a design. Object storage
replaces the desktop later and nothing else changes.

**Restore verified 2026-08-23** into a throwaway volume: level.dat, regions, playerdata,
whitelist, ops and config all came back, `mods/` correctly did not. `BACKUP_TARGET` is
still empty, so the off-box copy does not exist yet and the staleness alert is what would
tell you.

## Spend

Oracle is no longer free here: Always Free was halved to 2 OCPU / 12 GB on
2026-06-15, which the production box alone consumes. The bench box runs on
credits.

A systemd timer on the production box writes `/var/lib/wly/cost.json` daily at
06:30 UTC via `/opt/deploy/cost-report.sh`:

```json
{"generated": "...", "yesterday": 0.0338, "month_to_date": 0.0338,
 "by_service": {"compute": 0.0338}}
```

**wly must surface this in its push notifications**: a spend line in the daily
Discord message. Credits run out quietly otherwise, and the first sign would be
a stopped server. The file exists before the bot does precisely so the bot has
nothing to invent when it lands.

Until the bot exists, the box pushes the same numbers to the phone itself:
`wly-cost.service` runs `cost-report.sh` as `ubuntu` and then
`/opt/wly/bin/cost-push.sh` as `agent`, which renders the JSON to ntfy. It is
deterministic (no LLM for three numbers) and escalates to high priority on a
missing or null report, a report older than 36h, a day above 2x the month's
running average, or a month-end projection above the budget (`WLY_COST_BUDGET`,
default 5). A null amount is pushed as *unknown*, never as zero. The report now
carries the API's own `currency` field, so nothing downstream has to assume EUR.

Stop the bench box when it is not sweeping. Stopped instances bill only for the
boot volume, so an idle bench box is pennies and a running one is not.

## Provisioning another box

`/opt/deploy/provision-box.sh <name> [ocpus] [memory-gb]` on the production box,
which is where the OCI credential lives. One command, and it encodes the thing
that cost an afternoon to learn:

**A1 is almost always "Out of host capacity" for a NEW instance**: all three
Frankfurt ADs refused, **but A2 has capacity, and converting an existing A2 to
A1 succeeds.** So the script launches A1 first, falls back to A2, and converts.

The conversion needs a stopped instance and is asynchronous: for a minute or two
afterwards the instance still reports the old shape and START returns "currently
being modified". That is not a failure; retry until it settles.

Convert rather than stay on A2 because the shapes are different machines. A1 is
Neoverse-N1 with one vCPU per OCPU; A2 is AmpereOne with two. A bench box on
different silicon with twice the threads measures a server we do not ship, and
GC choices hinge on spare cores more than anything else.

Manual equivalent of the conversion step:

```bash
oci compute instance action --instance-id <ocid> --action SOFTSTOP --wait-for-state STOPPED
oci compute instance update --instance-id <ocid> --shape VM.Standard.A1.Flex   --shape-config '{"ocpus":2,"memoryInGBs":12}' --force
oci compute instance action --instance-id <ocid> --action START   # retry while "being modified"
```

## The bench box costs money when it runs

`/opt/deploy/bench-power.sh start|stop|status`. Three things keep it cheap:

- `bench.yml` powers the box off as its last step, `if: always()`, so a crashed
  sweep does not bill for a week. An in-guest poweroff moves the OCI instance to
  STOPPED, which is what stops billing. Only the boot volume is charged after that.
- `wly-bench-up.timer` starts it Mondays 03:45 UTC, before the 04:00 sweep. A
  stopped runner means GitHub queues the job rather than running it.
- `wly-bench-idle.timer` stops it nightly at 23:30 UTC, in case the sweep never
  reached its own power-off step.

## Cross-repo contract

`wly.toml` carries each channel's `pack_url` so the daemon can poll it for releases.
`weloveyou-pack` carries the same URL in its `channels.toml`, because its instance builder
substitutes it into `instance.cfg`. One hostname, written twice, deliberately. The
alternative is a shared config repo, which is more machinery than a hostname is worth.

If a channel's URL changes, both must change. Nothing enforces that automatically; it is one
of the few places the split costs something.

## Conventions

- **Line endings are LF**, enforced by `.gitattributes`. A CRLF shell script or Dockerfile
  breaks the Linux runners in ways that are tedious to diagnose from a Windows checkout.
- **Windows checkouts drop the executable bit.** A new script committed from here lands as
  100644 and CI fails with `Permission denied` (exit 126). Fix with
  `git update-index --chmod=+x scripts/<name>`, and check `git ls-files -s scripts/` before
  pushing a new one.
- **Write files with bash heredocs.** Python read_text/write_text use the Windows locale
  codec by default and will silently mangle every em-dash into a byte no UTF-8 parser
  accepts. If Python is unavoidable, pass `encoding="utf-8", newline="\n"`.
- **CI uses no third-party actions** outside `actions/*` and `dependabot/fetch-metadata`.
  Release workflows hold write access to the repo and registry, every action they run is
  part of this project's supply chain. Everything else is plain CLI.
- **Nothing is done until `scripts/coverage.sh` passes.** Not "it builds", not
  "the tests pass": the floors are part of the definition of finished, and any
  change that adds a statement has to carry a test for it in the same commit.
  Three rules, each learned by breaking it:
  - **Read the whole table, never grep one package out of it.** Adding
    `writeReports` dropped `cmd/wly` under its floor while `internal/bench` was
    filtered for and looked fine.
  - **`docker` is on this machine and not on the runners**, so local `cmd/wly`
    coverage is inflated. The binding number is
    `PATH="/usr/bin:/bin:/c/Program Files/Go/bin:/c/Users/kon/go/bin" go test ./cmd/wly/ -cover`.
    47.5% locally was 28.8% in CI, against a floor of 30.
  - **Leave margin.** Landing exactly on the floor is a failure waiting for the
    next commit: 99.0 against a floor of 99 went red in CI at 98.9.
- **Nothing on the server could answer for TPS until spark shipped**, and the
  board showed measured RCON response time rather than invent one.
  `StatusData.HasTick` is the switch. spark MUST run with
  `-Dspark.backgroundProfiler=false` on Java 25, in compose and in
  `internal/bench/runner.go` alike, or its bundled async-profiler segfaults the
  JVM at boot.
- **A surface never invents a number.** Every field on a `*Data` struct comes
  from a caller that read it from somewhere real, and a surface whose data source
  does not exist yet is SKIPPED OUT LOUD by `wly surfaces` rather than filled with
  a plausible value. Silently missing is indistinguishable from failed to post.
- **Coverage floors ratchet upward only.** Raise a floor when a package earns it; never lower
  one to make a red build green. `.coverage-floors` carries the reasoning.
- **Channel names are `<emoji>│<name>`**, the separator being U+2502 BOX DRAWINGS
  LIGHT VERTICAL. One emoji per channel, fitting, never decorative. The bar is
  visible because a blank one does not exist: Discord turns whitespace into a
  hyphen and strips zero-width characters, answering 200 either way. `guild.toml`
  carries the full measurement and HANDOFF the trap. Renames are rate limited to
  two per ten minutes per channel, so a sweep takes several `--apply` runs.
- **A channel that hides itself must grant `wly` view and send in the same
  breath.** `Channel.Overwrites` does this; do not hand-roll an overwrite that
  skips it. A bot cannot grant itself access to a channel it cannot see, so the
  recovery is a human in Server Settings.
- **Keep the dependency list short.** Adding one needs justification in the commit message.
- **Mark deliberate shortcuts** with a `ponytail:` comment naming the ceiling and the upgrade
  path, so `/ponytail-debt` can find them later.

## Testing

`.coverage-floors` is the contract. Beyond the number:

- Table-driven tests everywhere: adding the 26.2 log format should be a row, not a function.
- Fuzz every parser. A malformed remote `pack.toml` must never panic on a player machine.
- Golden JSON files for every Discord surface, so a layout change is a reviewable diff rather
  than a surprise in the channel.
- `httptest` for all remote fetches. No test touches the network.
- Integration tests behind `-tags integration`: real container, real RCON, real log tail.
- `-race` on everything (CI only, see above).

## Benchmarking

JVM flags are measured, not inherited. Base reference is
`brucethemoose/Minecraft-Performance-Flags-Benchmarks`, four years stale, several of its
conclusions need re-testing (the ZGC section is obsolete; compact object headers did not
exist).

Two rules that are easy to get wrong:

1. **Benchmark against a representative pack**, not vanilla. Workload A is vanilla worldgen
   as a control; workload B is the same run against `pack/stable`, which is why that pack
   carries Terralith and Oritech rather than only the perf mods. A flag that wins on A and
   loses on B is a finding, not noise.
2. **Never benchmark on a GitHub-hosted runner.** They are noisy shared VMs; the numbers
   would be fiction. `bench.yml` targets `[self-hosted, bench]`.
3. **The JDK is a dimension, not a constant.** 1.21.1 only requires Java 21, but compact
   object headers need 25. Running an older pack on a newer JDK is deliberate here: `warn`
   on 24/25, `deny` on 26 (fix with `--sun-misc-unsafe-memory-access=allow`), removed on 27+.
   Trading slight instability for a measured gain is fine when the mitigation is a documented
   flag; shipping an unmeasured profile because it sounds fast is not.

4. **The bench box must be boring, and Ubuntu's defaults are not.**
   `apt-daily.timer` and `apt-daily-upgrade.timer` are `OnCalendar=6:00` with
   `Persistent=true`. A box powered off except when sweeping therefore fires the
   *missed* 06:00 run shortly after every boot, which is precisely when a sweep is
   running. Measured: booted 13:52, `apt-daily-upgrade` started 14:22:57, sweep
   cancelled 14:25:25, and that run consumed **4min 7s of CPU on two cores**.
   The cancelled job was the visible half; the invisible half is a profile measured
   against apt using both cores, which yields a plausible and wrong number.
   Masked via `bench-admin.yml -> quiet-timers`; re-run it after reprovisioning,
   because `provision-box.sh` is not version controlled. `sysstat-collect` is left
   alone on purpose, ten-minutely CPU history is evidence, not noise.
5. **The pack is a skeleton and will grow.** Today's `pack/stable` is Terralith, Oritech and
   perf mods; gameplay mods are still to come. A workload-B number is only comparable to
   another taken against a similar pack, so the report prints the mod count it measured and
   says so. Do not compare a row from today against one taken after the pack doubles.
6. **The sweep is three passes.** 25 profiles at full radius and 3 repeats is roughly 50
   hours of a box that bills by the hour. Screen the whole matrix at `--runs 1 --radius 500`
   into `BENCHMARKS-screening.md`, then confirm the top few at full depth into
   `BENCHMARKS.md`, then run the tick workloads against only those.
   A screening run has no variance to report, so the two files are never comparable.

   ```bash
   gh workflow run bench -f workload=worldgen -f runs=1 -f radius=400 -f out=BENCHMARKS-screening.md
   gh workflow run bench -f workload=worldgen -f runs=3 -f radius=1000 -f only=j25-g1-coh,baseline-j21
   gh workflow run bench -f workload=players  -f runs=3 -f only=j25-g1-coh,baseline-j21
   ```

7. **The tick workloads are read by MSPT p95, never TPS.** TPS is capped at 20, so a load
   that is merely heavy reads 20 on every profile and reproduces the blind spot the
   workloads exist to remove: worldgen barely ticks entities, so the instrument was wired
   end to end for weeks while measuring nothing. p95 is unbounded and moves long before
   TPS does. `explore`, `village` and `machines` all report it, lower being better, and
   `vs base` is signed accordingly so a positive number means "better" in every column.

8. **A tick workload must be calibrated before it is believed.** One whose load cannot move
   p95 measures nothing while still printing a plausible row, which is the same failure as
   a JVM flag the JVM accepts and then ignores. Run
   `--only baseline-j21 --workload players --runs 1` and raise `--load` until p95 clears the
   idle floor, then leave it there: two rows taken at different `--load` are not comparable.
   Record the calibrated value in `jvm-profiles.toml` next to the other method notes.

9. **`--dry-run` prints each workload's RCON script and needs no docker.** That is the cheap
   proof a drive script is well-formed. The expensive alternative is finding out on a box
   that bills by the hour, from a log where a rejected command is one grey line among
   thousands.

   ```bash
   go run ./cmd/wly bench --dry-run --workload all
   ```

`jvm-profiles.toml` holds the candidates. Nothing in it is believed until it has a row in
`BENCHMARKS.md`.

**A workload is a row in `Specs` (`internal/bench/workload.go`)**, not a branch: what to
load, what to type once the server is up, what to type on each sample tick, when to stop,
and which number the table is read by. `Render`, `RenderJSON` and `scripts/bench-site.py`
all iterate `AllWorkloads`, so adding one is a row and forgetting one is a test failure
rather than a workload that silently renders nowhere.

**Carpet is an instrument, not content.** It supplies the fake players and is pinned in
`runner.go` beside spark and Chunky, loaded through `MODS=`. It is deliberately absent from
`pack/stable`: a fake-player mod has no business shipping to players, and the pack's
`side` checking has enough to worry about.

## The benchmark site

`scripts/bench-site.py` renders `BENCHMARKS.json` and `BENCHMARKS-screening.json`
into one self-contained page, published by `pages.yml` to Workers Static Assets
(`wrangler.jsonc`) on a **merge to main**, never off a sweep finishing.

**Design identity, so the next page in this project matches rather than guesses:**
minimal, monospace, dark, extended-ASCII, Minecraft-inspired. The accents are
Minecraft's own chat palette desaturated for reading on a dark background, and
the mapping is written into the file next to the CSS: `§a` win, `§c` loss and
failure, `§6` baseline and warnings, `§b` links, `§7` secondary.

**Box-drawing glyphs go where monospace alignment is safe and carries meaning**:
the header frame, the rules, the bars (`█▉▊▋▌▍▎▏`), the row markers. **Table grid
lines are CSS borders, not drawn characters.** A real `+--+--+` grid breaks the
moment a cell wraps or the viewport narrows, and a table that only lines up at
one width is worse than one that never pretended to. Where a rule has to span an
arbitrary width, it is a repeated glyph clipped by `overflow:hidden` between two
real corner characters, which fits any width exactly.

Two rules learned from publishing the first real sweep:

- **One glyph means "no value".** There were three, because `a08b9f8` swapped em
  dashes for plain punctuation and turned the placeholders into a literal `", "`.
- **The page states how each table reads.** Throughput is higher-is-better and
  tick health is lower-is-better, so `vs base` is signed to make positive always
  mean better, and each table says which metric it is read by.

`ci.yml` renders the committed results on every PR. Before that, nothing ran the
generator until `pages.yml` ran it on `main`, so a crash in it surfaced as a red
deploy after the merge that was meant to publish the numbers.

`wly bench` preflights every flag with `java <flags> -version` and drops what the JVM
rejects, so a flag removed by a future JDK degrades instead of failing the boot. It probes
the whole set first and only falls back to one-at-a-time when the set is refused, which
both costs one JVM start instead of thirty and keeps flags that are legal only in company
(`-XX:NodeLimitFudgeFactor` must be 2-40% of `-XX:MaxNodeLimit`, so raising the limit alone
is rejected while the pair is fine).

**The preflight cannot catch a flag the JVM accepts and then ignores**, and that is a real
category, not a hypothetical:

- `{C2 product}` flags are inert on GraalVM, where `UseJVMCICompiler=true` means Graal
  replaces C2. This is why `jvm-profiles.toml` splits `bruce-c2` into its own flagset.
- x86-only flags (`-XX:+UseVectorCmov`, `-XX:+UseFastUnorderedTimeStamps`) do nothing on the
  aarch64 bench box.
- `-XX:InitiatingHeapOccupancyPercent` is only a starting value while `G1UseAdaptiveIHOP`
  defaults to true.
- Contrary to the plan's guess, `-XX:NmethodSweepActivity` is **not** in this category, it
  is still a live `{product}` flag on JDK 25, accepted without warning.

One combination is impossible rather than merely inert: **Graal does not support Shenandoah**
(`GraalError: Shenandoah garbage collector is not supported by Graal`). HotSpot accepts the
flag and then the JIT disables itself, so a run that survived it would be measuring GraalVM
with no Graal. That profile is declared and disabled.

## Keeping these docs honest

`CLAUDE.md` and `HANDOFF.md` are read cold by someone, or something, with no
other context, so a stale line here is worse than a missing one: it is confidently
wrong and gets acted on. Two rules.

**Mark, do not silently delete.** When something is superseded, say so inline and
say what replaced it, with the commit if you have it:

```
**SUPERSEDED (commit c98aefb): there is no R2 and no Worker.** ...
PLANNED, NOT YET WRITTEN. This block described them as if they existed:
```

The reasoning behind a reversed decision is usually the expensive part, and
deleting the line deletes the reason too. Someone will otherwise re-propose it.
Delete only once the thing is gone AND nobody could reasonably re-propose it.

**Sweep on a schedule, and stamp it.** Both files carry a `*Last swept <date>.*`
line near the top. Re-read them whenever you finish a phase, and at minimum
whenever you touch either file, checking specifically:

- Does every path in the Architecture block exist? (Three did not, for weeks.)
- Does the Status line match what actually shipped?
- Do the Commands still run? Try them, do not assume.
- Has an external fact moved: an image tag, a JDK default, a free tier, an
  upstream that went unmaintained?

Findings that cost real time belong in `HANDOFF.md` under the traps, not here.
This file is the working reference; that one is the orientation.
