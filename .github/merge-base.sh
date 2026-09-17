#!/usr/bin/env bash
# The commit a pull request should be measured against.
#
# NOT github.event.pull_request.base.sha. That is a snapshot taken when
# the event fired and it does not follow the base branch afterwards, so
# every gate keyed off it compares against a main that has moved on. In
# a stack of pull requests this is the normal case rather than the edge
# one: merging the PR underneath leaves the one above measuring a diff
# that includes its own dependency, which the changelog gate reads as
# "no new lines" and the schema gate reads as a breaking change.
#
# Reopening a PR does not refresh it either — only a push to the head
# branch does, and a push is the one thing that cannot be done here
# without displacing the footer the schema gate greps for.
#
# The merge-base of the CURRENT base branch and the head is what every
# gate actually means by "what this PR adds".
set -euo pipefail

base_ref=$1
head_sha=$2

git fetch --no-tags --quiet origin "$base_ref"
git merge-base FETCH_HEAD "$head_sha"
