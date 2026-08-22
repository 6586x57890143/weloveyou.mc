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

**Status: phases 0, 1 and 3 done.** Server is live and joinable, pack publishes to
Cloudflare Pages. Phase 2 (the Discord design passes) got pushed back in favour of the
pack development loop, which now lives in `weloveyou-pack` as `scripts/pack-dev.sh`.

*Last swept 2026-08-19.* See **Keeping these docs honest** at the end.

## Commands

```bash
go run ./cmd/wly bench --dry-run          # preflight only, measures nothing
go run ./cmd/wly bench --only j25-g1-coh --workload pack --runs 1

go build ./...
go test ./...
go vet ./...
gofmt -l cmd internal              # must print nothing

scripts/coverage.sh                # enforce .coverage-floors, the CI gate
scripts/coverage.sh --report       # same table, always exit 0

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
cmd/wly/            server daemon, bench harness today; bot, log bridge and RCON later
cmd/wlyup/          player-side pack updater. STUB: only `version` works
internal/buildinfo/ version stamp injected by -ldflags, shared by both binaries
internal/bench/     JVM flag profiles, workload driver, result table

PLANNED, NOT YET WRITTEN. This block described them as if they existed:
internal/packwiz/   PURE: pack.toml/index.toml parsing, resolution, hashing, diff, sync
internal/mcevents/  PURE: log line -> event, one regex table per MC generation
worker/             Cloudflare Worker, read-only

deploy/             Dockerfile for wly, docker-compose.yml for mc + wly
scripts/            CI helpers that must also run by hand
```

**Dependencies point inward.** `internal/packwiz` and `internal/mcevents` are pure: no
Discord, no Docker, no network of their own. That is what makes them cheap to test to 95%
and what would let `internal/packwiz` compile to `GOOS=js` if a browser use ever appears.

**SUPERSEDED (commit `c98aefb`): there is no R2 and no Worker.** The pack is served from
Cloudflare Pages and the map from squaremap's own webserver, so nothing was left for object
storage to do. `WLY_R2_*` still passes through compose and nothing consumes it. The rule the
Worker existed to encode survives and still applies if R2 ever returns: read-only at the
edge, no authenticated write path.

## Decisions worth not relitigating

- **wly speaks the Docker Engine API over the mounted socket**, rather than shelling out to
  `docker compose`. The image is distroless (no shell, no docker CLI), and restart, stop and
  inspect are three endpoints over a unix socket: about forty lines with stdlib `net/http`
  and a custom dialer. Cheaper than either fattening the image or adding the docker client
  library. This supersedes the plan, which said `os/exec`.
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

The box is an Oracle ARM VM (`weloveyou`, aarch64, 2 cores, 11G) reachable only over the
tailnet at `100.103.121.9`. `release.yml` builds a multi-arch image, pushes it to GHCR, joins
the tailnet with an ephemeral tagged auth key, and SSHes one command.

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
/srv/app/deploy/.env         secrets. gitignored, so `git checkout --force` never touches it.
```

Rolling back is `WLY_IMAGE=ghcr.io/…:vX.Y.Z` in `.env` plus a redeploy, no rebuild, no revert.

`docker compose pull` runs before `up` because `up` alone will happily keep a stale local
image that shares a tag with a newer remote one, which would make a deploy silently do nothing.

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
- **Coverage floors ratchet upward only.** Raise a floor when a package earns it; never lower
  one to make a red build green. `.coverage-floors` carries the reasoning.
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
6. **The sweep is two passes.** 18 profiles at full radius and 3 repeats is roughly 50 hours
   of a box that bills by the hour. Screen the whole matrix at `--runs 1 --radius 500` into
   `BENCHMARKS-screening.md`, then confirm the top few at full depth into `BENCHMARKS.md`.
   A screening run has no variance to report, so the two files are never comparable.

`jvm-profiles.toml` holds the candidates. Nothing in it is believed until it has a row in
`BENCHMARKS.md`.

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
