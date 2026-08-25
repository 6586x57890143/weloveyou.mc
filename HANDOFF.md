# Handoff 💖

Notes for whoever picks this up next, including future me. Current as of
**v0.2.1**, *last swept 2026-08-24*.

**Phase numbers in this repo are ambiguous unless you say which plan.** The
master plan (`we-re-planning-a-highly-mutable-wirth.md`) numbers 0-9; the
2026-08-22 reframe (`scope-out-the-simulated-sleepy-canyon.md`) renumbers 1-5
over the same ground. Both are live. CLAUDE.md's status line uses the master's.

**Read this first if you are picking this up now.** The benchmark harness is
finished and trustworthy, and the playable-server work is done and on the box.
Two tracks remain and they do not block each other:

- **The Discord layer** (master phase 2, now broken into D0-D5) is where the
  work currently is. Start at [docs/DISCORD.md](docs/DISCORD.md).
- **The frozen baseline** (reframe phase 3) is one three-hour measurement that
  nobody has run yet. See **Phase 3** below.

If anything here disagrees with reality, reality wins and this file is stale, so
check the live state with the commands below before believing it.

When something gets superseded it's marked in place instead of deleted. The
reasoning behind a reversed decision is usually the expensive bit, and deleting
the line deletes the reason too. See **Keeping these docs honest** in
[CLAUDE.md](CLAUDE.md) for when to sweep and what to look at.

## What exists right now

A joinable modded Minecraft server, a pack that publishes itself, and a
benchmark harness that produces trustworthy numbers and publishes them without
a human in the loop.

| Thing | Where | State |
|---|---|---|
| Server | `weloveyou` @ `100.103.121.9` (tailnet), public `158.180.53.71` | live, healthy, whitelisted |
| Pack | `weloveyou-pack.pages.dev/pack/stable/` | published, v0.1.6 |
| Platform repo | `github.com/6586x57890143/weloveyou.mc` | public, v0.2.0 |
| Pack repo | `github.com/6586x57890143/weloveyou-pack` | public |
| Bench boxes | `weloveyou-bench`, `-2`, `-3`, each A1 2 OCPU/12 GB | powered off unless sweeping |
| Benchmarks | `weloveyou-bench.pox-nugget-mowing.workers.dev` | live with real numbers; publishes automatically |
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

Run 32567742816 was the first sweep that was both correct and actually parallel:
25 profiles across three boxes, screening at `runs=1 radius=400`. Its numbers are
committed and on the site. Every number older than it was produced by a bug, so
do not compare against anything earlier.

Run 32583103771 added the tick workloads at `--load 3`. Between them they are
the entire evidence base; the figures are under **Numbers to carry forward**.

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

### What runs on the boxes, and what is not in this repo

The production box carries machinery none of which is version controlled except
where noted. This has already caused two documented bugs in `provision-box.sh`,
and it is the same gap twice over.

| path on the box | what it does | in the repo? |
|---|---|---|
| `/opt/deploy/deploy.sh`, `compose-up.sh`, `config` | the entire deploy mechanism | **no** |
| `/opt/deploy/bench-power.sh` | start/stop one bench box, `BENCH_NAME=` selects it | **no** |
| `/opt/deploy/provision-box.sh` | new bench boxes | yes, `deploy/provision-box.sh` |
| `/opt/deploy/bench-reaper.sh` | stops idle bench boxes | yes, `deploy/bench-reaper.sh` |
| `/opt/wly/bin/collect.sh`, `notify.sh` | the ntfy ops stack | **no** |
| `/opt/wly/bin/gate.sh` | decides whether triage wakes the model | yes, `deploy/wly-health-gate.sh` |
| `/opt/wly/bin/triage.sh` | asks the model for a verdict, pushes at MEDIUM+ | yes, `deploy/wly-triage.sh` |

Timers on the production box: `wly-health` every 6h (gated), `wly-secaudit`
daily, `wly-cost` daily 06:30, `wly-bench-reaper` every 10min,
`wly-bench-up` Mondays 03:45.

### The ops alerting was paying for silence

`wly-health` fired hourly and called `claude -p` on **every** run. The push floor
is MEDIUM, so almost all of those read a healthy snapshot, returned OK and
pushed nothing: the tokens bought silence. `gate.sh` now decides whether the
model is woken at all, from plain thresholds on fields `collect.sh` already
writes, and the timer moved to every six hours.

Two fields had to be fixed first, because both were permanently non-empty and
would have fired the gate every single run anyway:

- **`new_ports_vs_baseline`** compared text that included `pid=`, so any process
  restart made every port look new. Now stripped alongside `fd=`, baseline
  reseeded.
- **`journal_errors`** carried a single sshd `kex_exchange_identification` line -
  an internet scanner touching port 22, which happens continuously and means
  nothing. The gate filters that class and counts anything else. Authentication
  failures are deliberately **not** filtered.

The gate is scoped to `quick` only. `wly-secaudit` runs the same `triage.sh` in
`full` mode, and gating that would have quietly turned the daily security audit
off - it exists to reason about CVEs and lynis findings on a box that looks
fine, which is exactly what the gate suppresses.

**Two things noticed and not acted on**, because they are decisions rather than
bugs: port 22 is open to `0.0.0.0` on a box whose design is tailnet-only, and
dozzle, a container log viewer, is published on `0.0.0.0:8080`. Both were
already in the old ports baseline, so neither is newly hidden.

**UPDATED 2026-08-24, both halves checked properly.** The port 22 half is worse
than recorded and the dozzle half is no longer true.

- **22 really is open to the internet**, confirmed at the OCI security list and
  by probing `158.180.53.71` from off-tailnet. It is the ONLY port that answers
  there. 25565, 8123 and 24454 are all filtered, so players are reaching the
  server over the tailnet and the public IP serves nothing but SSH. See the
  Deployment section of CLAUDE.md for the full ingress and the fix.
- **dozzle is not on `0.0.0.0` any more.** `ss -lntp` shows it bound to
  `100.103.121.9:8080`, the tailnet address. squaremap on 8123 is still bound to
  `0.0.0.0`, but the security list filters it, so the exposure is a host-level
  bind rather than a reachable port.

The trap worth keeping: **a check run ON the box cannot see the cloud firewall.**
`docker-compose.yml` records squaremap as verified serving 200 through its port
mapping, which was true from the box and irrelevant from the internet.

### Traps that cost real time

**Minecraft closes the RCON connection on an empty command.** Most RCON clients
end a multi-packet reply by sending an empty command and watching for its answer.
Here that drops the socket: every query came back `read size: EOF` while dial and
auth had both succeeded, so the first live status board reported the server UP
with day 0 and 0ms. The end of a reply is inferred from the 4096-byte split
instead. A failure that looks like healthy-but-zero is worse than an error.

**READ_MESSAGE_HISTORY is separate from VIEW_CHANNEL, and missing it returns an
EMPTY LIST rather than a 403.** Denying `@everyone` view takes history with it,
and the bot's grant only restored view and send. `GET /messages` then comes back
`[]`, so `wly surfaces` concludes it has never posted there and posts again on
every run. Every private surface would have grown duplicates instead of one
edited message. `Channel.Overwrites` grants it now.

**Discord embeds cannot be sized, and three ways of faking it do not work.** A
Components V2 Container has no width property and takes the width of its widest
child. Padding with U+2007 is STRIPPED from message content, a Separator has no
intrinsic width, and a Section accessory sits after the text. Only a Media
Gallery image has a real width, which is why `assets/feed-strip.png` exists: it
is layout that also carries the brand, not an ornament.

**The cost report was never mounted into the wly container.** It reads
`/var/lib/wly/cost.json`, which lives on the host, so the spend surface would
have reported "no cost report on the box at all" for ever while the box had one.

**Discord silently mangles channel names, and answers 200 while doing it.** The
emoji-prefix convention (`💬│general`) needs a separator, and picking one is
measurement, not taste. Discord rewrites every character Unicode calls
whitespace into a hyphen, and strips every zero-width one, returning success
either way. Applied one candidate per channel on the live guild and read them
back: U+00A0 became a hyphen; U+2800, U+3164 and U+2063 were stripped; U+2502
`│` survived. U+17B5 is the one that looks like a win and is not, it survives
storage but measures **0px wide** in Discord's own font, so the file matches, the
reconciler goes quiet, and the name renders exactly as if there were no
separator. A stripped separator is worse than none: the file can then never
match what Discord stored, so the plan reports the same rename forever and stops
meaning anything.

**Renaming a channel is rate limited to two changes per ten minutes, per
channel**, and `retry_after` comes back in the 180-450 second range rather than
the seconds an API rate limit usually means. A rename sweep across eight
channels does not finish in one `--apply`, which is normal rather than a
failure. Apply says which cause it hit and carries on to the channels it can
still touch, because a fixed block of "check your permissions" printed under a
rate-limit error is the confidently-wrong output this repo exists to avoid.

**A private channel locks the bot out of itself.** `visible_to` denies
`@everyone` VIEW\_CHANNEL, which denies it to `wly` too, and `wly` is the only
thing that ever writes the pinned surface in there. Worse, it cannot fix itself:
granting the access requires editing a channel it cannot see, so the request
that would repair it is the one being refused. Recovering needs a human in
Server Settings. `Channel.Overwrites` now emits the bot's own view+send grant in
the same payload as the deny, so the two land atomically and no channel can be
created into that state again. That applies to temporary event channels exactly
as much as to `#ops`.

**A compose profile does not gate interpolation.** Adding a `db` service with
`POSTGRES_PASSWORD: "${DB_PASSWORD:?...}"` broke `docker compose up -d` on every
box that had not set it, including one that only runs Minecraft. Putting the
service behind `profiles: ["bot"]` did NOT fix it: compose interpolates every
service in the file regardless of profile, so a required variable anywhere fails
the whole parse. The fix is `${DB_PASSWORD:-}` and letting the postgres image
refuse to initialise without a password, which it does with a clear message, at
the moment someone actually starts it. Second time in one day the same principle
bit, after the same argument was made for not putting a `:?` on
`WLY_DISCORD_TOKEN`.

**A merged branch still accepts pushes.** Four times in one session, work was
pushed to a branch whose PR had already been squash-merged, and it silently went
nowhere: the commits look fine, `git log` looks fine, and the content is simply
absent from `main`. Squash-merging also means your own commits are never
ancestors of `main`, so `git merge-base --is-ancestor` reports MISSING for work
that did land. **Check `gh pr view <n> --json state` before every push, and
verify landed work by grepping `main` for content markers, case-insensitively.**


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

## What is next: make it a product, then stop

**Reframed 2026-08-22.** The benchmark works and has produced real numbers. The
project stops being an optimisation experiment here. The remaining goal is a
server that is pleasant to play, one written-down baseline, and then no more
tuning. The full plan is at
`~/.claude/plans/scope-out-the-simulated-sleepy-canyon.md`.

### Phase 1, harness trustworthiness: DONE

Results now say what they measured and publish themselves.

- Every result records the pack version and its **index hash** (the content
  fingerprint that changes if any file in the pack changes), Minecraft and
  Fabric loader versions, the container image, the JVM's own version string,
  the flags that survived preflight, and how many runs were attempted versus
  finished.
- `Env` **pins the Fabric loader from the pack**. It used to set `TYPE=FABRIC`
  with no version, so the image resolved "latest for 1.21.1" at container start
  while production compose pinned `0.19.3` - the bench could silently stop
  measuring what production runs.
- `wly bench --validate` is the gate. It blocks a sweep that is misleading as a
  whole: nothing measured, a dead **baseline** (every `vs base` would compare
  against zero), rows against different packs or different machines, impossible
  numbers, or a pack workload that loaded no mods. A single crashed profile does
  **not** block: it is a finding, renders as `FAILED`, and publishes.
- On a pass the merge job commits to `main` and `pages.yml` deploys. On a
  failure the numbers park on `bench/held-*`. **There is no human step any
  more**, which is what fixed findings never reaching the page.

### Phase 2, make the server playable: DONE, 2026-08-23

Everything below came out of a full audit of `deploy/docker-compose.yml` on
2026-08-22. It is what EXISTS, not speculation. The code half is now done; the
install half needs a restart window, because **Phase 2 touches the server
players use**.

**Backups exist and have been restored once.** Installed on the box 2026-08-23,
first archive taken by hand and watched, restore verified into a throwaway
volume. `wly-backup.timer` is armed for 04:30 UTC daily.

| file | what it is |
|---|---|
| `deploy/backup.sh` | RCON `save-off` / `save-all flush` / tar / `save-on`, prune to two, rsync one off-box, alert on staleness. `--check` alerts only. |
| `deploy/wly-backup.service` | oneshot, `EnvironmentFile=-/etc/default/wly-backup`, idle IO |
| `deploy/wly-backup.timer` | 04:30 UTC, `Persistent=true` |

Install, already run on the production box:

```bash
sudo install -m755 deploy/backup.sh /opt/deploy/backup.sh
sudo install -m644 deploy/wly-backup.{service,timer} /etc/systemd/system/
echo 'BACKUP_TARGET=kon@<desktop-tailnet-ip>:/backups/wly' | sudo tee /etc/default/wly-backup
sudo systemctl daemon-reload && sudo systemctl enable --now wly-backup.timer
sudo /opt/deploy/backup.sh          # take the first one by hand and watch it
```

**`BACKUP_TARGET` is still empty**, so there are two copies on the box and none
off it. Setting it needs an ssh key for root that reaches the desktop, which is
the one step here nobody can do for you. Until then the staleness alert is the
only thing standing between an off-box copy and nobody noticing there is not
one.

**The restore, verified 2026-08-23**, which is what makes this a backup rather
than a script:

```bash
docker volume create wly-restore-test
docker run --rm -i --entrypoint tar -v wly-restore-test:/data   itzg/minecraft-server:java25 -xzf - -C /data < /var/lib/wly/backups/world-*.tar.gz
```

Came back: `world/level.dat`, 15 region files, `world/playerdata`,
`server.properties`, `whitelist.json`, `ops.json`, `config/`. No `mods/`, which
is correct: it is excluded and comes back from `PACKWIZ_URL` on the next start.
A 273M `/data` archives to 36M.

Four decisions in it worth not relitigating:

- **The tar runs inside the container** (`docker exec wly-mc tar`), not against
  the volume path on the host, so it needs no knowledge of the compose project
  prefix (`deploy_mc-data`) and pulls no second image.
- **`save-on` is a trap, not a line**, so an interrupted backup never leaves the
  server with saving disabled. That failure is silent and lasts until a restart.
- **tar exiting non-zero is normal** on a live server ("file changed as we read
  it"). `gzip -t` decides whether the archive is actually usable, so a routine
  warning does not throw away a good backup and a truncated stream still fails
  loudly.
- **Mods, libraries, cache and logs are excluded.** They come back from
  `PACKWIZ_URL` on the next start and they are most of the bytes.
- **A failed rsync is not high priority; staleness is.** The desktop being off
  is the expected case, and two local copies mean it never leaves zero. What
  escalates is the newest archive passing 36h, which covers a failed run, a
  timer that never fired, and a container down for a week, all the same way.

**SUPERSEDED 2026-08-23: the restore has now been done**, into a throwaway
volume, and the exact command is in the block above. What was written, and why
it still matters:

> **Still open on backups: nothing has ever been restored.** An untested restore
> is not a backup.

That remains the standard. What is still open is narrower and is stated above:
`BACKUP_TARGET` is empty, so there are two copies on the box and none off it.

**The compose audit findings are fixed, in `deploy/docker-compose.yml`:**

- **Voice chat could never have worked.** `"25565:25565"` has no protocol
  suffix, so Docker published TCP only and no UDP reached the container at all.
  `24454/udp` is now published. It must match `port` in
  `config/voicechat/voicechat-server.properties`; `-1` there means "reuse the
  game port", which would need `25565/udp` published instead. **Confirmed on the
  box 2026-08-23: `port=24454`.** The mod is in `pack/stable`, side `both`, and
  the pack ships no config, so the server wrote the mod's own default.
- **squaremap was not in the pack at all**, which is why the map never worked.
  "Unreachable because no port is published" was one layer short: there was
  nothing listening to reach, and `wly.toml`'s `tiles_dir` had pointed at a mod
  that never shipped since phase 0. Fixed on 2026-08-23 by adding it to
  `pack/stable` v0.1.7, `side = "server"` (Modrinth declares its sides unknown,
  so packwiz assumed universal; a client has nothing to do with server-rendered
  tiles). The compose port followed the mod, in that order and deliberately.
  **Verified live after the v0.2.1 deploy**: the mod writes
  `/data/squaremap/config.yml`, not `config/squaremap/` where every other mod
  puts its config, with `internal-webserver` enabled on `0.0.0.0:8080`; tiles
  land in `/data/squaremap/web/tiles`, which is exactly what `wly.toml` has
  claimed all along; and `http://100.103.121.9:8123/` answers 200.

  **It also opened a port, which the health gate is built to notice.**
  `new_ports_vs_baseline` fired on the very next snapshot, and left alone it
  would spend a model call every six hours reporting a port we opened on
  purpose. Reseed after any deliberate port change:

  ```bash
  sudo cp -a /opt/wly/baseline/ports /opt/wly/baseline/ports.pre-squaremap
  sudo rm -f /opt/wly/baseline/ports          # collect.sh reseeds when absent
  sudo /opt/wly/bin/collect.sh quick
  sudo /opt/wly/bin/gate.sh /var/lib/wly/quick.json   # want exit 1, "quiet"
  ```

  squaremap 1.3.2 is the newest build for 1.21.1; it logs that it is "14
  versions out of date" against 1.3.15, which targets later Minecraft. That
  line is noise here, not an upgrade to chase.
- **`VIEW_DISTANCE` and `SIMULATION_DISTANCE` were unset**, so they sat at
  itzg's 10/10 on a two-core box while fifteen JVM flags were argued line by
  line above them. Now 8/6, and simulation is the lower of the two on purpose:
  it drives what actually ticks, and `explore` showed travel saturating both
  cores at 65ms p95. Raise them against a measured row, not a feeling.
- **`STOP_SERVER_ANNOUNCE_DELAY: 30`**, so a deploy warns players instead of
  dropping them mid-sentence. Well inside the 120s stop grace period.
- **The base image is `${MC_IMAGE:-itzg/minecraft-server:java25}`.** The tag is
  still moving and compose still pulls before every up, but there is now a
  rollback lever that matches `WLY_IMAGE`: pin a dated tag in `.env`, no rebuild.

`OVERRIDE_SERVER_PROPERTIES: "true"` is left alone deliberately: it reverts hand
edits on the box at every restart, which is the point, since compose is meant to
be the only source of server.properties.

### Phase 3, the frozen baseline

Production runs `itzg/minecraft-server:java25` with `-XX:+UseCompactObjectHeaders`
and that combination has **never been tick-tested**. The only config with tick
evidence is `baseline-j21`. One final measurement is agreed - explore, village
and machines on `baseline-j21`, `j25-g1-coh` and `j21-graalvm-g1-bruce`, at
`--runs 3 --load 3`, roughly three hours - and then `production-2vcpu` is written
into `jvm-profiles.toml` as the single source compose reads, and `PRODUCTION.md`
records exact versions. You cannot freeze a baseline you have not measured; after
that, the profile changes only for a reproducible crash, a correctness bug, a
real gameplay problem, or a measured regression. Not for 1-3%.

### The Discord layer, master phase 2: IN PROGRESS

The design pass, D0. No Go, by design - the master plan puts the layouts before
the bot so the layout work is not thrown away. What landed:

| file | what it is |
|---|---|
| `docs/DISCORD.md` | the reference: limits, colour system, data sources, perk sheet, the Activity |
| `internal/discord/testdata/surfaces/*.json` | the surfaces, as real Components V2 payloads. Mockup and golden file are the same artifact. |
| `scripts/discord-mocks.py` | renders them into one comparable page, and validates against the API's limits while it does |
| `guild.toml` | the Discord server declared. **D1 is done and applied**: roles, hierarchy, categories, channels, channel permissions, names and emoji all reconcile. |
| `internal/discord/surface.go`, `surfaces.go` | **D2, in progress**: the six surfaces built in Go and checked against the D0 payloads as golden files. |
| `cmd/wly surfaces` | posts and edits the pinned messages. Finds its own message by author rather than storing an id, because Discord already knows and a second source of truth can disagree with what people are looking at. |

**D2 status: get-started is live in `#👋│start-here`.** The other five are
blocked on data that does not exist yet and `wly surfaces` says so per surface
rather than posting a plausible hole: status needs RCON and the log tail, spend
needs `/var/lib/wly/cost.json` which is on the box, events needs the log bridge,
map needs a published render (`weloveyou-pack.pages.dev/assets/map-latest.png`
currently answers 200 with `text/html`, which is the Pages fallback, not an
image), and release is an event rather than something reconciled.

**The mockup is the payload**, so a design cannot promise a layout Components V2
refuses to build. `ci.yml` runs the validator on every PR for the same reason it
runs `bench-site.py`: nothing else would, until it failed somewhere expensive.

Three things found while designing it that are worth knowing before D1:

- **Minecraft already writes the leaderboard.** `world/stats/<uuid>.json` and
  `world/advancements/<uuid>.json` carry playtime, deaths, distance and blocks
  per player, and `wly` already mounts `mc-data:/mc:ro`. There is no stats
  pipeline to build; `internal/mcevents` only needs the realtime half.
  **Unverified on the box** - confirm the directory is populated first.
- **Linked roles cap at 5 metadata keys**, and the design spends all five
  (playtime, joined, donor, first-joined, deaths). A sixth gate replaces one.
- **Components V2's flag is irreversible per message.** The status board is
  edited in place forever, so that is a one-way door on its first post.

### Phases 4 and 5

An eight-hour soak on a bench box against a throwaway world, reporting
first-hour versus last-hour so "steadily increasing tick time" becomes a number
rather than an impression. Then `LIMITATIONS.md` and stop.

## Open work at the moment of this handoff

- **PR #24** `harness-gate` - the validator and automatic publishing. Open,
  CI green. Nothing else depends on it landing first.
- Everything else from today is merged: #16, #19, #21, #22, #23.
- **All three bench boxes are STOPPED.** `bench-reaper` is armed on the
  production box every ten minutes and its hold file is cleared.
- `deploy/bench-reaper.sh` and `/opt/wly/bin/gate.sh` are installed and live.

Two loose ends worth knowing about:

- **The sweep still needs the bench box powered on before a manual dispatch.**
  The runner *is* the bench box, so nothing in the workflow can start it, and
  the production box's SSH is a forced command that accepts only `deploy <ref>`.
  The weekly cron is covered by `wly-bench-up.timer`. Documented, not solved.
- **`bench.yml`'s `workload` input is a `choice`**, so GitHub rejects a comma
  list like `explore,village` even though `ParseWorkloads` accepts one. Use the
  `players` alias, or make it a free-text input.

## Numbers to carry forward

Measured on `weloveyou-bench-2`, `baseline-j21`, radius 400, one run each. Idle
MSPT p95 on this box is 0.2-0.7ms, so all of these are real load.

| workload | `--load 1` | `--load 3` | notes |
|---|---|---|---|
| explore | died | **65.2ms p95**, 43.3ms median, **19.97 TPS**, 200% CPU | past the 50ms tick budget; the only reading where the server cannot keep up |
| village | 10.7ms | 17.3ms p95, 20 TPS, 111% CPU | 120 villagers |
| machines | 9.6ms | 10.5ms p95, 20 TPS, 46% CPU | 9 Oritech rows |

Worldgen, 25 profiles, screening pass: baseline J21 ~11.6 chunks/s, tuned J21
~12.2, GraalVM J21 ~15.0 (+29.7%). GraalVM's win is on **vanilla worldgen only**
- on the real pack every profile lands inside the noise floor. That is the
finding the two-workload split exists to produce, and it is still a single run
with no variance.

**`--load 3` is the calibrated setting.** Lower and village and machines barely
leave idle. Rows taken at different `--load` are not comparable.

**Travel costs this box more than entity count does.** explore is comfortably
the heaviest of the three, and it is the workload that saturates both cores.

## The plan

`~/.claude/plans/we-re-planning-a-highly-mutable-wirth.md` is the source of
truth for intent and phase order. It is long but it explains *why* for every
decision here, including the ones that were reversed.
