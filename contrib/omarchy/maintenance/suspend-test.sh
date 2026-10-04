#!/usr/bin/env bash
# Step 7 (manual): suspend to RAM for 30 s (rtcwake alarm + systemctl suspend), then inspect the journal.
# Saves your work first: the screen goes dark and the PC wakes by itself.
set -uo pipefail
STATE=${OMARCHY_MAINT_STATE:-$HOME/.local/state/omarchy-maintenance}
mkdir -p "$STATE"
LOG="$STATE/suspend-$(date +%Y%m%d-%H%M%S).log"
main() {
    local t0 j
    echo ">>> Guarda tu trabajo. Escribe tu contrasena de sudo; el equipo se suspende 30 s. <<<"
    sudo -v || return 1
    t0=$(date '+%F %T')
    # Arm the RTC alarm only, then suspend through logind (same path as the desktop).
    sudo rtcwake -m no -s 30 && systemctl suspend; echo "suspend rc=$?"
    sleep 3
    j=$(journalctl -b --since "$t0" --no-pager 2>/dev/null)
    if [[ "$j" == *"PM: suspend exit"* ]]; then echo "PASS: PM: suspend exit"; else echo "FAIL: no hay 'PM: suspend exit'"; fi
    if [[ "$j" == *"NVRM: Xid"* ]]; then echo "FAIL: NVRM: Xid en el journal"; grep 'NVRM: Xid' <<<"$j"; else echo "PASS: sin NVRM: Xid"; fi
}
main 2>&1 | tee -a "$LOG"
echo "exit=${PIPESTATUS[0]}" | tee -a "$LOG"
read -rp "Enter para cerrar"
