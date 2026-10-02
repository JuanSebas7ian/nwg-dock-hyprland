#!/usr/bin/env bash
# Instala omarchy-hwcheck para el usuario actual (no necesita sudo). Idempotente.
#
#   contrib/omarchy/hwcheck/install.sh            instala o actualiza
#   contrib/omarchy/hwcheck/install.sh --remove   lo quita (deja los informes y la línea base)
#
# Instala:
#   ~/.local/bin/omarchy-hwcheck
#   ~/.config/omarchy/hooks/post-update.d/omarchy-hwcheck.hook   (valida tras `omarchy update`)
#   ~/.config/omarchy/hooks/post-boot.d/omarchy-hwcheck.hook     (valida en cada arranque)
# y crea la línea base en ~/.local/state/omarchy-hwcheck/baseline.json si no existe. Lo que reemplaza queda
# respaldado en ~/.local/state/omarchy-hwcheck/backups/<fecha>/.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
BIN=$HOME/.local/bin/omarchy-hwcheck
HOOKS=$HOME/.config/omarchy/hooks
EVENTS=(post-update post-boot)
STAMP=$(date +%Y%m%d-%H%M%S)
BACKUPS=${XDG_STATE_HOME:-$HOME/.local/state}/omarchy-hwcheck/backups

say() { printf '==> %s\n' "$*"; }
fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

# Copia src en dst; si dst existía con otro contenido, lo respalda antes.
put() {
  local src=$1 dst=$2
  mkdir -p "$(dirname "$dst")"
  if [[ -e $dst ]]; then
    if cmp -s "$src" "$dst"; then
      say "  sin cambios: $dst"
      return
    fi
    # Fuera de las carpetas de hooks: omarchy-hook ejecuta todo lo que haya en ellas, también un .bak
    local bak=$BACKUPS/$STAMP${dst#"$HOME"}
    mkdir -p "$(dirname "$bak")"
    cp -a "$dst" "$bak"
    say "  respaldo: $bak"
  fi
  install -m 755 "$src" "$dst"
  say "  instalado: $dst"
}

if [[ ${1:-} == --remove ]]; then
  rm -f "$BIN"
  for ev in "${EVENTS[@]}"; do rm -f "$HOOKS/$ev.d/omarchy-hwcheck.hook"; done
  say "omarchy-hwcheck quitado. Los informes y la línea base siguen en ~/.local/state/omarchy-hwcheck/"
  exit 0
fi
[[ -z ${1:-} ]] || fail "opción desconocida: $1 (usa --remove o nada)"

command -v python3 > /dev/null || fail "falta python3"

say "Pruebas unitarias"
out=$(cd "$HERE" && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest test_hwcheck 2>&1) || {
  printf '%s\n' "$out" >&2
  fail "las pruebas fallan: no instalo"
}
printf '%s\n' "$out" | tail -1

say "Instalando"
put "$HERE/hwcheck.py" "$BIN"
for ev in "${EVENTS[@]}"; do
  put "$HERE/omarchy-hwcheck.hook" "$HOOKS/$ev.d/omarchy-hwcheck.hook"
done

command -v checkupdates > /dev/null ||
  say "  (opcional) para \`omarchy-hwcheck --online\`: omarchy pkg add pacman-contrib"

say "Línea base"
"$BIN" baseline || true # si ya existe, la deja como está

say "Primera validación"
rc=0
"$BIN" scan || rc=$?
echo
case $rc in
  0) say "Todo bien." ;;
  1) say "Hay avisos (arriba). No impiden usar el equipo." ;;
  *) say "Hay FALLOS (arriba): revísalos." ;;
esac
say "Listo. Uso: omarchy-hwcheck [scan|diff|baseline|state] [--online] [--json]"
