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
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
