#!/usr/bin/env bash
# S2: create the "stable" snapshots and the version manifest. Refuses unless reward.py says stable
# and the fork working tree is clean (nothing is created when it refuses).
# Run from a visible terminal (sudo). Log: ~/.local/state/omarchy-maintenance/stable-<date>.log, ends with exit=N.
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
STATE=${OMARCHY_MAINT_STATE:-$HOME/.local/state/omarchy-maintenance}
TODAY=$(date +%F)
MAN=$HOME/.local/state/omarchy-stable/$TODAY
FORK_DIR=$(cd "$HERE/../../.." && pwd)

main() {
    local tmp dirty n fails=0
    tmp=$(mktemp -d) || return 1
    trap 'rm -rf "$tmp"' RETURN

    # Pre-checks: nothing is written outside $tmp until all of them pass.
    dirty=$(git -C "$FORK_DIR" status --porcelain) || { echo "git status fallo"; return 1; }
    if [ -n "$dirty" ]; then echo "el arbol de $FORK_DIR no esta limpio: me niego."; echo "$dirty"; return 1; fi
    "$HERE/smoke-test.sh" --json >"$tmp/smoke.json"
    python3 "$HERE/reward.py" "$tmp/smoke.json" >"$tmp/reward.json"
    if [ $? -ne 0 ]; then echo "reward no es estable: me niego a crear el snapshot."; cat "$tmp/reward.json"; return 1; fi
    echo ">>> Escribe tu contrasena de sudo. <<<"
    sudo -v || return 1

    mkdir -p "$MAN" || return 1
    cp "$tmp/smoke.json" "$tmp/reward.json" "$MAN/" || return 1
    n=$(sudo snapper -c root create -p -d "stable $TODAY: maintenance" -u important=yes) \
        && echo "root=$n" >"$MAN/snapshots.txt" || { echo "snapshot root fallo"; return 1; }
    n=$(sudo snapper -c home create -p -d "stable $TODAY: maintenance") \
        && echo "home=$n" >>"$MAN/snapshots.txt" || { echo "snapshot home fallo"; fails=$((fails + 1)); }
    echo "snapshots: $(tr '\n' ' ' <"$MAN/snapshots.txt")"

    pacman -Q >"$MAN/pacman-Q.txt" || { echo "pacman -Q fallo"; fails=$((fails + 1)); }
    uname -r >"$MAN/kernel.txt" || fails=$((fails + 1))
    pacman -Q nvidia nvidia-open nvidia-open-dkms nvidia-utils >"$MAN/nvidia.txt" 2>/dev/null
    [ -s "$MAN/nvidia.txt" ] || { echo "sin paquetes nvidia en pacman -Q"; fails=$((fails + 1)); }
    # hwcheck exit codes 1/2 are findings, not failures of this script.
    omarchy-hwcheck --json >"$MAN/hwcheck.json" 2>"$MAN/hwcheck.err"; echo "hwcheck rc=$? (informativo)"
    git -C "$FORK_DIR" rev-parse HEAD >"$MAN/fork-commit.txt" || { echo "git rev-parse fallo"; fails=$((fails + 1)); }
    git -C "$FORK_DIR" tag -f "stable-$TODAY" || { echo "git tag fallo"; fails=$((fails + 1)); }
    git -C "$FORK_DIR" push fork "stable-$TODAY" --force || { echo "git push fallo"; fails=$((fails + 1)); }
    echo "manifiesto en $MAN; errores: $fails"
    return $((fails > 0 ? 1 : 0))
}
mkdir -p "$STATE"
LOG="$STATE/stable-$(date +%Y%m%d-%H%M%S).log"
main 2>&1 | tee -a "$LOG"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$LOG"
read -rp "Enter para cerrar"
exit "$rc"
