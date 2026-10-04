#!/usr/bin/env bash
# Read-only, root-less smoke test of the maintenance plan.
# One line per check: PASS|FAIL|WARN|SKIP <id> <text>; --json for reward.py.
# Env: SYSFS_ROOT, PROC_ROOT (fake trees for tests), SMARTD_CONF, BOOT_USED_PCT, DRIVERS_DIR (override).
# Option: --only ID,ID  runs just those checks.
set -uo pipefail
export LC_ALL=C

SYSFS_ROOT=${SYSFS_ROOT:-/sys}
PROC_ROOT=${PROC_ROOT:-/proc}
SMARTD_CONF=${SMARTD_CONF:-/etc/smartd.conf}
DRIVERS_DIR=${DRIVERS_DIR:-$(cd "$(dirname "$0")/../drivers" && pwd)}
HOOK_ETC=${HOOK_ETC:-/etc/pacman.d/hooks}; HOOK_LIB=${HOOK_LIB:-/usr/local/lib/omarchy}
JSON=0
ONLY=""
while [ $# -gt 0 ]; do
    case "$1" in
        --json) JSON=1 ;;
        --only) ONLY=",$2,"; shift ;;
        -h|--help) sed -n '2,6p' "$0"; exit 0 ;;
        *) echo "uso: $0 [--json] [--only ID,ID]" >&2; exit 3 ;;
    esac
    shift
done

LINES=()
HAS_FAIL=0
want() { [ -z "$ONLY" ] || [[ "$ONLY" == *",$1,"* ]]; }
emit() { # status id text
    LINES+=("$1"$'\t'"$2"$'\t'"$3")
    [ "$1" = FAIL ] && HAS_FAIL=1
}
timer_ok() {
    local en act
    en=$(systemctl is-enabled "$1" 2>/dev/null); act=$(systemctl is-active "$1" 2>/dev/null)
    [ "$en" = enabled ] && [ "$act" = active ]
}
timer_check() { # id unit text
    want "$1" || return 0
    if timer_ok "$2"; then emit PASS "$1" "$3 enabled y activo"
    else emit FAIL "$1" "$3 no esta enabled+activo"; fi
}

# --- TRIM ---
if want TRIM01; then
    found=""
    for d in "$SYSFS_ROOT"/block/dm-*; do
        [ -r "$d/dm/name" ] || continue
        [ "$(cat "$d/dm/name")" = root ] || continue
        found=$d; break
    done
    if [ -z "$found" ]; then
        emit FAIL TRIM01 "no hay mapeo dm llamado root"
    else
        val=$(cat "$found/queue/discard_max_bytes" 2>/dev/null || echo 0)
        [[ "$val" =~ ^[0-9]+$ ]] || val=0
        if [ "$val" -gt 0 ]; then emit PASS TRIM01 "discard_max_bytes de root = $val (> 0)"
        else emit FAIL TRIM01 "discard_max_bytes de root = 0: LUKS bloquea TRIM"; fi
    fi
fi
timer_check TRIM02 fstrim.timer "fstrim.timer"

# --- smartd ---
if want SMART01; then
    if [ "$(systemctl is-active smartd 2>/dev/null)" = active ]; then emit PASS SMART01 "smartd activo"
    else emit FAIL SMART01 "smartd no esta activo"; fi
fi
if want SMART02; then
    script=""
    [ -r "$SMARTD_CONF" ] && script=$(grep -E '^[[:space:]]*[^#[:space:]].*-M[[:space:]]+exec[[:space:]]+' "$SMARTD_CONF" \
        | head -n1 | sed -E 's/.*-M[[:space:]]+exec[[:space:]]+([^[:space:]]+).*/\1/')
    if [ -z "$script" ]; then emit FAIL SMART02 "$SMARTD_CONF no tiene -M exec"
    elif [ -x "$script" ]; then emit PASS SMART02 "-M exec apunta a $script (ejecutable)"
    else emit FAIL SMART02 "-M exec apunta a $script, que no es ejecutable"; fi
fi
if want SMART03; then
    jr=$(journalctl -u smartd -b --no-pager -q 2>&1)
    if [ -z "$jr" ] || [[ "$jr" == *"No journal files"* ]] || [[ "$jr" == *"not seeing messages"* ]]; then
        emit SKIP SMART03 "journal de smartd ilegible o vacio sin root"
    elif grep -Eiq 'unable to parse|invalid|unknown (option|directive)|problem creating|bad configuration' <<<"$jr"; then
        emit FAIL SMART03 "errores de configuracion en journalctl -u smartd"
    else emit PASS SMART03 "sin errores de configuracion en journalctl -u smartd"; fi
fi

# --- pacman cache, scrub, orphans, packages ---
timer_check PACCACHE01 paccache.timer "paccache.timer"
if want PACCACHE02; then
    pc=$(paccache -dk3 2>&1)
    if [[ "$pc" == *"no candidate packages found"* ]]; then emit PASS PACCACHE02 "ninguna version con mas de 3 copias en la cache"
    else emit WARN PACCACHE02 "hay copias sobrantes en la cache de pacman (paccache -dk3)"; fi
fi
timer_check SCRUB01 'btrfs-scrub@-.timer' "btrfs-scrub@-.timer"
if want ORPHAN01; then
    orph=$(pacman -Qdtq 2>/dev/null | tr '\n' ' ')
    if [ -z "$orph" ]; then emit PASS ORPHAN01 "sin paquetes huerfanos"
    else emit FAIL ORPHAN01 "huerfanos: ${orph% }"; fi
fi
if want PKG01; then
    missing=""
    for p in smartmontools gst-plugins-good gst-plugins-bad gst-plugins-ugly gst-libav libva-utils nvme-cli; do
        pacman -Q "$p" >/dev/null 2>&1 || missing="$missing $p"
    done
    if [ -z "$missing" ]; then emit PASS PKG01 "paquetes de codecs y diagnostico instalados"
    else emit FAIL PKG01 "faltan:$missing"; fi
fi
if want PKG02; then
    if ! command -v vainfo >/dev/null 2>&1; then emit WARN PKG02 "vainfo no instalado"
    else
        vi=$(vainfo 2>&1)
        if [[ "${vi,,}" == *nvidia* || "${vi,,}" == *nvdec* ]] && [[ "$vi" == *VAProfile* ]]; then
            emit PASS PKG02 "vainfo lista perfiles con el driver nvidia (NVDEC)"
        else emit WARN PKG02 "vainfo no lista perfiles con el driver nvidia"; fi
    fi
fi

# --- /boot ---
if want BOOT01; then
    if [ -n "${BOOT_USED_PCT:-}" ]; then pct=$BOOT_USED_PCT
    elif grep -q ' /boot ' "$PROC_ROOT/mounts" 2>/dev/null; then
        pct=$(df --output=pcent /boot 2>/dev/null | tail -n1 | tr -dc '0-9')
    else pct=""; fi
    if [ -z "$pct" ]; then emit SKIP BOOT01 "/boot no es un montaje propio"
    elif [ "$pct" -lt 85 ]; then emit PASS BOOT01 "/boot al ${pct} % (< 85 %)"
    elif [ "$pct" -lt 95 ]; then emit WARN BOOT01 "/boot al ${pct} % (>= 85 %)"
    else emit FAIL BOOT01 "/boot al ${pct} %"; fi
fi

# --- failed services, Hyprland ---
if want SVC01; then
    f=$( { systemctl --failed --no-legend --plain 2>/dev/null; systemctl --user --failed --no-legend --plain 2>/dev/null; } \
        | awk 'NF {print $1}' | tr '\n' ' ')
    if [ -z "$f" ]; then emit PASS SVC01 "sin servicios fallidos"
    else emit FAIL SVC01 "fallidos: ${f% }"; fi
fi
if want HYPR01; then
    if ! command -v hyprctl >/dev/null 2>&1 || [ -z "${HYPRLAND_INSTANCE_SIGNATURE:-}" ]; then
        emit SKIP HYPR01 "sin sesion de Hyprland"
    else
        he=$(hyprctl configerrors 2>&1); rc=$?
        if [ $rc -ne 0 ]; then emit WARN HYPR01 "hyprctl configerrors fallo (rc=$rc)"
        elif [ -z "$he" ]; then emit PASS HYPR01 "hyprctl configerrors vacio"
        else emit FAIL HYPR01 "hyprctl configerrors: $(printf '%s' "$he" | head -n1)"; fi
    fi
fi

# --- drivers module (contrib/omarchy/drivers) ---
if want DRIVERS01; then
    dchk=$("$DRIVERS_DIR/install.sh" --check 2>&1); drc=$?
    if [ "$drc" -eq 0 ]; then emit PASS DRIVERS01 "drivers: install.sh --check limpio"
    else emit FAIL DRIVERS01 "drivers: install.sh --check: $(grep -m1 -E '^(FALTA|DIFIERE)' <<<"$dchk")"; fi
fi
if want DRIVERS02; then
    dout=$(python3 "$DRIVERS_DIR/compat.py" post 2>&1); drc=$?
    if [ "$drc" -ge 2 ]; then emit FAIL DRIVERS02 "drivers: compat.py post: $(grep -m1 '^FAIL' <<<"$dout")"
    elif [ "$drc" -eq 1 ]; then emit WARN DRIVERS02 "drivers: compat.py post con avisos: $(grep -m1 '^WARN' <<<"$dout")"
    else emit PASS DRIVERS02 "drivers: compat.py post sin fallos"; fi
fi

if want HOOK01; then  # not part of the reward: informational only
    bad=""
    [ -f "$HOOK_ETC/90-omarchy-compat.hook" ] && cmp -s "$DRIVERS_DIR/pacman-hook/90-omarchy-compat.hook" "$HOOK_ETC/90-omarchy-compat.hook" || bad="$bad hook"
    [ -x "$HOOK_LIB/compat-hook" ] && cmp -s "$DRIVERS_DIR/pacman-hook/compat-hook" "$HOOK_LIB/compat-hook" || bad="$bad compat-hook"
    [ -x "$HOOK_LIB/compat.py" ] && cmp -s "$DRIVERS_DIR/compat.py" "$HOOK_LIB/compat.py" || bad="$bad compat.py"
    if [ -z "$bad" ]; then emit PASS HOOK01 "drivers: gancho de pacman instalado y al día"
    else emit WARN HOOK01 "drivers: gancho de pacman ausente o desactualizado:$bad"; fi
fi

if [ "$JSON" -eq 1 ]; then
    printf '%s\n' "${LINES[@]}" | python3 -c '
import json, sys
checks = []
for line in sys.stdin.read().splitlines():
    if line:
        s, i, t = line.split("\t", 2)
        checks.append({"id": i, "status": s, "text": t})
print(json.dumps({"checks": checks}, ensure_ascii=False, indent=1))'
else
    for l in "${LINES[@]}"; do IFS=$'\t' read -r s i t <<<"$l"; echo "$s $i $t"; done
fi
[ "$HAS_FAIL" -eq 0 ]
