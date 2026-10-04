#!/usr/bin/env bash
# Backups and self-restore for this Omarchy PC.
#   contrib/omarchy/restore/install.sh            install omarchy-backup/omarchy-restore, the daily timer and the
#                                                 self-heal boot hook; create the encrypted repository on Google Drive
#   contrib/omarchy/restore/install.sh --snapper  also create snapper's hourly snapshots of /home (sudo)
# Idempotent. Never overwrites the restic password or edited settings.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}
CONF=$CONFIG/omarchy-backup
say() { printf '==> %s\n' "$*"; }

for dep in restic rclone python3; do
  command -v "$dep" >/dev/null || { echo "missing $dep: omarchy pkg add $dep" >&2; exit 1; }
done
rclone listremotes | grep -qx 'rclone:' || { echo "configure Google Drive first: rclone config create rclone drive" >&2; exit 1; }

say "scripts, timer and boot hook"
install -D -m 755 "$HERE/bin/omarchy-backup" "$HOME/.local/bin/omarchy-backup"
install -D -m 755 "$HERE/bin/omarchy-restore" "$HOME/.local/bin/omarchy-restore"
install -D -m 644 "$HERE/systemd/omarchy-backup.service" "$CONFIG/systemd/user/omarchy-backup.service"
install -D -m 644 "$HERE/systemd/omarchy-backup.timer" "$CONFIG/systemd/user/omarchy-backup.timer"
install -D -m 755 "$HERE/hooks/omarchy-selfheal.hook" "$CONFIG/omarchy/hooks/post-boot.d/omarchy-selfheal.hook"

mkdir -p "$CONF" && chmod 700 "$CONF"
[[ -e $CONF/config ]] || install -m 644 "$HERE/config/config" "$CONF/config"
[[ -e $CONF/excludes ]] || sed "s|@HOME@|$HOME|g" "$HERE/config/excludes" >"$CONF/excludes"

# shellcheck source=/dev/null
. "$CONF/config"
export RESTIC_REPOSITORY RESTIC_PASSWORD_FILE=$CONF/restic-password
if [[ ! -s $RESTIC_PASSWORD_FILE ]]; then
  if restic cat config >/dev/null 2>&1 </dev/null; then
    echo "A repository already exists at $RESTIC_REPOSITORY: put its password in $RESTIC_PASSWORD_FILE." >&2
    exit 1
  fi
  (umask 077 && python3 -c "import secrets; print(secrets.token_urlsafe(32))" >"$RESTIC_PASSWORD_FILE")
  restic init
  echo
  echo "IMPORTANT: save this restic password in 1Password; without it the Drive backup cannot be restored:"
  echo "  $(cat "$RESTIC_PASSWORD_FILE")"
fi

if [[ ${1:-} == --snapper ]]; then
  say "hourly snapshots of /home (sudo)"
  sudo snapper list-configs | grep -q '^home ' || sudo snapper -c home create-config /home
  sudo snapper -c home set-config TIMELINE_CREATE=yes TIMELINE_CLEANUP=yes TIMELINE_LIMIT_HOURLY=6 \
    TIMELINE_LIMIT_DAILY=7 TIMELINE_LIMIT_WEEKLY=2 TIMELINE_LIMIT_MONTHLY=0 TIMELINE_LIMIT_YEARLY=0 \
    NUMBER_LIMIT=10 ALLOW_USERS="$USER" SYNC_ACL=yes
  sudo systemctl enable --now snapper-timeline.timer snapper-cleanup.timer
fi

systemctl --user daemon-reload
systemctl --user enable --now omarchy-backup.timer
say "done: omarchy-restore list · first backup: systemctl --user start omarchy-backup"
