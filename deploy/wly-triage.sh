#!/usr/bin/env bash
# Runs as `agent`. Claude reads the root-collected snapshot (Read tool only, no shell)
# and returns a verdict against the charter in prompts/base.md.
# Pushes only at MEDIUM and above; everything else is logged to journald.
set -uo pipefail
set -a; . /opt/wly/etc/env; set +a     # export, so `claude` inherits CLAUDE_CODE_OAUTH_TOKEN
MODE="${1:-quick}"
SNAP="/var/lib/wly/${MODE}.json"
LAST="/var/lib/wly/verdicts/last-${MODE}.json"
export HOME="${HOME:-/home/agent}"
export PATH="$HOME/.local/bin:$PATH"

# No auth yet -> stay quiet rather than alert-storm every hour.
if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && [ ! -s "$HOME/.claude/.credentials.json" ]; then
  echo "claude not authenticated; skipping"; exit 0
fi
[ -r "$SNAP" ] || { echo "no snapshot at $SNAP"; exit 1; }

# Only wake the model when something actually looks wrong. This ran
# unconditionally every hour, and almost every run read a healthy snapshot,
# paid for a model call and produced nothing: the push floor is MEDIUM, so
# those tokens bought silence. gate.sh is plain thresholds on fields the
# collector already writes.
#   0 = worth looking at   1 = quiet   2 = the snapshot itself is broken
# quick only. The daily security sweep (`full`) always reasons, even on a
# box that looks fine: judging CVEs, lynis findings and SSH patterns is the
# job, not a reaction to something already being broken. Gating that would
# have quietly turned the security audit off.
gated=0
if [ "$MODE" = "quick" ]; then
  /opt/wly/bin/gate.sh "$SNAP"; gated=$?
fi
if [ "$gated" -eq 1 ]; then
  exit 0
elif [ "$gated" -eq 2 ]; then
  # A broken snapshot is a real problem, but not one a model can help with.
  printf '%s
' "the health snapshot at $SNAP is missing or unreadable"     | /opt/wly/bin/notify.sh "$TOPIC_ALERTS" "wly: health snapshot unreadable" default warning
  exit 1
fi

# Charter principle 5: let the agent see its own last verdict so it can suppress repeats.
prior="(no previous verdict on record)"
[ -s "$LAST" ] && prior=$(cat "$LAST")

verdict=$(claude -p "$(cat /opt/wly/prompts/base.md /opt/wly/prompts/${MODE}.md)

## Previous verdict (from the last run of this same check)

$prior

## Now

Read $SNAP and produce your verdict." \
  --allowed-tools "Read" \
  --output-format json 2>/dev/null \
  | jq -r '.result // empty' \
  | sed 's/^```json//; s/^```//; s/```$//' \
  | sed -n '/^{/,$p')

[ -z "$verdict" ] && { echo "claude returned nothing"; exit 1; }

sev=$(jq -r '.severity // "UNPARSEABLE"' <<<"$verdict" 2>/dev/null || echo UNPARSEABLE)
conf=$(jq -r '.confidence // "unknown"' <<<"$verdict" 2>/dev/null || echo unknown)
title=$(jq -r '.title // "wly triage"' <<<"$verdict" 2>/dev/null || echo "wly triage")
body=$(jq -r '.body // .' <<<"$verdict" 2>/dev/null || echo "$verdict")

# Keep the verdict for the next run's repeat-suppression, and for the inbox `explain` op.
printf '%s\n' "$verdict" > "$LAST" 2>/dev/null

echo "[$MODE] severity=$sev confidence=$conf title=$title"

# Push floor is MEDIUM. OK/INFO/LOW stay in journald and last-${MODE}.json.
case "${sev^^}" in
  OK|INFO|LOW) exit 0 ;;
  MEDIUM)      prio=default; tags="mag" ;;
  HIGH)        prio=high;    tags="warning" ;;
  CRITICAL)    prio=urgent;  tags="rotating_light" ;;
  *)           prio=default; tags="grey_question"; title="wly: unparseable verdict" ;;
esac
printf '%s\n' "$body" | /opt/wly/bin/notify.sh "$TOPIC_ALERTS" "$title" "$prio" "$tags"
