#!/usr/bin/env bash
# Enforce the per-package coverage floors declared in .coverage-floors.
#
# Run from the repository root:
#   scripts/coverage.sh              # test and check
#   scripts/coverage.sh --report     # print the table, exit 0 regardless
#
# Floors ratchet upward only. When a package comfortably clears its floor,
# raise the floor in the same PR — that is what stops coverage sliding back.
set -euo pipefail

cd "$(dirname "$0")/.."
report_only=false
[ "${1:-}" = "--report" ] && report_only=true

profile=$(mktemp)
trap 'rm -f "$profile"' EXIT

# -covermode=atomic because the same suite runs under -race in CI, and the
# default `set` mode is not race-safe.
go test -covermode=atomic -coverprofile="$profile" ./... >/dev/null

# Per-package percentages, computed from the raw profile so they are
# statement-weighted and agree with what `go test -cover` reports. Averaging
# the per-function percentages `go tool cover -func` prints does NOT agree: it
# weights a one-line function the same as a hundred-line one.
#
# Profile rows are: path/to/file.go:startLine.col,endLine.col numStmt count
pkg_cov=$(awk 'NR > 1 {
    split($1, a, ":"); path = a[1]
    n = split(path, seg, "/"); pkg = ""
    for (i = 1; i < n; i++) pkg = pkg (i > 1 ? "/" : "") seg[i]
    stmts[pkg] += $2
    if ($3 > 0) covered[pkg] += $2
  }
  END { for (p in stmts) if (stmts[p] > 0) printf "%s %.1f\n", p, 100 * covered[p] / stmts[p] }
' "$profile")
total=$(go tool cover -func="$profile" | awk '$1 == "total:" { sub(/%$/, "", $3); print $3 }')

fail=0
printf '%-42s %8s %8s\n' PACKAGE COVERAGE FLOOR
printf '%-42s %8s %8s\n' "------------------------------------------" "--------" "--------"

while read -r name floor; do
  [ -z "${name:-}" ] && continue
  case "$name" in \#*) continue ;; esac

  if [ "$name" = "total" ]; then
    have=$total
  else
    have=$(printf '%s\n' "$pkg_cov" | awk -v p="$name" '$1 == p { print $2 }')
    if [ -z "$have" ]; then
      printf '%-42s %8s %8s  MISSING\n' "$name" "-" "$floor"
      echo "::error::$name has a coverage floor but produced no coverage — was it deleted or renamed?"
      fail=1
      continue
    fi
  fi

  if awk -v h="$have" -v f="$floor" 'BEGIN { exit !(h + 0 < f + 0) }'; then
    printf '%-42s %8s %8s  BELOW FLOOR\n' "$name" "$have" "$floor"
    echo "::error::$name coverage $have% is below its floor of $floor%"
    fail=1
  else
    printf '%-42s %8s %8s\n' "$name" "$have" "$floor"
  fi
done < <(grep -vE '^\s*(#|$)' .coverage-floors)

$report_only && exit 0
exit $fail
