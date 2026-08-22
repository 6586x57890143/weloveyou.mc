#!/usr/bin/env bash
# Stop any bench box that OCI reports RUNNING while no sweep is running.
#
# Runs on the PRODUCTION box, because that is where the OCI credential lives.
#
# Why this exists. The bench boxes power themselves off in-guest, at the end of
# every sweep and on an idle timer, and that was believed to be enough because
# an in-guest poweroff normally transitions the OCI instance to STOPPED, which
# is the state billing keys off. Observed 2026-08-22: a box whose own
# `shutdown -h +1` was scheduled at 15:25:34 still read RUNNING at 15:36:49. The
# guest had halted and the instance had not. Nobody would have noticed until the
# credits ran out.
#
# How it decides. Not by asking the bench box anything. The production box has
# no private key, and the bench boxes are not on the tailnet - they join with an
# EPHEMERAL auth key, so the tailnet forgets them every time they power off. The
# first draft of this SSHed in to check for a running job, which would have
# failed every single time and stopped boxes mid-sweep to save pennies.
#
# Instead it asks GitHub, anonymously, whether a bench run is in flight. The
# repository is public, so this needs no token, and one call every ten minutes
# is nowhere near the 60/hour anonymous limit. A sweep in progress is an
# in_progress or queued run by definition, so this cannot reap a live sweep.
#
#   --dry-run   report and change nothing
#   touch /var/lib/wly/bench-reaper.hold   pause reaping entirely, for debugging
set -uo pipefail
export PATH="/home/ubuntu/bin:$PATH"
export OCI_CLI_SUPPRESS_FILE_PERMISSIONS_WARNING=True

REPO="${BENCH_REPO:-6586x57890143/weloveyou.mc}"
BOXES="${BENCH_BOXES:-weloveyou-bench weloveyou-bench-2 weloveyou-bench-3}"
HOLD="${BENCH_HOLD:-/var/lib/wly/bench-reaper.hold}"
DRY=""
[ "${1:-}" = "--dry-run" ] && DRY=1

if [ -f "$HOLD" ]; then
  echo "hold file present at $HOLD, not reaping anything"
  exit 0
fi

# Anything in flight, for any workflow that runs on the bench boxes. Erring
# toward "busy" on a failed or unparseable call is deliberate: leaving a box up
# for another ten minutes costs pennies, stopping one mid-sweep costs the sweep.
active=$(curl -s -m 20 \
  "https://api.github.com/repos/$REPO/actions/runs?per_page=40" \
  | python3 -c "
import json,sys
try:
    runs = json.load(sys.stdin).get('workflow_runs')
except Exception:
    print('unknown'); raise SystemExit
if runs is None:
    print('unknown'); raise SystemExit
live = [r for r in runs
        if r.get('status') in ('in_progress', 'queued', 'waiting')
        and r.get('name') in ('bench', 'bench-admin')]
print(len(live))
" 2>/dev/null)

if [ "$active" = "unknown" ] || [ -z "$active" ]; then
  echo "could not ask GitHub what is running; leaving every box alone"
  exit 0
fi
if [ "$active" != "0" ]; then
  echo "$active bench run(s) in flight, leaving every box alone"
  exit 0
fi

T=$(grep -m1 '^tenancy' /home/ubuntu/.oci/config | cut -d= -f2 | tr -d ' ')
state_of() {
  oci compute instance get --instance-id "$1" --output json 2>/dev/null \
    | python3 -c "import json,sys;print(json.load(sys.stdin)['data']['lifecycle-state'])"
}

for name in $BOXES; do
  id=$(oci compute instance list -c "$T" --all --output json 2>/dev/null | python3 -c "
import json,sys
for i in json.load(sys.stdin)['data']:
    if i['display-name']=='$name' and i['lifecycle-state']!='TERMINATED':
        print(i['id']); break")
  [ -n "$id" ] || { echo "$name: no such instance"; continue; }
  state=$(state_of "$id")
  if [ "$state" != "RUNNING" ]; then
    echo "$name: $state"
    continue
  fi
  if [ -n "$DRY" ]; then
    echo "$name: RUNNING with nothing to do, would stop it"
    continue
  fi
  echo "$name: RUNNING with nothing to do, stopping"
  oci compute instance action --instance-id "$id" --action SOFTSTOP \
    --wait-for-state STOPPED >/dev/null 2>&1 \
    || oci compute instance action --instance-id "$id" --action STOP \
       --wait-for-state STOPPED >/dev/null 2>&1
  echo "  $(state_of "$id")"
done
