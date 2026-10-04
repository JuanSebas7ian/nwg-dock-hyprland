#!/usr/bin/env bash
# Read-only, root-less smoke test of the drivers module.
# One line per check: PASS|FAIL|WARN|SKIP <id> <text>; --json for machine reading. --only ID,ID runs just those.
# Env: HOOK_ETC (default /etc/pacman.d/hooks), HOOK_LIB (default /usr/local/lib/omarchy); the rest as install.sh.
set -uo pipefail
export LC_ALL=C
HERE=$(cd "$(dirname "$0")" && pwd)
HOOK_ETC=${HOOK_ETC:-/etc/pacman.d/hooks}; HOOK_LIB=${HOOK_LIB:-/usr/local/lib/omarchy}
COMPAT_CMD=${COMPAT_CMD:-python3 $HERE/compat.py post}
JSON=0; ONLY=""
while [ $# -gt 0 ]; do
    case "$1" in
        --json) JSON=1 ;;
        --only) ONLY=",$2,"; shift ;;
        -h|--help) sed -n '2,5p' "$0"; exit 0 ;;
        *) echo "uso: $0 [--json] [--only ID,ID]" >&2; exit 3 ;;
    esac
    shift
done
LINES=(); HAS_FAIL=0
want() { [ -z "$ONLY" ] || [[ "$ONLY" == *",$1,"* ]]; }
emit() { LINES+=("$1"$'\t'"$2"$'\t'"$3"); [ "$1" = FAIL ] && HAS_FAIL=1; }

if want INST01 || want ETC01; then
    chk=$("$HERE/install.sh" --check 2>&1); rc=$?
    etc=$(grep '^DIFIERE' <<<"$chk")
    other=$(grep -E '^(FALTA|DIFIERE)' <<<"$chk" | grep -v '^DIFIERE')
    if want INST01; then
        if [ -z "$other" ]; then emit PASS INST01 "install.sh --check: paquetes, servicios y DKMS coinciden"
        else emit FAIL INST01 "install.sh --check: $(head -n1 <<<"$other") (+$(( $(wc -l <<<"$other") - 1 )) más)"; fi
    fi
    if want ETC01; then
        if [ -z "$etc" ]; then emit PASS ETC01 "los archivos de etc/ del repo coinciden con el sistema"
        else emit FAIL ETC01 "$(head -n1 <<<"$etc")"; fi
    fi
fi
if want COMPAT01; then
    # shellcheck disable=SC2086
    out=$($COMPAT_CMD 2>&1); rc=$?
    if [ "$rc" -ge 2 ]; then emit FAIL COMPAT01 "compat.py post: $(grep -m1 '^FAIL' <<<"$out")"
    elif [ "$rc" -eq 1 ]; then emit WARN COMPAT01 "compat.py post con avisos: $(grep -m1 '^WARN' <<<"$out")"
    else emit PASS COMPAT01 "compat.py post sin fallos"; fi
fi
if want HOOK01; then
    bad=""
    [ -f "$HOOK_ETC/90-omarchy-compat.hook" ] && cmp -s "$HERE/pacman-hook/90-omarchy-compat.hook" "$HOOK_ETC/90-omarchy-compat.hook" || bad="$bad hook"
    [ -x "$HOOK_LIB/compat-hook" ] && cmp -s "$HERE/pacman-hook/compat-hook" "$HOOK_LIB/compat-hook" || bad="$bad compat-hook"
    [ -x "$HOOK_LIB/compat.py" ] && cmp -s "$HERE/compat.py" "$HOOK_LIB/compat.py" || bad="$bad compat.py"
    if [ -z "$bad" ]; then emit PASS HOOK01 "gancho de pacman instalado, ejecutable y al día"
    else emit WARN HOOK01 "gancho de pacman ausente o desactualizado:$bad (install.sh --hook-only)"; fi
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
