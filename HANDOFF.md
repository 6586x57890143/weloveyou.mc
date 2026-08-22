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
| Pack | `weloveyou-pack.pages.dev/pack/stable/` | published, v0.1.4 |
| Platform repo | `github.com/6586x57890143/weloveyou.mc` | public, v0.2.0 |
| Pack repo | `github.com/6586x57890143/weloveyou-pack` | public |
| Bench box | `weloveyou-bench`, A1 2 OCPU/12 GB | **STOPPED** on purpose |
| Spend | `/var/lib/wly/cost.json` on the server | ~€0.03/day |

Minecraft 1.21.1, Fabric, Java 25, 17 pack entries, 87 mods loaded server-side.

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

## Where phase 1 actually stands

`wly bench` is written and tested (98.8% on `internal/bench`) but **has never
produced a number**. The open question it exists to answer:

> Does `-XX:+UseCompactObjectHeaders` help a modded Minecraft server? JEP 519
> went production in JDK 25, claims 22% less heap on SPECjbb, and Minecraft is
> the archetypal small-object workload, FerriteCore exists because of it. No
> published Minecraft numbers exist that I could find.

It ships **on** in production today because it is a supported production
feature, not because it has been measured here. That is the first row to fill.

To run the sweep:

```bash
ssh -i ~/.ssh/oracle-weloveyou ubuntu@100.103.121.9 '/opt/deploy/bench-power.sh start'
gh workflow run bench -R 6586x57890143/weloveyou.mc -f workload=both -f runs=3
# the sweep powers the box off when it finishes, always, including on failure
```

Expect ZGC to lose: it costs 10-15% CPU and there are two cores.

## Where things actually stand, 2026-08-22

The sweep has still never produced a usable table, and the reasons are now known
and fixed rather than suspected.

**The 7-hour sweep was our bug, not the hardware.** It ran 7h11m, finished 9 of
42 runs, reported ~1.0 chunks/s for everything, and saved nothing.
`Timeouts.Sample` was declared and never read, so `docker stats --no-stream`
(1-2s each) and `rcon-cli spark tps` ran for *every log line*. Each spark reply
is ten more lines, each triggering another sample, so it compounded. The same box
did 8.1-8.4 chunks/s before spark landed. Any conclusion about ARM being too slow
came from those broken numbers, so it should be re-formed after a clean run.

**The idle timer killed it.** `wly-bench-idle.timer` on the production box fires
at 23:30 UTC to stop a box left running by a crashed sweep; it stopped a healthy
one. A busy-aware replacement is installed on the bench box itself (only that box
can see whether a job is running) via `bench-admin.yml -> self-stop`. The old
production-side timer should be disabled once the new one is confirmed.

**The bench box drops off the tailnet on every reboot.** The `TS_AUTHKEY` is
ephemeral, so the node is deleted when it goes offline. Re-run
`bench-admin.yml -> tailscale-up` after each boot, or use the workflow actions
instead of SSH.

**The live server was three days stale.** It ran the old 17-entry pack (87 mods)
while v0.1.5 with 72 entries was published, because a pack release only takes
effect on restart and nothing restarts it. Restarted 2026-08-22: now
`Loading 143 mods`, healthy. This is the gap `wly serve` is meant to close.

**The client Java guard had been switched off.** `deps-check.py` was set to
`client: 25` while the Prism instance pinned no JRE, so stock installs still got
21. That is the C2ME bug class the check exists to catch. Fixed by pinning a java
component in `mmc-pack.json` (`weloveyou-pack#4`).

### Open PRs

- `weloveyou.mc#10` sampling fix, incremental writes, TPS into JSON, self-stop action
- `weloveyou-pack#4` pin Java 25 in the instance

### What the benchmark still needs

- **Simulated players.** Worldgen is throughput-bound, so TPS reads ~20 on every
  profile and says nothing. TPS and MSPT p95 are wired end to end (spark, with
  `-Dspark.backgroundProfiler=false`, otherwise it segfaults on Java 25/aarch64),
  but they only become meaningful under a workload C that puts real tick load on
  the server. This is the missing piece, not the metric.
- **A re-run before any hardware decision.** Fix is in, so a clean sweep is cheap.
  If honest numbers are still poor: bigger A1 or an x86 box first (credits expire
  15 September and a sweep costs well under EUR 1), then the local-PC pivot behind
  the Oracle VPS. If production hardware moves, the bench box has to move with it
  or the numbers describe a machine nobody uses.
- **The pack tripled.** 17 entries to 72, 143 mods loaded. Re-check `radius` and
  the profile count so a full sweep fits comfortably inside one night.

## What is next

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
