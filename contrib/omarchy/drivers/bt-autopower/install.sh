#!/usr/bin/env bash
# Install bt-autopower for the current user (no root): ~/.local/bin + a user service.
# Usage: install.sh [--remove]
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=$HOME/.local/bin/bt-autopower
UNIT=$HOME/.config/systemd/user/bt-autopower.service
BACKUP=$HOME/.local/state/omarchy-drivers/backups/bt-autopower-$(date +%Y%m%d-%H%M%S)

if [ "${1:-}" = --remove ]; then
    systemctl --user disable --now bt-autopower.service 2>/dev/null || true
    rm -f "$BIN" "$UNIT"
    systemctl --user daemon-reload
    echo "bt-autopower removed"
    exit 0
fi

put() { # src dest mode
    if [ -e "$2" ] && ! cmp -s "$1" "$2"; then mkdir -p "$BACKUP"; cp -a "$2" "$BACKUP/"; echo "backup: $BACKUP/$(basename "$2")"; fi
    install -Dm "$3" "$1" "$2"
}
put "$HERE/bt-autopower" "$BIN" 755
put "$HERE/bt-autopower.service" "$UNIT" 644
systemctl --user daemon-reload
systemctl --user enable --quiet bt-autopower.service
systemctl --user restart bt-autopower.service
sleep 1
systemctl --user is-active bt-autopower.service
