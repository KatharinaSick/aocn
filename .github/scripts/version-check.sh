#!/usr/bin/env bash
# Checks that every part a PR changes has its VERSION bumped, and says so in one PR comment.
# The same script is in both of the project's repos. Keep them the same.
#
# PARTS has one part per line: its name, its VERSION file, then the folders it's built from.
#   app app/VERSION app
# READMEs and Go tests don't end up in what's published, so changing only them needs no bump.
#
# The comment is created when a part needs a bump, updated on later pushes and deleted once
# every part is bumped. Without COMMENT=true (PRs from forks, whose token can't comment), the
# result only goes to the log.
#
# Needs: PARTS, BASE (the base branch), and for comments COMMENT, PR, GH_REPO and GH_TOKEN.
set -euo pipefail

: "${PARTS:?}" "${BASE:?}"
MARKER='<!-- version-check -->'
EXCLUDE=(':(exclude,glob)**/*.md' ':(exclude,glob)**/*_test.go')

# newer A B: succeeds if version A is higher than version B.
newer() {
  [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" = "$1" ]
}

base="origin/$BASE"
missing=()
while read -r name file paths; do
  [ -n "$name" ] || continue
  # shellcheck disable=SC2086 # paths is a list of folders
  if [ -z "$(git diff --name-only "$base...HEAD" -- $paths "${EXCLUDE[@]}")" ]; then
    echo "$name: unchanged"
    continue
  fi
  head="$(tr -d '[:space:]' < "$file")"
  # A part that's new in this PR has nothing to bump.
  if ! old="$(git show "$base:$file" 2>/dev/null | tr -d '[:space:]')" || [ -z "$old" ]; then
    echo "$name: new at $head"
  elif newer "$head" "$old"; then
    echo "$name: bumped $old → $head"
  else
    echo "::warning::$name changed, but $file ($head) isn't higher than on $BASE ($old)"
    missing+=("- **$name**: \`$file\` is $head, the same as or lower than on \`$BASE\` ($old)")
  fi
done <<< "$PARTS"

[ "${COMMENT:-}" = true ] || exit 0

existing="$(gh api "repos/$GH_REPO/issues/$PR/comments" --paginate \
  --jq ".[] | select(.body | startswith(\"$MARKER\")) | .id" | head -1)"

if [ ${#missing[@]} -eq 0 ]; then
  [ -z "$existing" ] || gh api -X DELETE "repos/$GH_REPO/issues/comments/$existing" >/dev/null
  exit 0
fi

body="$MARKER
These parts changed, but their version didn't go up:

$(printf '%s\n' "${missing[@]}")

Without a bump, merging publishes nothing for them. Bump the patch version for fixes, the minor
version for something new."

if [ -n "$existing" ]; then
  gh api -X PATCH "repos/$GH_REPO/issues/comments/$existing" -f body="$body" >/dev/null
else
  gh api "repos/$GH_REPO/issues/$PR/comments" -f body="$body" >/dev/null
fi
