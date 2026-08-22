#!/usr/bin/env python3
"""Fail if a self-hosted runner is reachable from a fork.

Both repositories are public and this one has a self-hosted bench runner. That
combination is safe only while no workflow a fork can trigger targets it: a
pull_request trigger on a self-hosted job hands anyone who opens a PR code
execution on that machine.

Parses the workflow rather than grepping for "self-hosted". The grep version
failed on its own error message - ci.yml mentions the string in this check's
documentation and triggers on pull_request, so it flagged itself. A check that
cannot describe what it forbids is not much of a check.
"""

from __future__ import annotations

import pathlib
import sys
from pathlib import Path

import yaml

FORK_TRIGGERS = {"pull_request", "pull_request_target"}
ROOT = Path(__file__).resolve().parent.parent


def runs_on_self_hosted(runs_on) -> bool:
    if isinstance(runs_on, str):
        return runs_on == "self-hosted"
    if isinstance(runs_on, list):
        return "self-hosted" in runs_on
    if isinstance(runs_on, dict):          # runs-on: {group:, labels:}
        return "self-hosted" in (runs_on.get("labels") or [])
    return False


def triggers(doc: dict) -> set[str]:
    # PyYAML parses a bare `on:` key as the boolean True.
    on = doc.get("on", doc.get(True))
    if isinstance(on, str):
        return {on}
    if isinstance(on, list):
        return set(on)
    if isinstance(on, dict):
        return set(on)
    return set()


def main() -> int:
    failed = False
    for path in sorted((ROOT / ".github" / "workflows").glob("*.yml")):
        doc = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        hosted = [name for name, job in (doc.get("jobs") or {}).items()
                  if runs_on_self_hosted(job.get("runs-on"))]
        if not hosted:
            continue
        bad = triggers(doc) & FORK_TRIGGERS
        if bad:
            print(f"::error::{path.name}: job(s) {', '.join(hosted)} run on a "
                  f"self-hosted runner and the workflow triggers on "
                  f"{', '.join(sorted(bad))}. On a public repository that is "
                  f"arbitrary code execution from any fork.")
            failed = True
        else:
            print(f"  ok: {path.name} -> {', '.join(hosted)} "
                  f"(triggers: {', '.join(sorted(triggers(doc)))})")
    if check_shard_is_wired() != 0:
        failed = True
    return 1 if failed else 0


def check_shard_is_wired():
    """A sharded matrix that never passes --shard runs the whole sweep N times.

    That is exactly what happened: the matrix was correct, the job names said
    1/3, 2/3 and 3/3, and all three boxes worked through the same 21 profiles in
    lockstep for hours. Nothing failed, nothing warned, and the only symptom was
    three machines running an identically named container.

    The two halves live in different places and neither is meaningful alone, so
    check they agree.
    """
    path = pathlib.Path(".github/workflows/bench.yml")
    if not path.exists():
        return 0
    text = path.read_text(encoding="utf-8")
    has_matrix = "matrix.shard" in text or "shard:" in text
    passes_flag = "--shard" in text
    if has_matrix and not passes_flag:
        print("::error::bench.yml has a shard matrix but never passes --shard, "
              "so every shard would run the entire sweep")
        return 1
    if passes_flag and "--raw" not in text:
        print("::error::bench.yml passes --shard but not --raw, so the shards "
              "produce nothing for the merge job to combine")
        return 1
    if has_matrix:
        print("  ok: bench.yml -> shard matrix is wired to --shard and --raw")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
