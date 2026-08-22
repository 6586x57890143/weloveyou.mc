# Handoff 💖

Notes for whoever picks this up next, including future me. Current as of
**v0.2.0**, *last swept 2026-08-22*. If anything here disagrees with reality,
reality wins and this file is stale, so check the live state with the commands
below before believing it.

When something gets superseded it's marked in place instead of deleted. The
reasoning behind a reversed decision is usually the expensive bit, and deleting
the line deletes the reason too. See **Keeping these docs honest** in
[CLAUDE.md](CLAUDE.md) for when to sweep and what to look at.

## What exists right now

A joinable modded Minecraft server, a pack that publishes itself, and a
benchmark harness that still has not produced a full table, for reasons
now understood and fixed.

| Thing | Where | State |
|---|---|---|
| Server | `weloveyou` @ `100.103.121.9` (tailnet), public `158.180.53.71` | live, healthy, whitelisted |
| Pack | `weloveyou-pack.pages.dev/pack/stable/` | published, v0.1.6 |
| Platform repo | `github.com/6586x57890143/weloveyou.mc` | public, v0.2.0 |
| Pack repo | `github.com/6586x57890143/weloveyou-pack` | public |
| Bench boxes | `weloveyou-bench`, `-2`, `-3`, each A1 2 OCPU/12 GB | powered off unless sweeping |
| Benchmarks | `weloveyou-bench.pox-nugget-mowing.workers.dev` | live; publishes on a merge to main |
| Spend | `/var/lib/wly/cost.json` on the server | ~€0.03/day, credits expire 2026-09-15 |

Minecraft 1.21.1, Fabric, Java 25, 71 pack entries, 143 mods loaded server-side.

The three bench boxes must all stay A1 with 2 OCPUs. They exist to measure the
machine that serves players, and an A2 or a larger shape measures something else.

## Check the live state in one go

```bash
ssh -i ~/.ssh/oracle-weloveyou ubuntu@100.103.121.9 '
  sudo -u deploy git -C /srv/app describe --tags
  sudo docker ps --filter name=wly-mc --format "{{.Status}}"
  /opt/deploy/bench-power.sh status
  cat /var/lib/wly/cost.json'
curl -s https://weloveyou-pack.pages.dev/pack/stable/pack.toml | head -3
```

## How a change reaches players

Two repos, two pipelines, and they meet at a URL rather than at a deploy.

```
weloveyou.mc     tag v* -> build -> GHCR -> tailnet -> forced command -> /srv/app -> compose up
weloveyou-pack   tag stable-v* -> validate -> deps -> smoke boot -> wrangler -> Pages
                                                                        |
                              server fetches PACKWIZ_URL on restart <---+
                              Prism fetches the same URL on launch  <---+
```

A pack release deploys nothing. The server picks it up on its next restart and
players on their next launch. That is the whole point of the split.

**Tagging** (annotated only, `--verify-tag` rejects lightweight tags):

```bash
git tag -a v0.2.1 -m "..."          && git push origin v0.2.1        # platform
git tag -a stable-v0.1.5 -m "..."   && git push origin stable-v0.1.5 # pack
```

The pack's channel is parsed out of the tag, so `stable-v*` not `v*`.

## The invariant that keeps biting

**A mod's `side` must be right, and nothing structural can tell you it is.**
`pack-check.sh` proves a side is *declared*. Whether it is *correct* depends on
what other mods need, which only booting reveals. Four bugs shipped on day one
this way:

- Oritech hard-depends on athena, which Modrinth marks client-only → server died
- Terralith depends on lithostitched, which we shipped server-only → client died
- C2ME wants java >=25; Prism gives clients 21 → client died
- Spark's async-profiler segfaults the JVM on Java 25/aarch64 → ten restart loops

Three gates now catch these, all in the pack repo's CI:

- `scripts/pack-check.sh`, every entry declares a side, index is fresh
- `scripts/deps-check.py`, every declared dependency is satisfied *per side*,
  descending into nested jars (Fabric API bundles, and C2ME hides its java
  requirement inside `c2me-opts-natives-math`)
- `scripts/smoke-boot.sh`, boots a real Java 25 server and requires `Done (`

Each was verified by reverting the real bug and watching it fail.

## Things that will surprise you

- **Oracle Always Free is 2 OCPU / 12 GB**, halved 2026-06-15 with no
  announcement. The production box alone is the whole allowance; the bench box
  is on credits.
- **A1 has no capacity for new instances** in any Frankfurt AD, but **A2 does,
  and converting an A2 to A1 works.** `/opt/deploy/provision-box.sh` does this.
  The conversion is asynchronous and reports the old shape while it lands, which
  looks exactly like a failure.
- **Stopping an OCI instance is a small gamble** on getting it back.
- **Both repos are public** so Actions is free, and `weloveyou.mc` has a
  self-hosted runner. That is safe only because nothing a fork can trigger
  targets it. `scripts/check-runners.py` enforces it. Never add `pull_request`
  to `bench.yml`.
- **Writing files from Windows**: use bash heredocs. Python's `write_text` uses
  the locale codec and mangles em-dashes into bytes no YAML parser accepts; it
  cost two broken pushes. Backticks in `git commit -m "..."` get shell-expanded
  , use `git commit -F -` with a quoted heredoc.
- **The exec bit does not survive a Windows checkout.** New scripts need
  `git update-index --chmod=+x`.

## Where things actually stand, 2026-08-22

A sweep is running as this was written: run 32567742816, 25 profiles split
across three bench boxes, screening pass at `runs=1 radius=400`. It is the first
one that is both correct and actually parallel. Read its numbers before deciding
anything about hardware, because every earlier number was produced by a bug.

### The benchmark was broken three separate ways, all now fixed

**Sampling ran per log line.** `Timeouts.Sample` was declared and never read, so
`docker stats --no-stream` (one to two seconds each) and `rcon-cli spark tps` ran
for every line the server printed. Each spark reply is about ten more lines, each
triggering another sample, so the cost compounded. A seven minute run took
forty-five, and every profile reported roughly 1.0 chunks/s, which looked like
the flags making no difference. The same box did 8.1-8.4 before spark was added.

**Results were only written at the end.** A sweep that died partway lost
everything. One ran seven hours, completed nine runs and saved nothing. Reports
are now written after every profile.

**The idle timer killed a healthy sweep.** `wly-bench-idle.timer` on the
production box fired at 23:30 UTC and stopped a running job. It is now disabled
there, replaced by `wly-bench-selfstop.timer` on each bench box, which checks for
a `Runner.Worker` process or a `bench-` container first. Reinstall it after
reprovisioning with `bench-admin.yml -> self-stop`; only the bench box can see
whether a job is running, because the production box holds no key for it.

**And the shards were not sharding.** `bench.yml` had the matrix, the job names
said 1/3, 2/3 and 3/3, and all three boxes worked through the same 21 profiles in
lockstep for hours. The workflow never passed `--shard` or `--raw`. Nothing
failed and nothing warned; the only symptom was three machines running an
identically named container. `check-runners.py` now fails the build if the matrix
and the flag ever disagree again.

### Traps that cost real time

**A2 is not A1.** Both boxes provisioned on 2026-08-22 came up as
`VM.Standard.A2.Flex`. A2 is AmpereOne with two vCPUs per OCPU against A1's one,
so 2 OCPU means four threads rather than two. `provision-box.sh` broke out of its
wait loop on `RUNNING` without ever checking the shape, so an unconverted box was
reported as a success. Caught by hand before either took a shard. The script now
refuses to return anything that is not A1, and lives in the repo at
`deploy/provision-box.sh` so the next bug like it is visible in a diff.

**Every new box came up unreachable.** Cloud-init installed tailscale and never
ran `tailscale up`, and there is no public SSH by convention, so a fresh box had
no way in and no runner yet to reach it through. Cloud-init now joins at first
boot. Use a **one-off, non-ephemeral** key: one-off limits how many devices a key
can add, ephemeral deletes the node once it goes offline, and they are
independent. The repo's existing `TS_AUTHKEY` is ephemeral, which is right for
`release.yml` and wrong for a box that is powered off most of the time. Do not
use a reusable key; the box authenticates once in its life.

**Async deadlocks this server.** It parallelises entity ticking, works well on a
twelve-thread desktop, and on two cores its workers contend with the server
thread waiting on them. A player joins, `invokeAll` never returns, the tick stops
(which is what "cannot interact with anything" was), and sixty seconds later the
watchdog force-kills the server. It crash-looped production every few minutes
until it was dropped in `stable-v0.1.6`. Recreating the world would not have
helped: the trigger is a player, and a fresh world has one the moment you log in.

That is a hardware finding rather than a mod bug, and unlike the benchmark
numbers it came from a real failure. If the mods worth having assume spare cores,
two Ampere cores is the wrong host, and no flag sweep fixes that.

The `village` workload is now the regression test for this. It is entity ticking
with a player present, which is the trigger, and a watchdog kill is recorded and
named in the report rather than showing up as a run that quietly failed.

**Pack releases do not reach the server on their own.** It ran the old 17-entry
pack for three days while v0.1.5 was published, because a release only takes
effect on restart and nothing restarts it. This is the gap `wly serve` exists to
close.

### What the first real sweep got wrong about itself

Run `32567742816` measured fine and then misreported three things. All three are
fixed; they are recorded because each looked like a fact.

**It named the wrong machine.** `BENCHMARKS-screening.md` said
`host: runnervm76f27`, which is the ubuntu-latest merge runner: `mergeShards`
took `os.Hostname()` at render time. The numbers came off `weloveyou-bench`,
`-2` and `-3`. `Result.Host` is now recorded where the measuring happens, the
merge step no longer claims a hostname at all, and a shard that predates the
field reports `unrecorded` rather than borrowing whoever rendered it.

**A profile that never ran was published as a score.** `j25-shenandoah-gen`
failed every run, and because a failed run is skipped it ended with no runs,
so every median came out zero: `0.0 chunks/s`, `-100.0%`, `0 B`. It now renders
as `FAILED`. An absence and a catastrophic result are not the same claim.

**MSPT and TPS on the worldgen workloads are confirmed noise, with numbers.**
Workload B reported TPS exactly `20.0` and MSPT p95 between `0.5ms` and `0.9ms`
on all 25 profiles; workload A reported `50.0ms` on the nose for seven of them
and `2.1ms` for another. This is the measurement behind the claim below that the
instrument had no load. The site now says so on every worldgen table rather than
publishing fifty rows of idle as though they were a result.

The one finding that survives all of that: **GraalVM takes +29.7% on vanilla
worldgen and lands inside the noise floor on the pack.** A flag that wins on A
and loses on B is exactly what the two-workload split exists to surface. It is
still a single run with no variance, so it is a reason to confirm, not a result.

### What the benchmark still cannot answer

**SUPERSEDED: the load now exists.** What was true, and why it was written:

> **TPS and MSPT are wired end to end but mean nothing yet.** spark reports them
> (with `-Dspark.backgroundProfiler=false`, or its async-profiler segfaults the
> JVM on Java 25/aarch64), and they reach the table, the JSON and the site. But
> worldgen pregeneration barely ticks entities, so TPS reads 20 on every profile.
> The instrument exists; the load does not. **Simulated players are the missing
> piece**, and they are what would have caught Async before it reached production.

Three tick workloads now supply it, driven over the same `rcon-cli` path the
sweep already used. Carpet is pinned as a third instrument next to spark and
Chunky and loaded through `MODS=`; it is deliberately NOT in the pack, because a
fake-player mod has no business shipping to players.

| workload | the load | why it is the one worth measuring |
|---|---|---|
| `explore` | carpet fake players teleported outward along fixed bearings, mob spawning left ON | chunk load churn plus the spawning that follows a moving player; the dominant real cost on a small server |
| `village` | 40 villagers on a filled composter grid, natural spawning OFF | entity AI and pathfinding, the heaviest load vanilla has and the one lithium changes most |
| `machines` | a powered Oritech array fed by `oritech:creative_storage_block` | block entity ticking and energy network cost, which is the pack's own signature |

Three things about them that are easy to get wrong:

- **They are read by MSPT p95, not TPS.** TPS is capped at 20, so a load that is
  merely heavy reads 20 on every profile and reproduces the exact blind spot
  above. p95 tick duration is unbounded and moves long before TPS does.
- **The bots are teleported, not pathfound.** A walking bot gets stuck on
  terrain, drowns, or falls into a ravine, which would make the load depend on
  where the fixed seed happened to put one. Teleporting is terrain-independent
  and repeatable, and elytra travel is how a player really churns chunks anyway.
  For the same reason `village` and `machines` build on a stone platform in the
  air rather than on whatever terrain spawn produced.
- **`--load` is a required calibration step, not a nicety.** A workload whose
  load cannot move p95 measures nothing while still printing a plausible row.
  Run `--only baseline-j21 --workload players --runs 1` and raise `--load` until
  p95 clears the idle floor, then leave it there.

**`machines` still needs one verification pass on a real server.** Confirm the
pulverizer is a single block rather than a frame-and-core multiblock and that the
pipes connect as placed; `oritech:powered_furnace_block` is the fallback. Until
that is done it is capable of measuring an empty platform and reporting a number
for it, which is the same failure mode `jvm-profiles.toml` guards against for
JVM flags the JVM accepts and then ignores.

**A watchdog kill is now a recorded outcome**, not a missing row. That is the
Async failure mode exactly, and reported as a container flake it would be
debugged as one.

**Every result now records its hardware**: model, cores, threads per core, RAM,
arch. A table that mixes machines says so, in the report and on the site. That is
what makes "this mod needs cores" a column rather than folklore.

### The profile matrix

25 enabled profiles, two dimensions crossed: Temurin 21 and 25, GraalVM 21 and
25, against G1, Parallel, Serial, ZGC and generational Shenandoah, plus a heap
sweep at 4/6/8/10G on the shipping profile. Four `wly-*` profiles are tuned for
this box specifically rather than copied from a desktop guide; the reasoning is
in `jvm-profiles.toml` next to each flag. Nothing in it is believed until it has
a row.

Some combinations are impossible and are declared disabled with the reason:
GraalVM does not support Shenandoah, and JDK 26, Oracle JDK and OpenJ9 all need
an image that does not exist yet.

## What is next

**Reordered again 2026-08-22.** The benchmark took priority over the Discord
work because the pack tripled in size and a mod that looked like a performance
win crash-looped production instead. Simulated players were the next real piece,
and the harness for them has landed (see above). What remains is not code: the
sweep itself has never produced a committed row, so `BENCHMARKS.md` is still the
placeholder and no flag choice here is measured. Run the three passes it
documents before trusting anything about this box's JVM configuration.

**Reordered 2026-08-19.** Pack development came first: `weloveyou-pack` now has
`scripts/pack-dev.sh` (add/rm/check/play), so a mod change can be joined on a
local server before anything is published. `wly bench` then grew from six
profiles to an 18-profile JDK x collector matrix.

Then phase 2, **the Discord design passes**, deliberately not code. Five
surfaces need mockups before any bot is written, the get-started card first
because it is where a new player either joins or gives up. The plan asks for
2-3 distinct directions and real user input on each.

Then phases 4-6: `internal/packwiz` (Load + Diff only), `internal/mcevents`,
then `cmd/wly serve`: the bot, the event bridge and the status board.

`wly serve` is still a stub that exits immediately, which is why the `wly`
service sits behind a `bot` compose profile and does not start.

**One standing requirement:** `wly`'s daily push must include a spend line from
`/var/lib/wly/cost.json`. Credits drain quietly and the first symptom would be
a stopped server.

That requirement is met on the phone but not yet in Discord. `wly-cost.service`
now has a second step, `/opt/wly/bin/cost-push.sh`, which renders the report to
ntfy daily at 06:30 UTC and shouts when it is missing, null, stale, spiking, or
projecting past the budget. When the bot lands it should read the same file and
say the same thing in the channel; the thresholds are in that script, not in Go.

## The plan

`~/.claude/plans/we-re-planning-a-highly-mutable-wirth.md` is the source of
truth for intent and phase order. It is long but it explains *why* for every
decision here, including the ones that were reversed.
