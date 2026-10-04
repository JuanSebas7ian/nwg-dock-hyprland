#!/usr/bin/env bash
# Install pad-keepalive for the current user (no root): ~/.local/bin + a user service.
# Usage: install.sh [--remove]
# Needs python-evdev (repo package; listed in ../manifest.json, group peripherals) and the user in the
# `input` group (Omarchy keeps it when xpadneo-dkms is installed).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=$HOME/.local/bin/pad-keepalive
UNIT=$HOME/.config/systemd/user/pad-keepalive.service
BACKUP=$HOME/.local/state/omarchy-drivers/backups/pad-keepalive-$(date +%Y%m%d-%H%M%S)

if [ "${1:-}" = --remove ]; then
    systemctl --user disable --now pad-keepalive.service 2>/dev/null || true
    rm -f "$BIN" "$UNIT"
    systemctl --user daemon-reload
    echo "pad-keepalive removed"
    exit 0
fi

/usr/bin/python3 -c 'import evdev' 2>/dev/null || { echo "python-evdev missing: sudo pacman -S --needed python-evdev" >&2; exit 1; }
groups=" $(id -nG) "   # no grep -q in a pipe: SIGPIPE + pipefail fails at random
[[ $groups == *" input "* ]] || echo "warning: $USER is not in the input group; the pad cannot be grabbed" >&2

put() { # src dest mode
    if [ -e "$2" ] && ! cmp -s "$1" "$2"; then mkdir -p "$BACKUP"; cp -a "$2" "$BACKUP/"; echo "backup: $BACKUP/$(basename "$2")"; fi
    install -Dm "$3" "$1" "$2"
}
put "$HERE/pad-keepalive" "$BIN" 755
put "$HERE/pad-keepalive.service" "$UNIT" 644
systemctl --user daemon-reload
systemctl --user enable --quiet pad-keepalive.service
systemctl --user restart pad-keepalive.service
sleep 1
systemctl --user is-active pad-keepalive.service
