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
: "${DB_CONTAINER:=wly-db}"     # the link database, absent until the bot ships
: "${DB_USER:=wly}"
: "${DB_NAME:=wly}"
: "${DB_PASSWORD:=}"            # unused with the image's local-socket trust
: "${NOTIFY:=/opt/wly/bin/notify.sh}"

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
ARCHIVE="$BACKUP_DIR/world-$STAMP.tar.gz"
DB_ARCHIVE="$BACKUP_DIR/db-$STAMP.sql.gz"

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

  # The database, once there is one. A fresh world archive beside a dump that
  # silently stopped is the gap this exists to catch: --check would otherwise
  # report a healthy backup while player links rot.
  docker inspect -f '{{.State.Running}}' "$DB_CONTAINER" 2>/dev/null | grep -q true || return 0
  newest_db=$(ls -t "$BACKUP_DIR"/db-*.sql.gz 2>/dev/null | head -1)
  if [ -z "$newest_db" ]; then
    say high "db backup missing" "$DB_CONTAINER is running but there is no dump in $BACKUP_DIR"
    return 1
  fi
  db_age_h=$(( ( $(date +%s) - $(stat -c %Y "$newest_db") ) / 3600 ))
  if [ "$db_age_h" -ge "$BACKUP_MAX_AGE_H" ]; then
    say high "db backup stale" "newest dump $(basename "$newest_db") is ${db_age_h}h old"
    return 1
  fi
  echo "backup: newest dump is $(basename "$newest_db"), ${db_age_h}h old"
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

# The link database, dumped beside the world rather than into it. It lives in
# its own volume, and a Postgres data directory copied file by file under a
# running server is a torn snapshot in exactly the way the save-off dance above
# exists to prevent. pg_dump takes its own consistent snapshot, so it needs no
# coordination, no downtime and no save dance of its own.
#
# This is not a nice-to-have. The world tar cannot rebuild it, and losing it
# means every player has to re-link their Discord account by hand.
#
# --clean --if-exists makes the dump self-sufficient: it drops before it creates,
# so it restores over a volume that has already run schema.sql from initdb. That
# is not a corner case, it is the normal one, because a fresh volume ALWAYS runs
# schema.sql. Without these flags the only safe restore is into a database that
# has never seen it, which is a footgun at exactly the hour you would find out.
#
# No credential in this script: pg_dump runs INSIDE the container over the local
# socket, which the postgres image trusts. PGPASSWORD is passed anyway and is
# harmless when empty, so a box that tightens pg_hba later does not silently
# start failing every night.
if docker inspect -f '{{.State.Running}}' "$DB_CONTAINER" 2>/dev/null | grep -q true; then
  if docker exec -e PGPASSWORD="$DB_PASSWORD" "$DB_CONTAINER" \
       pg_dump --clean --if-exists -U "$DB_USER" -d "$DB_NAME" | gzip > "$DB_ARCHIVE.part" \
     && gzip -t "$DB_ARCHIVE.part" 2>/dev/null; then
    mv "$DB_ARCHIVE.part" "$DB_ARCHIVE"
    echo "backup: wrote $DB_ARCHIVE ($(du -h "$DB_ARCHIVE" | cut -f1))"
  else
    # High, unlike a failed rsync: there is no second copy of this anywhere.
    rm -f "$DB_ARCHIVE.part"
    say high "db backup FAILED" "pg_dump of $DB_CONTAINER produced no usable dump"
  fi
else
  # Not an error while the bot has not shipped, the service simply does not
  # exist yet. It becomes one the moment there is a database to lose, and the
  # branch above is what says so.
  echo "backup: $DB_CONTAINER is not running, no database to dump"
fi

# Prune before shipping: the box is 47G and a full disk is its own outage.
ls -t "$BACKUP_DIR"/world-*.tar.gz 2>/dev/null | tail -n +$((BACKUP_KEEP + 1)) \
  | xargs -r rm -f

ls -t "$BACKUP_DIR"/db-*.sql.gz 2>/dev/null | tail -n +$((BACKUP_KEEP + 1)) | xargs -r rm -f

if [ -n "$BACKUP_TARGET" ]; then
  ship=("$ARCHIVE")
  [ -f "$DB_ARCHIVE" ] && ship+=("$DB_ARCHIVE")
  if rsync -a --timeout=300 "${ship[@]}" "$BACKUP_TARGET/"; then
    echo "backup: shipped to $BACKUP_TARGET"
  else
    # Not high: two local copies survive, and the desktop being off is the
    # expected case rather than an incident. Staleness is what escalates.
    say default "backup not shipped" "rsync to $BACKUP_TARGET failed; $BACKUP_KEEP local copies remain"
  fi
fi

check_age
