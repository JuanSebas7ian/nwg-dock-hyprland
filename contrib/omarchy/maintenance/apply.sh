#!/usr/bin/env bash
# Maintenance plan (PLAN.md): S0, steps 1-6, S1. Idempotent: each step checks first and says "skip".
# Usage: apply.sh [--dry-run] [--only S0,1,2,...] [--wait]
#   --dry-run  print what would be done; no root, no changes, no log
#   --wait     ask for Enter before closing (for the visible terminal)
# Log: ~/.local/state/omarchy-maintenance/apply-<date>.log, ends with exit=N.
# Run it from a visible terminal: it needs sudo and, for step 1, the LUKS passphrase.
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
STATE=${OMARCHY_MAINT_STATE:-$HOME/.local/state/omarchy-maintenance}
SYSFS_ROOT=${SYSFS_ROOT:-/sys}
NOTIFY_DST=/usr/local/lib/omarchy/smartd-notify
SMARTD_CONF=/etc/smartd.conf
DATE=$(date +%Y%m%d-%H%M%S)
ALL=(S0 1 2 3 4 5 6 S1)
PKGS6=(gst-plugins-good gst-plugins-bad gst-plugins-ugly gst-libav libva-utils nvme-cli)
ORPHANS5=(drive-bin-debug gdrive-debug)
DRY=0; WAIT=0; ONLY=""

while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run) DRY=1 ;;
        --wait) WAIT=1 ;;
        --only) ONLY=$2; shift ;;
        -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
        *) echo "uso: $0 [--dry-run] [--only S0,1,2,...] [--wait]" >&2; exit 3 ;;
    esac
    shift
done

SEL=()
if [ -n "$ONLY" ]; then
    IFS=',' read -ra want <<<"$ONLY"
    for s in "${ALL[@]}"; do for w in "${want[@]}"; do [ "${w^^}" = "$s" ] && SEL+=("$s"); done; done
else SEL=("${ALL[@]}"); fi

say() { printf '%s\n' "$*"; }
# Root command: printed in dry-run, run through sudo otherwise.
rt() { if [ "$DRY" -eq 1 ]; then say "    [dry] sudo $*"; return 0; fi; sudo "$@"; }
unit_ok() { [ "$(systemctl is-enabled "$1" 2>/dev/null)" = enabled ] && [ "$(systemctl is-active "$1" 2>/dev/null)" = active ]; }
root_discard() {
    local d v
    for d in "$SYSFS_ROOT"/block/dm-*; do
        [ "$(cat "$d/dm/name" 2>/dev/null)" = root ] || continue
        v=$(cat "$d/queue/discard_max_bytes" 2>/dev/null || echo 0); echo "$v"; return
    done
    echo 0
}
orphans_now() { pacman -Qdtq 2>/dev/null || true; }

# ---- checks: return 0 when the step is already done (no root needed) ----
chk_S0() { [ -s "$STATE/pre-number" ]; }
chk_S1() { [ -s "$STATE/post-number" ]; }
chk_1() { [ "$(root_discard)" -gt 0 ] && unit_ok fstrim.timer; }
chk_2() { grep -q "^PACCACHE_ARGS='-k3'" /etc/conf.d/pacman-contrib 2>/dev/null && unit_ok paccache.timer; }
chk_3() { unit_ok 'btrfs-scrub@-.timer'; }
chk_4() {
    command -v smartd >/dev/null 2>&1 || return 1
    grep -qE "^[^#]*-M exec $NOTIFY_DST" "$SMARTD_CONF" 2>/dev/null || return 1
    cmp -s "$HERE/smart-notify" "$NOTIFY_DST" || return 1
    [ -x "$NOTIFY_DST" ] && [ "$(systemctl is-active smartd 2>/dev/null)" = active ] && systemctl is-enabled smartd >/dev/null 2>&1
}
chk_5() {
    local o cur; cur=" $(orphans_now | tr '\n' ' ') "
    for o in "${ORPHANS5[@]}"; do [[ "$cur" == *" $o "* ]] && return 1; done
    return 0
}
chk_6() { pacman -Q "${PKGS6[@]}" >/dev/null 2>&1; }

# ---- actions ----
do_S0() {
    local n
    if [ "$DRY" -eq 1 ]; then say '    [dry] sudo snapper -c root create -t pre -p -d "maintenance: before"'; return 0; fi
    n=$(sudo snapper -c root create -t pre -p -d "maintenance: before") || return 1
    mkdir -p "$STATE"; echo "$n" >"$STATE/pre-number"
    say "snapshot previo (pre) = #$n"
}
do_S1() {
    local pre n
    if [ "$DRY" -eq 1 ]; then say '    [dry] sudo snapper -c root create -t post --pre-number <S0> -d "maintenance: after"'; return 0; fi
    pre=$(cat "$STATE/pre-number" 2>/dev/null) || { say "falta $STATE/pre-number (ejecuta S0)"; return 1; }
    n=$(sudo snapper -c root create -t post --pre-number "$pre" -d "maintenance: after" -p) || return 1
    echo "$n" >"$STATE/post-number"
    say "snapshot posterior (post) = #$n (pre #$pre)"
}
do_1() {
    local dev
    if [ "$(root_discard)" -eq 0 ]; then
        say ""; say ">>> Ahora escribe la frase de cifrado del disco (LUKS) cuando la pida cryptsetup. <<<"
        rt cryptsetup refresh --allow-discards --persistent root || return 1
    fi
    if [ "$DRY" -eq 0 ]; then
        dev=$(sudo cryptsetup status root 2>/dev/null | awk '/^ *device:/ {print $2}')
        [ -n "$dev" ] && say "luksDump ($dev): $(sudo cryptsetup luksDump "$dev" 2>/dev/null | grep -E '^Flags:' || echo 'Flags: (sin linea)')"
        say "discard_max_bytes ahora = $(root_discard)"
    fi
    rt systemctl enable --now fstrim.timer || return 1
    rt fstrim -v / || return 1
}
do_2() {
    if ! grep -q "^PACCACHE_ARGS='-k3'" /etc/conf.d/pacman-contrib 2>/dev/null; then
        rt cp -a /etc/conf.d/pacman-contrib "/etc/conf.d/pacman-contrib.bak.$DATE" || return 1
        rt sed -i "s/^PACCACHE_ARGS=.*/PACCACHE_ARGS='-k3'/" /etc/conf.d/pacman-contrib || return 1
    fi
    rt systemctl enable --now paccache.timer || return 1
    rt paccache -rk3 || return 1
    rt paccache -ruk0 || return 1
}
do_3() { rt systemctl enable --now 'btrfs-scrub@-.timer'; }
# smartd directive sets, most complete first; the first one smartd accepts wins.
SMART_SETS=(
    "-a -n standby,q -W 0,70,80 -m <nomailer> -M exec $NOTIFY_DST"
    "-a -W 0,70,80 -m <nomailer> -M exec $NOTIFY_DST"
    "-a -m <nomailer> -M exec $NOTIFY_DST"
)
do_4() {
    local set tmp out rc ok=0
    rt pacman -S --needed --noconfirm smartmontools || return 1
    rt install -D -m 755 "$HERE/smart-notify" "$NOTIFY_DST" || return 1
    if [ "$DRY" -eq 1 ]; then
        say "    [dry] respaldar $SMARTD_CONF y escribir 'DEVICESCAN ${SMART_SETS[0]}' (validar con smartd -q onecheck)"
        say "    [dry] sudo systemctl enable --now smartd"; return 0
    fi
    if [ -f "$SMARTD_CONF" ] && ! grep -qE "^[^#]*-M exec $NOTIFY_DST" "$SMARTD_CONF"; then
        sudo cp -a "$SMARTD_CONF" "$SMARTD_CONF.bak.$DATE" || return 1
    fi
    tmp=$(mktemp)
    for set in "${SMART_SETS[@]}"; do
        printf '# Managed by omarchy maintenance (contrib/omarchy/maintenance)\nDEVICESCAN %s\n' "$set" >"$tmp"
        sudo install -m 644 "$tmp" "$SMARTD_CONF" || return 1
        out=$(sudo smartd -q onecheck 2>&1); rc=$?
        say "smartd -q onecheck con 'DEVICESCAN $set': rc=$rc"
        printf '%s\n' "$out" | sed 's/^/    | /'
        if [ $rc -eq 0 ] && ! printf '%s\n' "$out" | grep -Eiq 'unable to parse|invalid|unknown (option|directive)|problem creating'; then ok=1; break; fi
        say "directivas rechazadas por smartd; probando un conjunto menor"
    done
    rm -f "$tmp"
    [ $ok -eq 1 ] || return 1
    rt systemctl enable --now smartd || return 1
    rt systemctl restart smartd
}
do_5() {
    local o cur list=()
    cur=" $(orphans_now | tr '\n' ' ') "
    for o in "${ORPHANS5[@]}"; do [[ "$cur" == *" $o "* ]] && list+=("$o"); done
    [ ${#list[@]} -gt 0 ] || { say "ya no son huerfanos"; return 0; }
    rt pacman -Rns --noconfirm "${list[@]}"
}
do_6() {
    rt pacman -S --needed --noconfirm "${PKGS6[@]}" || return 1
    [ "$DRY" -eq 1 ] || { say "vainfo:"; vainfo 2>&1 | head -n 12 | sed 's/^/    | /'; }
    return 0
}

main() {
    local s fails=0 desc
    if [ "$DRY" -eq 0 ]; then
        mkdir -p "$STATE"
        say ">>> Escribe tu contrasena de sudo. <<<"
        sudo -v || return 1
        ( while true; do sleep 50; sudo -n -v 2>/dev/null || exit; done ) >/dev/null 2>&1 & KEEP=$!
        trap 'kill $KEEP 2>/dev/null' EXIT
    fi
    say "Mantenimiento ($([ $DRY -eq 1 ] && echo dry-run || echo real)) $(date '+%F %T'): pasos ${SEL[*]}"
    for s in "${SEL[@]}"; do
        say ""; say "== Paso $s =="
        if "chk_$s"; then say "STEP $s skip (ya hecho)"; continue; fi
        if [ "$DRY" -eq 1 ]; then say "STEP $s would-do"; "do_$s"; continue; fi
        if "do_$s" && "chk_$s"; then say "STEP $s done"
        else say "STEP $s fail"; fails=$((fails + 1)); fi
    done
    say ""; say "pasos fallidos: $fails"
    return $((fails > 0 ? 1 : 0))
}

if [ "$DRY" -eq 1 ]; then main; exit $?; fi
mkdir -p "$STATE"
LOG="$STATE/apply-$DATE.log"
main 2>&1 | tee -a "$LOG"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$LOG"
[ "$WAIT" -eq 1 ] && read -rp "Enter para cerrar"
exit "$rc"
