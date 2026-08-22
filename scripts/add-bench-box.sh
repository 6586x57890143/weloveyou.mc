#!/usr/bin/env bash
# Add another bench box, so a sweep can be sharded across several of them.
#
#   scripts/add-bench-box.sh weloveyou-bench-2 [ocpus] [memory-gb]
#
# One box takes about seven minutes per run, so 21 profiles across two workloads
# is most of a night. Three boxes turn that into a couple of hours. Credits
# expire on 2026-09-15 and a sweep costs well under a euro, so wall time is the
# scarce thing here, not money.
#
# What this does, in order:
#
#   1. Runs /opt/deploy/provision-box.sh on the production box, which is where
#      the OCI credential lives. That script already knows the awkward part:
#      A1 capacity is almost always refused for a NEW instance, A2 is not, and
#      converting an A2 to A1 afterwards works.
#   2. Waits for the box to join the tailnet. Provisioning installs the
#      tailscale package but does not authenticate it, so this needs
#      TS_AUTHKEY, and a REUSABLE one: an ephemeral key deletes the node the
#      moment it goes offline, and a bench box is offline most of the time.
#   3. Installs the GitHub runner with the `bench` label, which is what makes
#      the box eligible for a shard.
#   4. Quietens the box. Ubuntu's apt-daily timers are Persistent=true, so a
#      machine that is powered off except when sweeping fires the missed 06:00
#      run shortly after every boot, which is exactly when a sweep is running.
#      One of those ate four minutes of CPU on two cores mid-measurement.
#
# The box must match production, or the numbers describe a machine nobody runs.
# Keep it 2 OCPU unless production changes too: GC choices hinge on spare cores
# more than on anything else here.
set -euo pipefail

NAME="${1:?usage: add-bench-box.sh <name> [ocpus] [memory-gb]}"
OCPUS="${2:-2}"
MEM="${3:-12}"
PROD="${PROD_HOST:-ubuntu@100.103.121.9}"
KEY="${SSH_KEY:-$HOME/.ssh/oracle-weloveyou}"
REPO="${REPO:-6586x57890143/weloveyou.mc}"

: "${TS_AUTHKEY:?set TS_AUTHKEY to a REUSABLE tailscale auth key}"
command -v gh >/dev/null || { echo "::error::gh is not on PATH"; exit 1; }

ssh_prod() { ssh -i "$KEY" -o StrictHostKeyChecking=no "$PROD" "$@"; }

echo "==> provisioning $NAME ($OCPUS ocpu / ${MEM}GB)"
ssh_prod "/opt/deploy/provision-box.sh '$NAME' '$OCPUS' '$MEM'"

echo "==> waiting for $NAME on the tailnet"
ip=""
for _ in $(seq 40); do
  ip=$(ssh_prod "tailscale status 2>/dev/null | awk '\$2 == \"$NAME\" {print \$1}'" || true)
  [ -n "$ip" ] && break
  sleep 15
done
if [ -z "$ip" ]; then
  cat <<EOF
::error::$NAME never appeared on the tailnet.
Provisioning installs tailscale but does not run 'tailscale up', so a brand new
box needs it once. Reach it on its public IP if SSH is open, or add
'tailscale up --authkey ...' to the cloud-init in /opt/deploy/provision-box.sh
so future boxes join by themselves.
EOF
  exit 1
fi
echo "    $NAME is $ip"

run_on_box() { ssh -i "$KEY" -o StrictHostKeyChecking=no "ubuntu@$ip" "$@"; }

echo "==> registering a runner labelled 'bench'"
# A registration token is short-lived and single-use, so it is fetched now
# rather than stored anywhere.
token=$(gh api -X POST "repos/$REPO/actions/runners/registration-token" --jq .token)
run_on_box "bash -s" <<EOF
set -euo pipefail
mkdir -p ~/actions-runner && cd ~/actions-runner
if [ ! -f ./config.sh ]; then
  v=\$(curl -fsSL https://api.github.com/repos/actions/runner/releases/latest | grep -oP '"tag_name": "v\K[^"]+')
  curl -fsSL -o r.tar.gz "https://github.com/actions/runner/releases/download/v\$v/actions-runner-linux-arm64-\$v.tar.gz"
  tar xzf r.tar.gz && rm r.tar.gz
fi
./config.sh --unattended --replace --url "https://github.com/$REPO" \
  --token "$token" --name "$NAME" --labels self-hosted,bench --work _work
sudo ./svc.sh install ubuntu && sudo ./svc.sh start
EOF

echo "==> quietening the box"
run_on_box "sudo systemctl mask --now apt-daily.timer apt-daily-upgrade.timer \
  unattended-upgrades.service motd-news.timer update-notifier-motd.timer \
  fwupd-refresh.timer ua-timer.timer" || true

cat <<EOF

$NAME is ready.

Shard a sweep across every box carrying the 'bench' label:

  gh workflow run bench -f shards='["1/3","2/3","3/3"]' \\
    -f workload=both -f runs=1 -f radius=400

Shards only run in parallel if that many runners are online. With one box they
queue behind each other and nothing is gained.
EOF
