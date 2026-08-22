#!/usr/bin/env bash
# Decide whether a health snapshot is worth waking the model for.
#
# Runs on the PRODUCTION box, before triage.sh. Exit 0 means "something looks
# wrong, go ask Claude"; exit 1 means "quiet, stop here".
#
# Why this exists. wly-health fired hourly and called `claude -p` every single
# time, whether or not anything had happened. The overwhelming majority of those
# runs read a snapshot showing 6% disk, no failed units and a healthy container,
# and paid for a model call to be told so. The push floor is already MEDIUM, so
# almost none of them even produced a notification: the tokens bought silence.
#
# Everything below is a plain threshold on a field the collector already writes.
# The model is for judgement about things that ARE odd - correlating a restart
# with a deploy, deciding whether a new listening port matters. It is not for
# confirming that a quiet box is quiet.
#
# Fail-quiet, not fail-loud: a missing or unparseable snapshot is reported
# directly through ntfy rather than by asking a model to look at a file that is
# not there.
set -uo pipefail

SNAP="${1:-/var/lib/wly/quick.json}"
: "${DISK_PCT:=85}"     # any mount at or above this
: "${MEM_PCT:=90}"
: "${LOAD1:=4.0}"       # two cores; sustained 4 is a queue, not a spike

say() { echo "gate: $*"; }

if [ ! -s "$SNAP" ]; then
  say "no snapshot at $SNAP"
  exit 2                # caller notifies; do not spend a model call on this
fi
if ! jq -e . "$SNAP" >/dev/null 2>&1; then
  say "snapshot at $SNAP is not valid JSON"
  exit 2
fi

reasons=()
add() { reasons+=("$1"); }

# --- things the collector reports as plain text, empty when healthy ---
for f in .host_health.failed_units \
         .containers.restarting .containers.risk_flags \
         .network.new_ports_vs_baseline; do
  v=$(jq -r "$f // empty" "$SNAP" 2>/dev/null | tr -d '[:space:]')
  [ -n "$v" ] && add "${f#.} is not empty"
done

# Journal errors, minus the noise a public SSH port generates continuously.
# sshd logs a handshake error every time an internet scanner touches port 22.
# That is constant, means nothing, and on its own was enough to wake the model
# on every single run. Anything that is NOT one of these still counts.
#
# Deliberately NOT filtered: authentication failures. A sustained brute force
# should reach a human, and the daily security sweep is too slow for that.
noise='kex_exchange_identification|Connection (reset|closed) by|banner exchange|Unable to negotiate'
jerr=$(jq -r '.host_health.journal_errors // ""' "$SNAP" | grep -vE "$noise" | grep -c '[^[:space:]]' || true)
[ "${jerr:-0}" -gt 0 ] && add "$jerr journal error(s) that are not scanner noise"

# --- security findings: strings or arrays, non-empty either way ---
for f in $(jq -r '.security | keys[]?' "$SNAP" 2>/dev/null); do
  v=$(jq -r --arg k "$f" '.security[$k] | if type=="array" then (length|tostring) elif type=="string" then (.|gsub("\\s";"")) else (.|tostring) end' "$SNAP" 2>/dev/null)
  case "$v" in ""|"0"|"null"|"{}"|"[]") ;; *) add "security.$f has findings" ;; esac
done

# --- reboots and security updates ---
[ "$(jq -r '.host_health.reboot_required // "no"' "$SNAP")" != "no" ] && add "a reboot is required"
sec=$(jq -r '.host_health.pending_updates // ""' "$SNAP" | grep -oE '[0-9]+ security' | grep -oE '^[0-9]+')
[ -n "${sec:-}" ] && [ "$sec" -gt 0 ] && add "$sec pending security update(s)"

# --- capacity: disk, memory, swap, load ---
while read -r pct; do
  [ -n "$pct" ] && [ "$pct" -ge "$DISK_PCT" ] && add "a filesystem is ${pct}% full"
done < <(jq -r '.host_health.disk // ""' "$SNAP" | grep -oE '[0-9]+%' | tr -d '%')

mem=$(jq -r '.host_health.memory // ""' "$SNAP" | grep -oE '\([0-9]+%\)' | tr -d '()%')
[ -n "${mem:-}" ] && [ "$mem" -ge "$MEM_PCT" ] && add "memory at ${mem}%"

swap=$(jq -r '.host_health.swap // ""' "$SNAP" | grep -oE '^[0-9]+')
[ -n "${swap:-}" ] && [ "$swap" -gt 0 ] && add "swap in use (${swap}M)"

load=$(jq -r '.host_health.loadavg // ""' "$SNAP" | awk '{print $1}')
[ -n "${load:-}" ] && awk -v l="$load" -v t="$LOAD1" 'BEGIN{exit !(l+0 >= t+0)}' && add "load average $load"

# --- containers: anything not Up, or Up but not healthy when it has a check ---
while IFS= read -r line; do
  [ -z "$line" ] && continue
  status=$(cut -d'|' -f3 <<<"$line")
  name=$(cut -d'|' -f1 <<<"$line")
  case "$status" in
    Up*unhealthy*) add "container $name is unhealthy" ;;
    Up*)           ;;
    *)             add "container $name is '$status'" ;;
  esac
done < <(jq -r '.containers.list // ""' "$SNAP")

# --- anyone reaching the box from outside the tailnet ---
out=$(jq -r '.deploy.ssh_from_outside_tailnet // ""' "$SNAP")
case "$out" in ""|"(none"*) ;; *) add "ssh from outside the tailnet" ;; esac

if [ ${#reasons[@]} -eq 0 ]; then
  say "nothing to report; not calling the model"
  exit 1
fi
say "${#reasons[@]} reason(s) to look:"
printf 'gate:   - %s\n' "${reasons[@]}"
exit 0
