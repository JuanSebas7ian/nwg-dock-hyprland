#!/usr/bin/env bash
# Install bt-autopower for the current user (no root): ~/.local/bin + a user service.
# Usage: install.sh [--remove]
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=$HOME/.local/bin/bt-autopower
UNIT=$HOME/.config/systemd/user/bt-autopower.service
BACKUP=$HOME/.local/state/omarchy-drivers/backups/bt-autopower-$(date +%Y%m%d-%H%M%S)

# blueman-applet starts before the MT7925 finishes its ~17 s setup, sees no adapter, requests
# "off" and its KillSwitch plugin soft-blocks Bluetooth with rfkill, which bt-autopower then
# respects as the user's off switch. Turn off blueman's power control (Omarchy's bar handles it).
BLUEMAN_OFF="['!PowerManager', '!KillSwitch']"
has_blueman() { local s; s=$(gsettings list-schemas 2>/dev/null) || return 1; [[ $'\n'$s$'\n' == *$'\norg.blueman.general\n'* ]]; }

if [ "${1:-}" = --remove ]; then
    if has_blueman && [ "$(gsettings get org.blueman.general plugin-list)" = "$BLUEMAN_OFF" ]; then
        gsettings set org.blueman.general plugin-list "[]"
    fi
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
if has_blueman; then
    gsettings set org.blueman.general plugin-list "$BLUEMAN_OFF"
    echo "blueman: PowerManager/KillSwitch disabled"
    if systemctl --user is-active --quiet app-blueman@autostart.service; then
        systemctl --user restart app-blueman@autostart.service
    fi
fi
systemctl --user daemon-reload
systemctl --user enable --quiet bt-autopower.service
systemctl --user restart bt-autopower.service
sleep 1
systemctl --user is-active bt-autopower.service
