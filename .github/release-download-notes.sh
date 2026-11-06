#!/usr/bin/env bash
# Prints the download section appended to the GitHub release notes. Links
# are built from the final asset names, and a missing asset fails instead
# of publishing a dead link. Usage: release-download-notes.sh dist tag owner/repo
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <dist-dir> <tag> <owner/repo>" >&2
  exit 2
fi
dist="$1"
base="https://github.com/$3/releases/download/$2"

# link <label> <glob>: Markdown link to the single asset matching glob
link() {
  local matches=()
  local path
  for path in "$dist"/$2; do
    [[ -e "$path" ]] && matches+=("${path##*/}")
  done
  if [[ ${#matches[@]} -ne 1 ]]; then
    echo "expected one asset matching $2, found ${#matches[@]}" >&2
    return 1
  fi
  printf '[%s](%s/%s)' "$1" "$base" "${matches[0]}"
}

# One assignment per link: in "$(a) · $(b)" only b's status reaches set -e
win_x64_setup="$(link Installer 'sshyard-setup-*-amd64.exe')"
win_x64_portable="$(link 'Portable ZIP' 'sshyard-*-windows-amd64-portable.zip')"
win_arm_setup="$(link Installer 'sshyard-setup-*-arm64.exe')"
win_arm_portable="$(link 'Portable ZIP' 'sshyard-*-windows-arm64-portable.zip')"
checksums="$(link SHA256SUMS 'SHA256SUMS')"

cat <<EOF

---

## Downloads

### Windows

| Architecture | Download |
| --- | --- |
| x64 | $win_x64_setup · $win_x64_portable |
| Arm64 | $win_arm_setup · $win_arm_portable |

The installer installs for the current user into \`%LOCALAPPDATA%\\Programs\` and supports in-app updates; no administrator rights needed.

For the portable version, extract the ZIP into a writable folder and run \`SSHYard.exe\`; no installation needed. App data is stored in \`%APPDATA%\\SSHYard\`, and saved credentials remain tied to the Windows user account.

Verify downloads with $checksums.
EOF
