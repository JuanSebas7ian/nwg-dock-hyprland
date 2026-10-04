#!/usr/bin/env bash
# (Re)install the driver stack described by manifest.json. Idempotent: only acts on what is missing or differs.
# Usage: install.sh [--check | --dry-run | --hook-only] [--groups g1,g2] [--wait]
#   --check      no root; list what is missing or differs; exit 0 only if nothing is missing
#   --dry-run    no root; print what a real run would do (exit 0)
#   --hook-only  only the pacman hook (/etc/pacman.d/hooks + /usr/local/lib/omarchy)
#   --groups     restrict the package step to some manifest groups (etc/services/DKMS are skipped)
#   --wait       ask for Enter before closing (visible terminal)
# A real run needs sudo: snapshot pre, packages, etc files (backup .bak.<date> only if they differ), DKMS,
# limine-update / mkinitcpio only if their inputs changed, services, snapshot post, compat.py post.
# Env (tests): ROOT (prefix for destination paths), MANIFEST, ETC_DIR, KERNEL_RELEASE, OMARCHY_DRIVERS_STATE, COMPAT_CMD.
# Log: ~/.local/state/omarchy-drivers/install-<date>.log, ends with exit=N.
set -uo pipefail
export LC_ALL=C

HERE=$(cd "$(dirname "$0")" && pwd)
MANIFEST=${MANIFEST:-$HERE/manifest.json}
ROOT=${ROOT:-}
STATE=${OMARCHY_DRIVERS_STATE:-$HOME/.local/state/omarchy-drivers}
KERNEL=${KERNEL_RELEASE:-$(uname -r)}
COMPAT_CMD=${COMPAT_CMD:-python3 $HERE/compat.py post}
DATE=$(date +%Y%m%d-%H%M%S)
MODE=real; WAIT=0; GROUPS_SEL=""; HOOK_ONLY=0

usage() { echo "uso: $0 [--check|--dry-run|--hook-only] [--groups g1,g2] [--wait]" >&2; exit 3; }
while [ $# -gt 0 ]; do
    case "$1" in
        --check) MODE=check ;;
        --dry-run) MODE=dry ;;
        --hook-only) HOOK_ONLY=1 ;;
        --groups) [ $# -ge 2 ] || usage; GROUPS_SEL=$2; shift ;;
        --wait) WAIT=1 ;;
        -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
        *) usage ;;
    esac
    shift
done

say() { printf '%s\n' "$*"; }
# ETC_DIR defaults next to the manifest so a fake MANIFEST brings its own etc/.
ETC_DIR=${ETC_DIR:-$(dirname "$MANIFEST")/etc}
MAN_DIR=$(dirname "$MANIFEST")

# Flatten the manifest into TSV: KIND<TAB>fields...
flat() {
    GROUPS_SEL=$GROUPS_SEL python3 - "$MANIFEST" <<'PY'
import json, os, sys
m = json.load(open(sys.argv[1]))
sel = [g for g in os.environ["GROUPS_SEL"].split(",") if g]
for g in sorted(m["groups"]):
    if sel and g not in sel:
        continue
    for kind in ("repo", "aur"):
        for p in m["groups"][g].get(kind, []):
            print("PKG", g, kind, p, sep="\t")
if not sel:
    for e in m["etc_files"]:
        print("ETC", e["dest"], e["src"], e.get("mode", "644"), sep="\t")
    for k in ("system", "user"):
        for u in m["services"].get(k, []):
            print("SVC", k, u, sep="\t")
    for d in m.get("dkms", []):
        print("DKMS", d, sep="\t")
    for p in m.get("omarchy_owned", []):
        print("OWNED", p, sep="\t")
for h in m.get("hook", []):
    print("HOOK", h["src"], h["dest"], h["mode"], sep="\t")
PY
}

MISS_REPO=(); MISS_AUR=(); DIFF_ETC=(); BAD_SVC=(); BAD_DKMS=(); MISS_OWNED=(); DIFF_HOOK=()
declare -A GRP_REPO GRP_AUR

file_state() { # dest src mode -> prints "ok" or reason
    local d=$ROOT$1 s=$2 mode=$3
    [ -e "$d" ] || { echo missing; return; }
    cmp -s "$s" "$d" || { echo differs; return; }
    [ "$(stat -c %a "$d")" = "$mode" ] || { echo mode; return; }
    echo ok
}

collect() {
    local kind a b c d st dk_out
    dk_out=$(dkms status 2>/dev/null)
    while IFS=$'\t' read -r kind a b c d; do
        case "$kind" in
            PKG) [ "$HOOK_ONLY" -eq 1 ] && continue
                 pacman -Q "$c" >/dev/null 2>&1 && continue
                 if [ "$b" = aur ]; then MISS_AUR+=("$c"); GRP_AUR[$a]+="$c "; else MISS_REPO+=("$c"); GRP_REPO[$a]+="$c "; fi ;;
            ETC) [ "$HOOK_ONLY" -eq 1 ] && continue
                 st=$(file_state "$a" "$MAN_DIR/$b" "$c"); [ "$st" = ok ] || DIFF_ETC+=("$a|$b|$c|$st") ;;
            SVC) [ "$HOOK_ONLY" -eq 1 ] && continue
                 if [ "$a" = user ]; then st=$(systemctl --user is-enabled "$b" 2>/dev/null); else st=$(systemctl is-enabled "$b" 2>/dev/null); fi
                 [ "$st" = enabled ] || BAD_SVC+=("$a|$b") ;;
            DKMS) [ "$HOOK_ONLY" -eq 1 ] && continue
                  [[ "$dk_out" =~ (^|$'\n')"$a"/[^,]*,\ "$KERNEL",[^:]*:\ installed ]] || BAD_DKMS+=("$a") ;;
            OWNED) [ "$HOOK_ONLY" -eq 1 ] && continue
                   [ -e "$ROOT$a" ] || MISS_OWNED+=("$a") ;;
            HOOK) st=$(file_state "$b" "$MAN_DIR/$a" "$c"); [ "$st" = ok ] || DIFF_HOOK+=("$b|$a|$c|$st") ;;
        esac
    done < <(flat)
}

problems() { echo $(( ${#MISS_REPO[@]} + ${#MISS_AUR[@]} + ${#DIFF_ETC[@]} + ${#BAD_SVC[@]} + ${#BAD_DKMS[@]} + ${#MISS_OWNED[@]} )); }

report() {
    local x
    for x in "${MISS_REPO[@]}"; do say "FALTA paquete (repo): $x"; done
    for x in "${MISS_AUR[@]}"; do say "FALTA paquete (AUR): $x"; done
    for x in "${DIFF_ETC[@]}"; do IFS='|' read -r d s m st <<<"$x"; say "DIFIERE archivo: $d ($st)"; done
    for x in "${BAD_SVC[@]}"; do IFS='|' read -r k u <<<"$x"; say "FALTA servicio habilitado ($k): $u"; done
    for x in "${BAD_DKMS[@]}"; do say "FALTA módulo DKMS instalado para $KERNEL: $x"; done
    for x in "${MISS_OWNED[@]}"; do say "FALTA archivo de Omarchy: $x"; done
    if [ "${#DIFF_HOOK[@]}" -gt 0 ]; then say "INFO gancho de pacman no instalado o desactualizado (install.sh --hook-only)"; fi
}

# Root command: printed in dry-run, run through sudo otherwise.
rt() { if [ "$MODE" = dry ]; then say "    [dry] sudo $*"; return 0; fi; sudo "$@"; }

snapshot() { # pre|post
    local n
    if [ "$MODE" = dry ]; then say "    [dry] sudo snapper -c root create -t $1 ... -d \"drivers: $1\""; return 0; fi
    if [ "$1" = pre ]; then
        n=$(sudo snapper -c root create -t pre -p -c number -d "drivers: before") || return 1
        mkdir -p "$STATE"; echo "$n" >"$STATE/pre-number"; say "snapshot previo (pre) = #$n"
    else
        local pre; pre=$(cat "$STATE/pre-number" 2>/dev/null) || { say "falta pre-number"; return 1; }
        n=$(sudo snapper -c root create -t post --pre-number "$pre" -c number -d "drivers: after" -p) || return 1
        say "snapshot posterior (post) = #$n (pre #$pre)"
    fi
}

install_file() { # dest src mode ; returns 0 and sets CHANGED_<kind> flags
    local dest=$1 src=$2 mode=$3
    if [ -e "$ROOT$dest" ] && ! cmp -s "$src" "$ROOT$dest"; then
        rt cp -a "$ROOT$dest" "$ROOT$dest.bak.$DATE" || return 1
    fi
    rt install -D -m "$mode" "$src" "$ROOT$dest" || return 1
    [ "$MODE" = dry ] || say "  instalado $dest"
}

apply() {
    local fails=0 x g pk d s m st k u aurh="" rc
    local c_limine=0 c_initcpio=0 c_systemd=0
    if [ "$MODE" != dry ]; then
        say ">>> Escribe tu contraseña de sudo. <<<"
        sudo -v || return 1
        ( while true; do sleep 50; sudo -n -v 2>/dev/null || exit; done ) >/dev/null 2>&1 & KEEP=$!
        trap 'kill $KEEP 2>/dev/null' RETURN
    fi
    snapshot pre || return 1
    for g in $(printf '%s\n' "${!GRP_REPO[@]}" | sort); do
        say "== paquetes del grupo $g =="
        # shellcheck disable=SC2086
        rt pacman -S --needed --noconfirm ${GRP_REPO[$g]} || fails=$((fails + 1))
    done
    if [ "${#MISS_AUR[@]}" -gt 0 ]; then
        say "== paquetes de AUR =="
        if command -v omarchy >/dev/null 2>&1 && [[ "$(omarchy pkg 2>&1)" == *"aur add"* ]]; then aurh="omarchy pkg aur add"
        elif command -v yay >/dev/null 2>&1; then aurh="yay -S --needed --noconfirm"
        elif command -v paru >/dev/null 2>&1; then aurh="paru -S --needed --noconfirm"; fi
        if [ -z "$aurh" ]; then say "sin ayudante de AUR (omarchy pkg aur / yay / paru): ${MISS_AUR[*]}"; fails=$((fails + 1))
        elif [ "$MODE" = dry ]; then say "    [dry] $aurh ${MISS_AUR[*]}"
        else $aurh "${MISS_AUR[@]}" || fails=$((fails + 1)); fi
    fi
    for x in "${DIFF_ETC[@]}"; do
        IFS='|' read -r d s m st <<<"$x"
        install_file "$d" "$MAN_DIR/$s" "$m" || { fails=$((fails + 1)); continue; }
        case "$d" in
            /etc/limine-entry-tool.d/*) c_limine=1 ;;
            /etc/mkinitcpio.conf.d/*|/etc/modprobe.d/*) c_initcpio=1 ;;
            /etc/systemd/*|/usr/local/lib/bt-guardian/*) c_systemd=1 ;;
        esac
    done
    for x in "${DIFF_HOOK[@]}"; do
        IFS='|' read -r d s m st <<<"$x"; install_file "$d" "$MAN_DIR/$s" "$m" || fails=$((fails + 1))
    done
    [ "$c_systemd" -eq 1 ] && { rt systemctl daemon-reload || fails=$((fails + 1)); }
    if [ "${#BAD_DKMS[@]}" -gt 0 ]; then rt dkms autoinstall -k "$KERNEL" || fails=$((fails + 1)); fi
    [ "$c_limine" -eq 1 ] && { rt limine-update || fails=$((fails + 1)); }
    [ "$c_initcpio" -eq 1 ] && { rt mkinitcpio -P || fails=$((fails + 1)); }
    for x in "${BAD_SVC[@]}"; do
        IFS='|' read -r k u <<<"$x"
        if [ "$k" = user ]; then [ "$MODE" = dry ] && say "    [dry] systemctl --user enable --now $u" || systemctl --user enable --now "$u" || fails=$((fails + 1))
        elif [[ "$u" == *-resume.service ]]; then rt systemctl enable "$u" || fails=$((fails + 1))
        else rt systemctl enable --now "$u" || fails=$((fails + 1)); fi
    done
    snapshot post || fails=$((fails + 1))
    if [ "$MODE" != dry ]; then
        say "== compat post =="
        # shellcheck disable=SC2086
        $COMPAT_CMD; rc=$?
        [ "$rc" -ge 2 ] && { say "compat post: algo no funciona (rc=$rc)"; fails=$((fails + 1)); }
    fi
    say "pasos fallidos: $fails"
    return $((fails > 0 ? 1 : 0))
}

main() {
    local n
    collect
    n=$(problems)
    case "$MODE" in
        check)
            report
            if [ "$n" -eq 0 ]; then say "OK: el sistema coincide con el manifiesto"; return 0; fi
            say "$n diferencias con el manifiesto"; return 1 ;;
        dry)
            report
            if [ "$n" -eq 0 ] && [ "${#DIFF_HOOK[@]}" -eq 0 ]; then say "nada que hacer"; return 0; fi
            if [ "$HOOK_ONLY" -eq 1 ] && [ "${#DIFF_HOOK[@]}" -eq 0 ]; then say "nada que hacer (gancho al día)"; return 0; fi
            say "haría:"; apply ;;
        real)
            if [ "$HOOK_ONLY" -eq 1 ] && [ "${#DIFF_HOOK[@]}" -eq 0 ]; then say "nada que hacer (gancho al día)"; return 0; fi
            if [ "$n" -eq 0 ] && [ "${#DIFF_HOOK[@]}" -eq 0 ]; then say "nada que hacer"; return 0; fi
            report; apply ;;
    esac
}

if [ "$MODE" != real ]; then main; exit $?; fi
mkdir -p "$STATE"
LOG="$STATE/install-$DATE.log"
main 2>&1 | tee -a "$LOG"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$LOG"
[ "$WAIT" -eq 1 ] && read -rp "Enter para cerrar"
exit "$rc"
