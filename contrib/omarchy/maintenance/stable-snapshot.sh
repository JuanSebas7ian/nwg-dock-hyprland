#!/usr/bin/env bash
# S2: create the "stable" snapshots and the version manifest. Refuses unless reward.py says stable.
# Run from a visible terminal (sudo). Log: ~/.local/state/omarchy-maintenance/stable-<date>.log, ends with exit=N.
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
STATE=${OMARCHY_MAINT_STATE:-$HOME/.local/state/omarchy-maintenance}
TODAY=$(date +%F)
MAN=$HOME/.local/state/omarchy-stable/$TODAY
FORK_DIR=$(cd "$HERE/../../.." && pwd)

main() {
    local out
    mkdir -p "$STATE" "$MAN"
    "$HERE/smoke-test.sh" --json >"$MAN/smoke.json"
    python3 "$HERE/reward.py" "$MAN/smoke.json" >"$MAN/reward.json"
    if [ $? -ne 0 ]; then echo "reward no es estable: me niego a crear el snapshot."; cat "$MAN/reward.json"; return 1; fi
    echo ">>> Escribe tu contrasena de sudo. <<<"
    sudo -v || return 1
    sudo snapper -c root create -d "stable $TODAY: maintenance" -u important=yes || return 1
    sudo snapper -c home create -d "stable $TODAY: maintenance" || return 1
    pacman -Q >"$MAN/pacman-Q.txt"
    uname -r >"$MAN/kernel.txt"
    pacman -Q nvidia nvidia-open nvidia-open-dkms nvidia-utils 2>/dev/null >"$MAN/nvidia.txt"
    omarchy-hwcheck --json >"$MAN/hwcheck.json" 2>&1
    git -C "$FORK_DIR" rev-parse HEAD >"$MAN/fork-commit.txt"
    git -C "$FORK_DIR" tag -f "stable-$TODAY" && git -C "$FORK_DIR" push fork "stable-$TODAY" --force
    echo "manifiesto en $MAN"
    sudo snapper -c root list | tail -n 5
}
LOG="$STATE/stable-$(date +%Y%m%d-%H%M%S).log"
main 2>&1 | tee -a "$LOG"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$LOG"
read -rp "Enter para cerrar"
exit "$rc"
