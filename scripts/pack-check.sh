#!/usr/bin/env bash
# Validate every packwiz channel under pack/.
#
#   scripts/pack-check.sh          # structure + reachability. Runs on every PR.
#   scripts/pack-check.sh --full   # also downloads every file and verifies its
#                                  # hash. Slow (hundreds of MB); release only.
#
# The check that matters is `side`. An entry without one defaults to both,
# which quietly ships Sodium to the server and costs real TPS. packwiz fills it
# in from Modrinth metadata, but a hand-written entry or a `packwiz url` add
# will not, and nothing else in the toolchain complains.
set -uo pipefail

cd "$(dirname "$0")/.."
full=false
[ "${1:-}" = "--full" ] && full=true

command -v packwiz >/dev/null || { echo "::error::packwiz is not on PATH"; exit 1; }

fail=0
err() { echo "::error::$1"; fail=1; }

shopt -s nullglob
packs=(pack/*/pack.toml)
[ ${#packs[@]} -eq 0 ] && { echo "::error::no packs found under pack/"; exit 1; }

for packfile in "${packs[@]}"; do
  dir=$(dirname "$packfile")
  channel=$(basename "$dir")
  echo "=== $channel"

  # 1. A stale index ships a pack that cannot resolve. `refresh` is idempotent,
  #    so any diff it produces means someone edited metadata without refreshing.
  (cd "$dir" && packwiz refresh >/dev/null 2>&1) || err "$channel: packwiz refresh failed"
  # --porcelain, not `git diff`: an untracked metafile is just as much a stale
  # index as a modified one, and `git diff` does not see untracked files.
  if [ -n "$(git status --porcelain -- "$dir")" ]; then
    git status --short -- "$dir"
    err "$channel: index is stale — run 'packwiz refresh' in $dir and commit the result"
  fi

  # 2. Every entry declares an explicit, valid side.
  entries=0
  while IFS= read -r meta; do
    entries=$((entries + 1))
    side=$(grep -m1 -oE '^side *= *"[^"]+"' "$meta" | grep -oE '"[^"]+"' | tr -d '"')
    case "$side" in
      client|server|both) ;;
      "") err "$channel: ${meta#$dir/} declares no side — it would default to both" ;;
      *)  err "$channel: ${meta#$dir/} has side=\"$side\"; expected client, server or both" ;;
    esac
  done < <(find "$dir" -name '*.pw.toml' | sort)
  [ "$entries" -eq 0 ] && err "$channel: no mod entries found"
  echo "  $entries entries, sides all explicit"

  # 3. Downloads. HEAD by default so a PR stays fast; --full fetches and hashes.
  while IFS=$'\t' read -r meta url hashfmt want; do
    [ -z "$url" ] && continue
    if $full; then
      got=$(curl -sSLf "$url" | case "$hashfmt" in
              sha512) sha512sum ;;
              sha256) sha256sum ;;
              sha1)   sha1sum ;;
              *)      echo "UNSUPPORTED-$hashfmt" ;;
            esac | cut -d' ' -f1)
      [ "$got" = "$want" ] || err "$channel: ${meta#$dir/} hash mismatch (want $hashfmt:$want, got $got)"
    else
      curl -sSLf -o /dev/null --head "$url" || err "$channel: ${meta#$dir/} download URL is unreachable: $url"
    fi
  done < <(
    find "$dir" -name '*.pw.toml' | sort | while IFS= read -r meta; do
      awk -v m="$meta" '
        /^\[download\]/ { in_dl = 1; next }
        /^\[/           { in_dl = 0 }
        in_dl && /^url *=/         { u = $0; sub(/^url *= *"/, "", u); sub(/"$/, "", u) }
        in_dl && /^hash-format *=/ { f = $0; sub(/^hash-format *= *"/, "", f); sub(/"$/, "", f) }
        in_dl && /^hash *=/        { h = $0; sub(/^hash *= *"/, "", h); sub(/"$/, "", h) }
        END { if (u != "") printf "%s\t%s\t%s\t%s\n", m, u, f, h }
      ' "$meta"
    done
  )
  $full && echo "  downloads verified by hash" || echo "  download URLs reachable"
done

exit $fail
