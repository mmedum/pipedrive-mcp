#!/usr/bin/env bash
# Fail when a PR touches load-bearing source paths without adding an entry
# under [Unreleased] in CHANGELOG.md.
#
# Usage: changelog-check.sh <base-sha> <head-sha>

set -euo pipefail

base_sha="${1:-}"
head_sha="${2:-HEAD}"

if [ -z "$base_sha" ]; then
  echo "usage: $0 <base-sha> <head-sha>" >&2
  exit 2
fi

watched_paths=(
  "internal/tools/"
  "internal/pipedrive/"
  "cmd/"
)

source_changed=0
while IFS= read -r f; do
  for p in "${watched_paths[@]}"; do
    case "$f" in
      "$p"*) source_changed=1 ;;
    esac
  done
done < <(git diff --name-only "$base_sha" "$head_sha")

if [ "$source_changed" = "0" ]; then
  echo "no watched source paths changed; skipping CHANGELOG check"
  exit 0
fi

if ! git diff "$base_sha" "$head_sha" -- CHANGELOG.md | grep -qE '^\+'; then
  echo "::error::source files under internal/{tools,pipedrive}/ or cmd/ changed but CHANGELOG.md was not updated"
  exit 1
fi

# Verify the new lines land under the [Unreleased] section. Use a
# generous unified-context window so the [Unreleased] header is
# always present in the diff regardless of how long the changelog
# has grown — without this, hunks for new entries land far below
# the header and the awk state machine never flips into "in
# Unreleased" mode.
unreleased_added=$(git diff --unified=99999 "$base_sha" "$head_sha" -- CHANGELOG.md \
  | awk '
      /^@@/ { inhunk=1 }
      inhunk && /^\+## \[/ { in_unreleased = ($0 ~ /\[Unreleased\]/); next }
      inhunk && /^ ## \[/  { in_unreleased = ($0 ~ /\[Unreleased\]/); next }
      inhunk && in_unreleased && /^\+/ && !/^\+\+\+/ { count++ }
      END { print count+0 }
    ')

if [ "${unreleased_added:-0}" = "0" ]; then
  echo "::error::CHANGELOG.md changed but no new lines under [Unreleased]"
  exit 1
fi

echo "CHANGELOG entry under [Unreleased] confirmed (${unreleased_added} added lines)"
