#!/usr/bin/env bash
# Stop any bench box that is RUNNING while nothing is sweeping.
#
# Runs on the PRODUCTION box, because that is where the OCI credential lives.
#
# The bench boxes already power themselves off in-guest, both at the end of
# every sweep (bench.yml) and on an idle timer. That was believed to be enough
# because an in-guest poweroff normally transitions the OCI instance to STOPPED,
# which is the state billing keys off. Observed 2026-08-22: a box whose own
# `shutdown -h +1` was scheduled at 15:25:34 still read RUNNING at 15:36:49, so
# the guest had halted and the instance had not. That is eleven minutes of
# nobody noticing, on a box that bills by the hour and whose credits expire.
#
# So this is the belt to the guest's braces: it asks OCI directly, and never
# trusts the guest to have stopped itself.
#
# A box is busy if it answers SSH and reports a runner job or a bench container.
# A box that does NOT answer is treated as idle, which is the whole point: a
# halted guest is exactly the case that leaves the instance billing.
set -uo pipefail
export PATH="/home/ubuntu/bin:$PATH"
export OCI_CLI_SUPPRESS_FILE_PERMISSIONS_WARNING=True

BOXES="${BENCH_BOXES:-weloveyou-bench weloveyou-bench-2 weloveyou-bench-3}"
KEY="${BENCH_KEY:-/home/ubuntu/.ssh/id_ed25519}"
T=$(grep -m1 '^tenancy' /home/ubuntu/.oci/config | cut -d= -f2 | tr -d ' ')

busy() { # $1 = host. Answers yes only on a clear signal that work is happening.
  ssh -i "$KEY" -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=8 \
    "ubuntu@$1" \
    "pgrep -f Runner.Worker >/dev/null || [ -n \"\$(docker ps -q --filter name=bench- 2>/dev/null)\" ]" \
    >/dev/null 2>&1
}

for name in $BOXES; do
  id=$(oci compute instance list -c "$T" --all --output json 2>/dev/null | python3 -c "
import json,sys
for i in json.load(sys.stdin)['data']:
    if i['display-name']=='$name' and i['lifecycle-state']!='TERMINATED':
        print(i['id']); break")
  [ -n "$id" ] || { echo "$name: no such instance"; continue; }
  state=$(oci compute instance get --instance-id "$id" --output json 2>/dev/null \
    | python3 -c "import json,sys;print(json.load(sys.stdin)['data']['lifecycle-state'])")
  if [ "$state" != "RUNNING" ]; then
    echo "$name: $state, nothing to do"
    continue
  fi
  if busy "$name"; then
    echo "$name: RUNNING and busy, leaving it up"
    continue
  fi
  echo "$name: RUNNING and idle, stopping"
  oci compute instance action --instance-id "$id" --action SOFTSTOP \
    --wait-for-state STOPPED >/dev/null 2>&1 \
    || oci compute instance action --instance-id "$id" --action STOP \
       --wait-for-state STOPPED >/dev/null 2>&1
  echo "  $(oci compute instance get --instance-id "$id" --output json 2>/dev/null \
    | python3 -c "import json,sys;print(json.load(sys.stdin)['data']['lifecycle-state'])")"
done
