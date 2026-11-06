#!/usr/bin/env bash
# Prints the release version: <mygo.json version>_<commits ahead of upstream>.
# mygo.json keeps the upstream x.y.z and is not modified. Ahead is the number
# of commits reachable from HEAD but not from Licoy/sshyard main.
# Usage: fork-version.sh [upstream-url]
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

base="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' mygo.json | head -n 1)"
[[ "$base" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  printf 'mygo.json version must stay x.y.z, got: %s\n' "$base" >&2
  exit 1
}

if [ "$(git rev-parse --is-shallow-repository)" = true ]; then
  git fetch --unshallow --no-tags origin
fi

url="${1:-${FORK_UPSTREAM_URL:-https://github.com/Licoy/sshyard.git}}"
case "$url" in
  [A-Za-z]:*)
    drive="${url%%:*}"
    rest="${url#*:}"
    drive="$(printf '%s' "$drive" | tr '[:upper:]' '[:lower:]')"
    if [ -d "/mnt/$drive" ]; then
      url="/mnt/$drive$rest"
    fi
    ;;
esac
git fetch --no-tags "$url" +refs/heads/main:refs/remotes/fork-upstream/main
ahead="$(git rev-list --count refs/remotes/fork-upstream/main..HEAD)"
printf '%s_%s\n' "$base" "$ahead"
