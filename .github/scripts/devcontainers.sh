#!/usr/bin/env bash
# Writes .devcontainer/door-NN/devcontainer.json for every door in schedule.yaml, from the
# template below and the image in .devcontainer/devcontainer.json. A door needs its own file:
# a "play" link can only pick a devcontainer.json (devcontainer_path), not pass the door along,
# and quickstart offers the player's existing Codespace for the same file. Bump the image in
# the root devcontainer.json, then run this.
#
# .github/scripts/devcontainers.sh           write them
# .github/scripts/devcontainers.sh --check   fail if any is missing, extra or edited by hand
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

image="$(sed -n 's/^ *"image": *"\([^"]*\)".*/\1/p' .devcontainer/devcontainer.json)"
[ -n "$image" ] || { echo "devcontainers: no image in .devcontainer/devcontainer.json" >&2; exit 1; }

# The door numbers: the keys under doors: in schedule.yaml.
doors="$(awk '/^doors:/ {in_doors = 1; next} /^[^ #]/ {in_doors = 0} in_doors && /^ +[0-9]+:/ {sub(":", "", $1); print $1}' schedule.yaml)"
[ -n "$doors" ] || { echo "devcontainers: no doors in schedule.yaml" >&2; exit 1; }

render() {
  local n="$1" nn
  nn="$(printf '%02d' "$n")"
  cat <<EOF
{
  // The Codespace for door $n, which the door's "play" link on aocn.dev opens. Written by
  // .github/scripts/devcontainers.sh, don't edit it by hand.
  //
  // The same as ../devcontainer.json, plus AOCN_DOOR: the image's postStartCommand
  // (aocn resume) opens door $n on the first start, and starts it again on every start after
  // that. Before the door opens, a previewer signs in with aocn login and opens it with
  // aocn open $n. Start commands never run in a prebuild, so no door is downloaded before it
  // opens. On creation, it links play/README.md, the "your station is starting" page, to
  // door $n's page; git ignores that change, like the door's README later.
  "name": "Advent of Cloud Native · door $nn",
  "image": "$image",
  "workspaceFolder": "/workspaces/\${localWorkspaceFolderBasename}/play",
  "containerEnv": {
    "AOCN_WORKSPACE": "/workspaces/\${localWorkspaceFolderBasename}/play",
    "AOCN_DOOR": "$n"
  },
  "postCreateCommand": "git update-index --skip-worktree README.md && sed -i 's|page: https://aocn.dev\$|page: https://aocn.dev/doors/$n|' README.md",
  "customizations": {
    "vscode": {
      "settings": {
        "files.exclude": {
          ".gitignore": true,
          ".open.json": true
        }
      }
    }
  }
}
EOF
}

check=false
[ "${1-}" = --check ] && check=true
failed=0
wanted=()
for n in $doors; do
  file=".devcontainer/door-$(printf '%02d' "$n")/devcontainer.json"
  wanted+=("$file")
  if $check; then
    if ! render "$n" | diff -u "$file" - >/dev/null 2>&1; then
      echo "devcontainers: $file is missing or differs from what the script writes" >&2
      failed=1
    fi
  else
    mkdir -p "$(dirname "$file")"
    render "$n" >"$file"
  fi
done

# A door that left the schedule leaves a file behind that no link opens.
for file in .devcontainer/door-*/devcontainer.json; do
  [ -e "$file" ] || continue
  if ! printf '%s\n' "${wanted[@]}" | grep -qx "$file"; then
    echo "devcontainers: $file has no door in schedule.yaml" >&2
    failed=1
  fi
done

if [ "$failed" = 1 ]; then
  echo "Run .github/scripts/devcontainers.sh and commit what it writes." >&2
  exit 1
fi
$check || echo "devcontainers: wrote ${#wanted[@]} doors with $image"
