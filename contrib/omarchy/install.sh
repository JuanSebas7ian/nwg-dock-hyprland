#!/usr/bin/env bash
# Instala el dock -dnd en Omarchy (configuración de Hyprland en Lua).
# Uso, desde la raíz del repositorio:
#   contrib/omarchy/install.sh           # instala; el dock arranca en el próximo inicio de sesión
#   contrib/omarchy/install.sh --start   # instala y (re)lanza el dock ya mismo
# Se puede volver a ejecutar para actualizar: no duplica nada y respalda lo que reemplaza
# como <archivo>.bak.<fecha>.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
HERE=$REPO/contrib/omarchy
CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}
HYPR=$CONFIG/hypr
DOCK_CONFIG=$CONFIG/nwg-dock-hyprland
BIN=$HOME/.local/bin/nwg-dock-hyprland-dnd
STAMP=$(date +%Y%m%d-%H%M%S)
START=false
[[ ${1:-} == --start ]] && START=true

say() { printf '==> %s\n' "$*"; }

# Avisos inofensivos de cgo que imprime la dependencia gotk4 al compilar
CGO_NOISE='conflicting types for built-in function|note: .free. is declared|^# github.com/diamondburned'
quiet_go() { "$@" 2> >(grep -vE "$CGO_NOISE" >&2); }
fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

backup() {
  [[ -e $1 ]] && cp -a "$1" "$1.bak.$STAMP" && say "  respaldo: $1.bak.$STAMP"
  return 0
}

# Copia src a dst; si dst existe y es distinto, lo respalda antes
install_file() {
  local src=$1 dst=$2 mode=$3
  if [[ -e $dst ]] && cmp -s "$src" "$dst"; then
    say "  sin cambios: $dst"
    return
  fi
  backup "$dst"
  install -D -m "$mode" "$src" "$dst"
  say "  instalado: $dst"
}

# Agrega al final de `file` la línea de hypr-snippets.lua que empieza con `prefix`, si no está ya
add_snippet() {
  local file=$1 prefix=$2 line
  line=$(grep -m1 -F "$prefix" "$HERE/hypr-snippets.lua")
  [[ -n $line ]] || fail "no encuentro '$prefix' en hypr-snippets.lua"
  touch "$file"
  if grep -qF "$line" "$file"; then
    say "  ya estaba: $(basename "$file")"
    return
  fi
  backup "$file"
  printf '\n-- nwg-dock-hyprland -dnd (contrib/omarchy/install.sh)\n%s\n' "$line" >> "$file"
  say "  agregado a $(basename "$file"): $line"
}

say "Verificando requisitos"
if $START && [[ -z ${HYPRLAND_INSTANCE_SIGNATURE:-} ]]; then
  fail "--start necesita una sesión de Hyprland"
fi
command -v go > /dev/null || fail "falta Go: omarchy pkg add go"
[[ -d /usr/share/nwg-dock-hyprland/images ]] ||
  fail "falta el paquete nwg-dock-hyprland (aporta los íconos y queda de respaldo): omarchy pkg add nwg-dock-hyprland"
pkg-config --exists gtk+-3.0 gtk-layer-shell-0 ||
  fail "faltan gtk3 o gtk-layer-shell: omarchy pkg add gtk3 gtk-layer-shell"
[[ -f $HYPR/hyprland.lua ]] ||
  fail "no encuentro $HYPR/hyprland.lua: este instalador es para Omarchy con configuración en Lua"

say "Pruebas unitarias"
(cd "$REPO" && quiet_go go test ./... > /dev/null) || fail "las pruebas fallaron (ver: go test ./...)"

say "Compilando (la primera vez puede tardar varios minutos)"
mkdir -p "$(dirname "$BIN")"
(cd "$REPO" && quiet_go go build -o "$BIN.new" .)
# sin tubería a grep -q: cortaría la salida y, con pipefail, daría un falso error
help=$("$BIN.new" -h 2>&1 || true)
[[ $help == *"-dnd"* ]] || fail "el binario compilado no tiene la opción -dnd"
mv -f "$BIN.new" "$BIN"
say "  instalado: $BIN"

say "Archivos de configuración"
install_file "$HERE/launch-dock.sh" "$HYPR/scripts/launch-dock.sh" 755
install_file "$HERE/style.css" "$DOCK_CONFIG/style.css" 644

say "Hyprland"
add_snippet "$HYPR/autostart.lua" 'o.launch_on_start('
add_snippet "$HYPR/looknfeel.lua" 'hl.layer_rule('
add_snippet "$HYPR/bindings.lua" 'o.bind("SUPER + CTRL + SHIFT + D"'

if [[ -n ${HYPRLAND_INSTANCE_SIGNATURE:-} ]] && command -v hyprctl > /dev/null; then
  hyprctl reload > /dev/null
  errors=$(hyprctl configerrors | grep -v '^\s*$' || true)
  [[ -z $errors ]] || fail "Hyprland reporta errores de configuración:
$errors
Restaura los respaldos *.bak.$STAMP de $HYPR si hace falta."
  say "  configuración recargada, sin errores"
else
  say "  Hyprland no está corriendo: se aplicará al iniciar sesión"
fi

if $START; then
  say "Lanzando el dock"
  pkill -f 'bash .*/launch-dock\.sh$' 2> /dev/null || true
  pkill -x nwg-dock-hyprla 2> /dev/null || true
  sleep 1
  # el mensaje "expected a dispatcher" es normal: el comando sí se ejecuta
  hyprctl dispatch "hl.exec_cmd(\"uwsm-app -- $HYPR/scripts/launch-dock.sh\")" > /dev/null 2>&1 || true
  sleep 3
  pgrep -x nwg-dock-hyprla > /dev/null && say "  dock corriendo" ||
    fail "el dock no arrancó; revisa ~/.local/state/nwg-dock/dock.log"
fi

say "Listo. Clic derecho en un ícono → \"Add app…\" para anclar apps; arrastra para reordenar."
$START || say "Para lanzarlo ya: contrib/omarchy/install.sh --start (o cierra sesión y vuelve a entrar)"
