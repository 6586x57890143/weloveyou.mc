#!/usr/bin/env bash
# Provision an Oracle box. Lives on the production box at
# /opt/deploy/provision-box.sh, which is where the OCI credential is; this copy
# exists so the thing is reviewable. It was untracked for a long time and picked
# up two bugs that a diff would have caught:
#
#   - the conversion check broke on RUNNING without ever checking the SHAPE, so
#     a box that stayed A2 was reported as a success. A2 is AmpereOne with two
#     vCPUs per OCPU against A1's one, so an unconverted box does not measure
#     slightly fast, it measures a different machine and mixes those numbers
#     into the same table.
#   - cloud-init installed tailscale and never ran `tailscale up`, so every new
#     box came up unreachable: no public SSH by convention, not on the tailnet,
#     and no runner yet to reach it through.
#
# Copy back with:
#   scp deploy/provision-box.sh ubuntu@100.103.121.9:/tmp/ && #     ssh ubuntu@100.103.121.9 'sudo install -m755 /tmp/provision-box.sh /opt/deploy/'
#
# Needs TS_PROVISION_KEY set to a ONE-OFF, NON-EPHEMERAL tailscale key. One-off
# and ephemeral are independent: one-off limits how many devices the key can
# add, ephemeral deletes the node when it goes offline. A bench box is offline
# most of the time, so ephemeral is wrong here.
# Provision a box identical to the production one, in one command.
#
#   provision-box.sh <display-name> [ocpus] [memory-gb]
#
# The trick this encodes, learned the hard way: A1 is almost always "Out of host
# capacity" for a NEW instance in Frankfurt - all three ADs refused - but A2 has
# capacity, and CONVERTING an existing A2 to A1 succeeds. So launch A2, then
# convert. The conversion is asynchronous and the instance reports the old shape
# and "currently being modified" for a minute or two; that is not a failure.
#
# Why A1 and not just keep A2: A1 is Neoverse-N1 with 1 vCPU per OCPU, A2 is
# AmpereOne with 2. A bench box on different silicon with twice the threads
# measures a server we do not ship, and GC choices in particular hinge on
# spare cores.
set -euo pipefail
export PATH="/home/ubuntu/bin:$PATH"
export OCI_CLI_SUPPRESS_FILE_PERMISSIONS_WARNING=True

NAME="${1:?usage: provision-box.sh <display-name> [ocpus] [memory-gb]}"
OCPUS="${2:-2}"
MEM="${3:-12}"

T=$(grep -m1 '^tenancy' /home/ubuntu/.oci/config | cut -d= -f2 | tr -d ' ')
SUBNET=$(oci network subnet list -c "$T" --all --output json 2>/dev/null | python3 -c "
import json,sys
for s in json.load(sys.stdin)['data']:
    if not s.get('prohibit-public-ip-on-vnic'): print(s['id']); break")
IMAGE=$(oci compute image list -c "$T" --operating-system "Canonical Ubuntu" \
        --shape VM.Standard.A1.Flex --output json 2>/dev/null | python3 -c "
import json,sys; print(json.load(sys.stdin)['data'][0]['id'])")

cat > /tmp/ci-$NAME.yaml <<'CI'
#cloud-config
package_update: true
packages: [ca-certificates, curl, git, jq]
runcmd:
  - install -m 0755 -d /etc/apt/keyrings
  - curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  - chmod a+r /etc/apt/keyrings/docker.asc
  - echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" > /etc/apt/sources.list.d/docker.list
  - curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg -o /usr/share/keyrings/tailscale-archive-keyring.gpg
  - curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.tailscale-keyring.list -o /etc/apt/sources.list.d/tailscale.list
  - apt-get update -qq
  - apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin tailscale
  - usermod -aG docker ubuntu
  - systemctl enable --now docker
  # Join the tailnet at first boot. Installing the package without doing this
  # left every new box unreachable: no public SSH by default, not on the
  # tailnet, so the only way in was a workflow on its own runner, which does
  # not exist yet on a box that has just been created.
  #
  # TS_PROVISION_KEY must be ONE-OFF and NON-EPHEMERAL. Those are independent
  # properties: one-off limits how many devices the key can add, ephemeral
  # deletes the NODE when it goes offline. A bench box is offline most of the
  # time, so ephemeral is wrong here and is why the first box kept vanishing
  # from the tailnet. A reusable key is not the answer either: the box
  # authenticates once in its life, so a spent one-off key is strictly safer.
  #
  # Not --ssh: tailscale SSH takes over port 22 and enforces the tailnet ACL,
  # which refuses tagged nodes by default. Plain sshd already has the key.
  - test -n "__TS_KEY__" && tailscale up --authkey "__TS_KEY__" --hostname __BOX_NAME__ --advertise-tags=tag:ci --ssh=false --accept-dns=false || echo "no key given, box will not be on the tailnet"
CI

# The heredoc above is quoted so nothing in it expands, which is what keeps the
# docker keyring lines intact. Substitute afterwards instead.
#
# The key ends up in the instance's user-data, which is readable by anyone who
# can reach the instance metadata. That is acceptable only because it is a
# one-off key: it is spent the moment this box uses it. Never put a reusable
# key here.
sed -i "s|__TS_KEY__|${TS_PROVISION_KEY:-}|g; s|__BOX_NAME__|$NAME|g" "/tmp/ci-$NAME.yaml"
if [ -z "${TS_PROVISION_KEY:-}" ]; then
  echo "warning: TS_PROVISION_KEY not set, $NAME will not join the tailnet"
  echo "         generate a ONE-OFF, NON-EPHEMERAL key and re-run to get one that does"
fi

echo "launching $NAME ($OCPUS ocpu / ${MEM}GB), trying A1 first then A2 ..."
ID=""
for SHAPE in VM.Standard.A1.Flex VM.Standard.A2.Flex; do
  for AD in $(oci iam availability-domain list -c "$T" --output json 2>/dev/null | python3 -c "
import json,sys
print(' '.join(a['name'] for a in json.load(sys.stdin)['data']))"); do
    printf '  %-22s %s ... ' "$SHAPE" "${AD##*-}"
    out=$(oci compute instance launch -c "$T" --availability-domain "$AD" \
      --display-name "$NAME" --shape "$SHAPE" \
      --shape-config "{\"ocpus\":$OCPUS,\"memoryInGBs\":$MEM}" \
      --image-id "$IMAGE" --subnet-id "$SUBNET" --assign-public-ip true \
      --ssh-authorized-keys-file /home/ubuntu/.ssh/authorized_keys \
      --user-data-file "/tmp/ci-$NAME.yaml" --output json 2>&1) || true
    if grep -q '"lifecycle-state"' <<<"$out"; then
      ID=$(python3 -c "
import json,sys,re
m=re.search(r'\"id\": \"(ocid1\.instance[^\"]+)\"', '''$out''')
print(m.group(1) if m else '')")
      echo "launched on $SHAPE"; break 2
    fi
    grep -oE '"message": "[^"]+"' <<<"$out" | head -1 | cut -c13-60 || echo failed
  done
done
[ -n "$ID" ] || { echo "no capacity on any shape or AD"; exit 1; }

SHAPE_NOW=$(oci compute instance get --instance-id "$ID" --output json | python3 -c "
import json,sys;print(json.load(sys.stdin)['data']['shape'])")

if [ "$SHAPE_NOW" != "VM.Standard.A1.Flex" ]; then
  echo "converting $SHAPE_NOW -> VM.Standard.A1.Flex (shape changes need a stopped instance) ..."
  oci compute instance action --instance-id "$ID" --action SOFTSTOP --wait-for-state STOPPED >/dev/null 2>&1
  oci compute instance update --instance-id "$ID" --shape VM.Standard.A1.Flex \
    --shape-config "{\"ocpus\":$OCPUS,\"memoryInGBs\":$MEM}" --force >/dev/null 2>&1
  # The update is asynchronous: START returns "currently being modified" until
  # it lands. Retry rather than treat that as an error.
  for _ in $(seq 40); do
    oci compute instance action --instance-id "$ID" --action START >/dev/null 2>&1 && break
    sleep 15
  done
fi

for _ in $(seq 40); do
  line=$(oci compute instance get --instance-id "$ID" --output json 2>/dev/null | python3 -c "
import json,sys; d=json.load(sys.stdin)['data']; sc=d.get('shape-config') or {}
print(f\"{d['shape']} {sc.get('ocpus')}ocpu {sc.get('memory-in-gbs')}GB {d['lifecycle-state']}\")")
  # Must be RUNNING *and* A1. Breaking on RUNNING alone reported a box that
  # never converted as a success, and an A2 has two vCPUs per OCPU against A1's
  # one, so it benchmarks a machine with twice the threads of the one we ship.
  [[ "$line" == *RUNNING* ]] && { echo "  $line"; break; }
  sleep 15
done
oci compute instance list-vnics --instance-id "$ID" --output json 2>/dev/null | python3 -c "
import json,sys
for v in json.load(sys.stdin)['data']: print('  public ip:', v.get('public-ip'))"
echo "  id: $ID"

# Refuse to hand back a box on the wrong silicon. GC behaviour hinges on spare
# cores more than on anything else measured here, so an unconverted A2 does not
# produce slightly optimistic numbers, it produces numbers for a different
# machine, mixed into the same table as the real ones.
FINAL=$(oci compute instance get --instance-id "$ID" --output json 2>/dev/null | python3 -c "
import json,sys;print(json.load(sys.stdin)['data']['shape'])")
if [ "$FINAL" != "VM.Standard.A1.Flex" ]; then
  echo "::error::$NAME is $FINAL, not VM.Standard.A1.Flex."
  echo "  The conversion is asynchronous and may simply need longer. Retry with:"
  echo "    oci compute instance action --instance-id $ID --action SOFTSTOP --wait-for-state STOPPED"
  echo "    oci compute instance update --instance-id $ID --shape VM.Standard.A1.Flex \\"
  echo "      --shape-config '{\"ocpus\":$OCPUS,\"memoryInGBs\":$MEM}' --force"
  echo "    oci compute instance action --instance-id $ID --action START"
  exit 1
fi
echo "  shape verified: $FINAL"
