# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## What this is

`weloveyou.mc` — the platform behind a Fabric modpack: a Dockerized Minecraft server, a
Discord bot that is the entire front-end, and a squaremap live map served from Cloudflare R2.

**The modpack itself lives in a separate repository, `weloveyou-pack`** — pack channels, the
Prism instance templates, and their publishing pipeline. This repo ships code; that one ships
content. Split because the cadences differ: a pack release is weekly and touches no Go.

Two Go binaries and one Cloudflare Worker. Plan lives at
`~/.claude/plans/we-re-planning-a-highly-mutable-wirth.md`.

**Status: phases 0, 1 and 3 done.** The server is live and joinable; the pack
publishes to Cloudflare Pages; `wly bench` exists and has never been run for
real. Next is phase 2, the Discord design passes.

## Commands

```bash
go run ./cmd/wly bench --dry-run          # preflight only, measures nothing
go run ./cmd/wly bench --only j25-g1-coh --workload pack --runs 1

go build ./...
go test ./...
go vet ./...
gofmt -l cmd internal              # must print nothing

scripts/coverage.sh                # enforce .coverage-floors — the CI gate
scripts/coverage.sh --report       # same table, always exit 0

go run ./cmd/wly version
go run ./cmd/wlyup version

docker compose -f deploy/docker-compose.yml up -d    # needs deploy/.env
```

`go test -race` needs a C toolchain and **there is no gcc on this machine** — it runs on the
CI runners, which have one. Do not take a local `-race` failure at face value.

Requires Go 1.26.6 (`go.mod` pins it; 1.26.4 and .5 carry `crypto/tls` and `net/http`
advisories that `govulncheck` fails CI on).

## Architecture

```
cmd/wly/            server daemon — Discord bot, log bridge, RCON, R2 sync, bench harness
cmd/wlyup/          player-side pack updater, single static binary
internal/buildinfo/ version stamp injected by -ldflags, shared by both binaries
internal/packwiz/   PURE: pack.toml/index.toml parsing, resolution, hashing, diff, sync
internal/mcevents/  PURE: log line -> event, one regex table per MC generation
internal/bench/     JVM flag profiles, workload driver, result table
worker/             Cloudflare Worker, R2 binding, read-only
deploy/             Dockerfile for wly, docker-compose.yml for mc + wly
scripts/            CI helpers that must also run by hand
```

**Dependencies point inward.** `internal/packwiz` and `internal/mcevents` are pure: no
Discord, no Docker, no network of their own. That is what makes them cheap to test to 95%
and what would let `internal/packwiz` compile to `GOOS=js` if a browser use ever appears.

**The Worker is read-only.** `wly` writes to R2 over the S3 API with a scoped token. There is
deliberately no authenticated write path at the edge.

## Decisions worth not relitigating

- **wly speaks the Docker Engine API over the mounted socket**, rather than shelling out to
  `docker compose`. The image is distroless — no shell, no docker CLI — and restart, stop and
  inspect are three endpoints over a unix socket: about forty lines with stdlib `net/http`
  and a custom dialer. Cheaper than either fattening the image or adding the docker client
  library. This supersedes the plan, which said `os/exec`.
- **The bridge tails `latest.log`** instead of using the Docker log API or a bridge mod. No
  Docker dependency for the hot path, and no mod to keep ported across MC versions.
- **Fabric-only, so no Create.** Create Fabric stopped at 1.20.1 and the 1.21.1 port branch
  died in March 2025. `stable` is 1.21.1 with Oritech as the tech spine. Create Fly is a live
  Fabric fork of Create for 26.2, but it has no addon ecosystem — an `edge` question, not a
  `stable` one.
- **Players install through Prism Launcher, not a binary we ship.** The instance zip carries a
  pinned `packwiz-installer.jar` run as a pre-launch step, so nothing unsigned is executed and
  SmartScreen never enters it. `cmd/wlyup` survives as an optional CLI and as insurance
  against packwiz-installer, which last shipped in April 2024.
- **Pack releases never deploy anything.** The pack lives in R2; the server fetches it via
  `PACKWIZ_URL` on restart. Only `wly` and compose changes touch the box.

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

Rolling back is `WLY_IMAGE=ghcr.io/…:vX.Y.Z` in `.env` plus a redeploy — no rebuild, no revert.

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

**wly must surface this in its push notifications** — a spend line in the daily
Discord message. Credits run out quietly otherwise, and the first sign would be
a stopped server. The file exists before the bot does precisely so the bot has
nothing to invent when it lands.

Stop the bench box when it is not sweeping. Stopped instances bill only for the
boot volume, so an idle bench box is pennies and a running one is not.

## Provisioning another box

`/opt/deploy/provision-box.sh <name> [ocpus] [memory-gb]` on the production box,
which is where the OCI credential lives. One command, and it encodes the thing
that cost an afternoon to learn:

**A1 is almost always "Out of host capacity" for a NEW instance** — all three
Frankfurt ADs refused — **but A2 has capacity, and converting an existing A2 to
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
  STOPPED, which is what stops billing — only the boot volume is charged then.
- `wly-bench-up.timer` starts it Mondays 03:45 UTC, before the 04:00 sweep. A
  stopped runner means GitHub queues the job rather than running it.
- `wly-bench-idle.timer` stops it nightly at 23:30 UTC, in case the sweep never
  reached its own power-off step.

## Cross-repo contract

`wly.toml` carries each channel's `pack_url` so the daemon can poll it for releases.
`weloveyou-pack` carries the same URL in its `channels.toml`, because its instance builder
substitutes it into `instance.cfg`. One hostname, written twice, deliberately — the
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
  Release workflows hold write access to the repo and registry — every action they run is
  part of this project's supply chain. Everything else is plain CLI.
- **Coverage floors ratchet upward only.** Raise a floor when a package earns it; never lower
  one to make a red build green. `.coverage-floors` carries the reasoning.
- **Keep the dependency list short.** Adding one needs justification in the commit message.
- **Mark deliberate shortcuts** with a `ponytail:` comment naming the ceiling and the upgrade
  path, so `/ponytail-debt` can find them later.

## Testing

`.coverage-floors` is the contract. Beyond the number:

- Table-driven tests everywhere — adding the 26.2 log format should be a row, not a function.
- Fuzz every parser. A malformed remote `pack.toml` must never panic on a player machine.
- Golden JSON files for every Discord surface, so a layout change is a reviewable diff rather
  than a surprise in the channel.
- `httptest` for all remote fetches. No test touches the network.
- Integration tests behind `-tags integration` — real container, real RCON, real log tail.
- `-race` on everything (CI only, see above).

## Benchmarking

JVM flags are measured, not inherited. Base reference is
`brucethemoose/Minecraft-Performance-Flags-Benchmarks`, four years stale — several of its
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

`jvm-profiles.toml` holds the candidates. Nothing in it is believed until it has a row in
`BENCHMARKS.md`.

`wly bench` preflights every flag with `java <flags> -version` and drops what the JVM
rejects, so a flag removed by a future JDK degrades instead of failing the boot.
