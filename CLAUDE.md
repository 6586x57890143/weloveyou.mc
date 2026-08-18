# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## What this is

`weloveyou.mc` — a Fabric modpack plus the platform that ships it: a Dockerized Minecraft
server, packwiz-versioned mods delivered by our own Go updater, Discord as the entire
front-end, and a squaremap live map served from Cloudflare R2.

Two Go binaries, one Cloudflare Worker, one packwiz repo. Plan lives at
`~/.claude/plans/we-re-planning-a-highly-mutable-wirth.md`.

**Status: phase 0 (skeleton and CI).** Nothing is deployed yet.

## Commands

```bash
go build ./...
go test ./...                      # all green
go test -race -count=1 ./...
go vet ./...
gofmt -l cmd internal              # must print nothing

scripts/coverage.sh                # enforce .coverage-floors — this is the CI gate
scripts/coverage.sh --report       # same table, always exit 0

go run ./cmd/wly version
go run ./cmd/wlyup version
```

Requires Go 1.26.6 (`go.mod` pins it; 1.26.4 and .5 carry `crypto/tls` and `net/http`
advisories that `govulncheck` fails CI on). `packwiz` must be on PATH to touch `pack/`.

## Architecture

```
cmd/wly/            server daemon — Discord bot, log bridge, RCON, R2 sync, bench harness
cmd/wlyup/          player-side pack updater, single static binary
internal/buildinfo/ version stamp injected by -ldflags, shared by both binaries
internal/packwiz/   PURE: pack.toml/index.toml parsing, resolution, hashing, diff, sync
internal/mcevents/  PURE: log line -> event, one regex table per MC generation
internal/bench/     JVM flag profiles, workload driver, result table
worker/             Cloudflare Worker, R2 binding, read-only
pack/stable/        packwiz pack — MC 1.21.1 Fabric
pack/edge/          packwiz pack — MC 26.2 Fabric (later)
deploy/             Dockerfile for wly, docker-compose.yml for mc + wly
scripts/            CI helpers that must also run by hand
```

**Dependencies point inward.** `internal/packwiz` and `internal/mcevents` are pure: no
Discord, no Docker, no network of their own. That is what makes them cheap to test to 95%
and what would let `internal/packwiz` compile to `GOOS=js` if a browser use ever appears.

**The Worker is read-only.** `wly` writes to R2 over the S3 API with a scoped token. There
is deliberately no authenticated write path at the edge.

## Conventions

- **Line endings are LF**, enforced by `.gitattributes`. A CRLF shell script or Dockerfile
  breaks the Linux runners in ways that are tedious to diagnose from a Windows checkout.
  Write files with bash heredocs; Python's `write_text` silently emits CRLF on Windows.
- **CI uses no third-party actions** outside `actions/*` and `dependabot/fetch-metadata`.
  Release workflows hold write access to the repo and registry — every action they run is
  part of this project's supply chain. Everything else is plain CLI.
- **Coverage floors ratchet upward only.** Raise a floor when a package earns it; never
  lower one to make a red build green. `.coverage-floors` carries the reasoning.
- **Keep the dependency list short.** Adding one needs justification in the commit message.
- **Mark deliberate shortcuts** with a `ponytail:` comment naming the ceiling and the
  upgrade path, so `/ponytail-debt` can find them later.

## The invariant that matters most

**Every entry in every packwiz pack declares an explicit `side`.** An unset side defaults to
both, which quietly ships Sodium to the server and costs real TPS. CI's `pack` job enforces
this; do not weaken it.

## Testing

`.coverage-floors` is the contract. Beyond the number:

- Table-driven tests everywhere — adding the 26.2 log format should be a row, not a function.
- Fuzz every parser. A malformed remote `pack.toml` must never panic on a player's machine.
- Golden JSON files for every Discord surface, so a layout change is a reviewable diff
  rather than a surprise in the channel.
- `httptest` for all remote fetches. No test touches the network.
- Integration tests behind `-tags integration` — real container, real RCON, real log tail.
- `-race` on everything; `wly` shares state across goroutines.

## Benchmarking

JVM flags are measured, not inherited. Base reference is
`brucethemoose/Minecraft-Performance-Flags-Benchmarks`, four years stale — several of its
conclusions need re-testing (ZGC's section is obsolete, compact object headers didn't exist).

Two rules that are easy to get wrong:

1. **Benchmark against a representative pack**, not vanilla. Workload A is vanilla worldgen
   as a control; workload B is the same run against the real pack. A flag that wins on A and
   loses on B is a finding, not noise.
2. **Never benchmark on a GitHub-hosted runner.** They are noisy shared VMs; the numbers
   would be fiction. `bench.yml` targets a self-hosted runner on the bench box.

`wly bench` preflights every flag with `java <flags> -version` and drops what the JVM
rejects, so a flag removed by a future JDK degrades instead of failing the boot.
