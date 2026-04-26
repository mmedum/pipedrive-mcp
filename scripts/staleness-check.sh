#!/usr/bin/env bash
# Fail when a direct dependency in go.mod is more than 6 months out of date
# unless it has a corresponding `// pinned: <reason>` comment in go.mod itself.
#
# Only direct deps are checked — indirect deps roll forward freely via
# Dependabot. Six months gives Dependabot time to surface upgrades without
# forcing emergency bumps.

set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "go not installed; cannot check staleness" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq not installed; cannot check staleness" >&2
  exit 2
fi

if [ ! -f go.mod ]; then
  echo "no go.mod; nothing to check"
  exit 0
fi

threshold_seconds=$((60*60*24*30*6))
now=$(date +%s)
fail=0

while IFS=$'\t' read -r module current available; do
  # Skip pinned deps with a documented reason in go.mod.
  if grep -E "^[[:space:]]+${module} .*// pinned:" go.mod >/dev/null 2>&1; then
    echo "skip $module — pinned"
    continue
  fi

  ts=$(go list -m -json "${module}@${available}" 2>/dev/null | jq -r '.Time // empty')
  if [ -z "$ts" ]; then
    echo "warn: could not resolve release date for ${module}@${available}"
    continue
  fi
  release_epoch=$(date -d "$ts" +%s 2>/dev/null || echo 0)
  if [ "$release_epoch" = "0" ]; then
    continue
  fi
  age=$((now - release_epoch))
  if [ "$age" -gt "$threshold_seconds" ]; then
    echo "::error::$module: $current → $available available (released $ts, > 6 months ago)"
    echo "         add \`// pinned: <reason>\` after the require line in go.mod or upgrade"
    fail=1
  else
    echo "ok    $module: $current ($available available, released $ts)"
  fi
done < <(
  go list -m -u -json all \
    | jq -r 'select(.Indirect != true and .Update != null)
             | [.Path, .Version, .Update.Version] | @tsv'
)

[ "$fail" = "0" ]
