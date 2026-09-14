#!/usr/bin/env bash
# Extract a single version's section from CHANGELOG.md and print it on
# stdout. Used by the release workflow to feed the matching
# `## [vX.Y.Z]` block to `goreleaser release --release-notes`.
#
# Usage:
#   scripts/extract-release-notes.sh <version>
#
# <version> may be passed with or without a leading `v`; both
# `v0.1.0` and `0.1.0` work.

set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <version>" >&2
  exit 2
fi

tag="${1#v}"

awk -v t="$tag" '
  $0 ~ "^## \\[" t "\\]" { in_section=1; next }
  in_section && $0 ~ "^## \\[" { exit }
  in_section {
    # Lift the headings one level. In the file a version is an h2 and its
    # change kinds are h3s under it; on the release page the version
    # heading is gone, because GitHub renders the tag name as the h1, so
    # an unaltered section starts at h3 and skips a rank. Fenced code is
    # left alone, since a `#` comment in a shell block is not a heading.
    if ($0 ~ /^```/) { fenced = !fenced }
    if (!fenced && $0 ~ /^###/) { sub(/^#/, "") }
    print
  }
' CHANGELOG.md
