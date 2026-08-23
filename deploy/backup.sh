#!/usr/bin/env bash
# Nightly world backup. Runs on the PRODUCTION box as root (it needs docker),
# from wly-backup.timer. Installed at /opt/deploy/backup.sh; this copy is the
# reviewable one, see provision-box.sh for why that matters.
#
#   backup.sh            take a backup, prune, ship, alert on staleness
#   backup.sh --check    only check staleness and alert, take nothing
#
# Why this exists: the world lived in exactly one Docker named volume with no
# snapshot, no copy and nothing off-box. `docker compose down -v`, a stray
# `docker volume prune`, or losing the VM was total unrecoverable loss.
#
# save-off/save-all/save-on around the tar is the whole point: without it the
# archive is a torn world, which looks like a backup right up until you restore
# one. save-on runs from a trap, so an interrupted backup never leaves the
# server with saving disabled.
#
# The tar runs INSIDE the container rather than against the volume path on the
# host, so this needs no knowledge of the compose project prefix
# (`deploy_mc-data`) and no second image.
#
# Everything the pack owns is excluded: mods, libraries, cache and logs all come
# back from PACKWIZ_URL on the next start, and they are most of the bytes.
#
# BACKUP_TARGET is deliberately temporary. One rsync destination, one setting,
# not a design: today it is the desktop over the tailnet, object storage
# replaces it later and nothing else has to change. A desktop that is off means
# the off-box copy silently stops, which is exactly what the staleness alert is
# for, and the two local copies mean an offline desktop never leaves zero.
set -uo pipefail

: "${BACKUP_DIR:=/var/lib/wly/backups}"
: "${BACKUP_TARGET:=}"          # rsync destination, e.g. kon@100.x.y.z:/backups/wly
: "${BACKUP_KEEP:=2}"           # archives kept on the box
: "${BACKUP_MAX_AGE_H:=36}"     # alert when the newest archive is older
: "${MC_CONTAINER:=wly-mc}"
: "${NOTIFY:=/opt/wly/bin/notify.sh}"

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
ARCHIVE="$BACKUP_DIR/world-$STAMP.tar.gz"

# The box's ntfy stack is not in this repo. Fall back to stderr so a box without
# it still says something into the journal rather than failing silently.
say() {  # say <priority> <title> <message>
  echo "backup: [$1] $2 - $3" >&2
  [ -x "$NOTIFY" ] && "$NOTIFY" "$1" "$2" "$3" >/dev/null 2>&1
  return 0
}

rcon() { docker exec "$MC_CONTAINER" rcon-cli "$@" >/dev/null 2>&1; }

# Alert when the newest archive is too old, whatever the reason: a failed run, a
# timer that never fired, a container that was down for a week.
check_age() {
  newest=$(ls -t "$BACKUP_DIR"/world-*.tar.gz 2>/dev/null | head -1)
  if [ -z "$newest" ]; then
    say high "backup missing" "no world archive in $BACKUP_DIR at all"
    return 1
  fi
  age_h=$(( ( $(date +%s) - $(stat -c %Y "$newest") ) / 3600 ))
  if [ "$age_h" -ge "$BACKUP_MAX_AGE_H" ]; then
    say high "backup stale" "newest archive $(basename "$newest") is ${age_h}h old"
    return 1
  fi
  echo "backup: newest is $(basename "$newest"), ${age_h}h old"
}

if [ "${1:-}" = "--check" ]; then
  check_age; exit $?
fi

mkdir -p "$BACKUP_DIR"

if ! docker inspect -f '{{.State.Running}}' "$MC_CONTAINER" 2>/dev/null | grep -q true; then
  # A stopped server is not saving, so its volume is already consistent: back it
  # up anyway rather than skipping the night.
  echo "backup: $MC_CONTAINER is not running, archiving without the save dance"
else
  trap 'rcon save-on' EXIT
  rcon save-off
  rcon save-all flush || rcon save-all
  sleep 5   # save-all is asynchronous and rcon-cli does not wait for the flush
fi

if ! docker exec "$MC_CONTAINER" tar -czf - -C /data \
      --exclude=./mods --exclude=./libraries --exclude=./cache \
      --exclude=./logs --exclude=./crash-reports --exclude=./.fabric \
      --warning=no-file-changed . > "$ARCHIVE.part" 2>/dev/null; then
  # tar exits 1 for "file changed as we read it", which is routine on a live
  # server and not a reason to throw the archive away. Only a truncated stream
  # is fatal, and gzip -t is what tells the two apart.
  if ! gzip -t "$ARCHIVE.part" 2>/dev/null; then
    rm -f "$ARCHIVE.part"
    say high "backup FAILED" "tar of $MC_CONTAINER:/data produced no usable archive"
    exit 1
  fi
fi
mv "$ARCHIVE.part" "$ARCHIVE"
echo "backup: wrote $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1))"

# Prune before shipping: the box is 47G and a full disk is its own outage.
ls -t "$BACKUP_DIR"/world-*.tar.gz 2>/dev/null | tail -n +$((BACKUP_KEEP + 1)) \
  | xargs -r rm -f

if [ -n "$BACKUP_TARGET" ]; then
  if rsync -a --timeout=300 "$ARCHIVE" "$BACKUP_TARGET/"; then
    echo "backup: shipped to $BACKUP_TARGET"
  else
    # Not high: two local copies survive, and the desktop being off is the
    # expected case rather than an incident. Staleness is what escalates.
    say default "backup not shipped" "rsync to $BACKUP_TARGET failed; $BACKUP_KEEP local copies remain"
  fi
fi

check_age
