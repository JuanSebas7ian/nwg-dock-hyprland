#!/usr/bin/env bash
# Instala los extras de la barra de Omarchy: los widgets Spotify, Nube (Google Drive, iCloud Drive,
# Google Fotos y Dropbox en pestañas), Ollama y Hardware (resumen, CPU, GPU, discos, drivers y
# periféricos en pestañas) y VPN (Surfshark + Proton VPN Free), más los colectores de uso de Antigravity y opencode para el panel de agentes.
#
# Uso, desde la raíz del repositorio:
#   contrib/omarchy/bar/install.sh            # instala o actualiza (idempotente)
#   contrib/omarchy/bar/install.sh --host     # además los ajustes de ESTE equipo (WirePlumber, Ollama 32k: pide sudo)
#   contrib/omarchy/bar/install.sh --remove   # quita todo lo que instala
#
# Nunca toca /usr/share/omarchy. Lo que reemplaza queda en
# ~/.local/state/omarchy-bar-extras/backups/<fecha>/ (no junto a los plugins:
# la barra cargaría las copias como plugins).
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}
PLUGINS=$CONFIG/omarchy/plugins
UNITS=$CONFIG/systemd/user
BIN=$HOME/.local/bin
STAMP=$(date +%Y%m%d-%H%M%S)
BACKUPS=${XDG_STATE_HOME:-$HOME/.local/state}/omarchy-bar-extras/backups/$STAMP
RCLONE_REMOTE=${RCLONE_REMOTE:-rclone:}

# id del plugin y widget junto al que se coloca en la sección derecha
WIDGETS=(
  "juansebas7ian.spotify:omarchy.tray"
  "juansebas7ian.cloud:juansebas7ian.spotify"
  "juansebas7ian.ollama:omarchy.agents"
  "juansebas7ian.hardware:juansebas7ian.ollama"
  "juansebas7ian.vpn:omarchy.network"
)
# Clones de widgets de Omarchy: al activarse ocupan el lugar del original con sus mismos ajustes
# (omarchy plugin disable <clon> devuelve el original). juansebas7ian.clock = reloj + Google Calendar.
CLONES=(juansebas7ian.clock)
# Widgets que Nube y Hardware reemplazaron (2026-10-07): se quitan al instalar, con respaldo.
RETIRED=(
  juansebas7ian.gdrive juansebas7ian.icloud juansebas7ian.gphotos
  juansebas7ian.sysmon juansebas7ian.cooling juansebas7ian.storage juansebas7ian.nvidia juansebas7ian.drivers
)
COLLECTORS=(omarchy-agent-usage-antigravity omarchy-agent-usage-opencode omarchy-agent-usage-extra omarchy-session omarchy-freeze-guard omarchy-memguard bt-pair-keyboard spotify-bar-setup icloud-setup gphotos-sync gphotos-setup opencode-ollama-sync omarchy-vpn omarchy-gcal)

say() { printf '==> %s\n' "$*"; }
warn() { printf 'AVISO: %s\n' "$*" >&2; }
fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

backup() {
  [[ -e $1 ]] || return 0
  mkdir -p "$BACKUPS"
  cp -a "$1" "$BACKUPS/"
  say "  respaldo: $BACKUPS/$(basename "$1")"
}

install_file() {
  local src=$1 dst=$2 mode=$3
  if [[ -e $dst ]] && cmp -s "$src" "$dst"; then
    return 1
  fi
  backup "$dst"
  install -D -m "$mode" "$src" "$dst"
  say "  instalado: $dst"
}

install_dir() {
  local src=$1 dst=$2
  if [[ -d $dst ]] && diff -rq -x __pycache__ -x .placed "$src" "$dst" >/dev/null 2>&1; then
    say "  sin cambios: $dst"
    return
  fi
  backup "$dst"
  mkdir -p "$dst"
  rsync -a --delete --exclude __pycache__ --exclude .placed "$src/" "$dst/"
  say "  instalado: $dst"
}

remote_exists() {
  local remotes
  remotes=$(rclone listremotes 2>/dev/null || true)
  [[ $'\n'$remotes$'\n' == *$'\n'$RCLONE_REMOTE$'\n'* ]]
}

remove_all() {
  say "Quitando los extras de la barra"
  for entry in "${WIDGETS[@]}" "${CLONES[@]/%/:}"; do
    id=${entry%%:*}
    omarchy plugin disable "$id" >/dev/null 2>&1 || true
    if [[ -d $PLUGINS/$id ]]; then
      backup "$PLUGINS/$id"
      rm -rf "${PLUGINS:?}/$id"
      say "  quitado: $id"
    fi
  done
  systemctl --user disable --now omarchy-gcal.timer >/dev/null 2>&1 || true
  systemctl --user disable --now omarchy-agent-usage-extra.timer omarchy-session.service omarchy-freeze-guard.service omarchy-memguard.service gphotos-sync.timer gphotos-sync.path opencode-ollama-sync.path opencode-ollama-sync.timer >/dev/null 2>&1 || true
  systemctl --user stop gphotos-sync.service >/dev/null 2>&1 || true
  for unit in omarchy-gcal.service omarchy-gcal.timer omarchy-agent-usage-extra.service omarchy-agent-usage-extra.timer omarchy-session.service omarchy-freeze-guard.service omarchy-memguard.service gphotos-sync.service gphotos-sync.timer gphotos-sync.path opencode-ollama-sync.service opencode-ollama-sync.path opencode-ollama-sync.timer; do
    [[ -e $UNITS/$unit ]] && backup "$UNITS/$unit" && rm -f "$UNITS/$unit"
  done
  for c in "${COLLECTORS[@]}"; do
    [[ -e $BIN/$c ]] && backup "$BIN/$c" && rm -f "$BIN/$c"
  done
  rm -f "${XDG_STATE_HOME:-$HOME/.local/state}"/omarchy/agents/usage/{antigravity,opencode}.json
  systemctl --user daemon-reload
  say "Los montajes de Drive e iCloud (rclone-gdrive, rclone-icloud, rclone-icloud-photos) y spotifyd se dejan como están."
  say "Para quitarlos: systemctl --user disable --now rclone-gdrive.service rclone-icloud.service rclone-icloud-photos.service spotifyd.service"
  omarchy restart shell >/dev/null 2>&1 || true
}

HOST=false
case ${1:-} in
--remove)
  remove_all
  exit 0
  ;;
--host) HOST=true ;;
"") ;;
*) fail "opción desconocida: $1 (usa --host o --remove)" ;;
esac

# --------------------------------------------------------------- requisitos
command -v omarchy >/dev/null || fail "esto es para Omarchy (no encuentro el comando omarchy)"
for dep in python3 jq rsync busctl; do
  command -v "$dep" >/dev/null || fail "falta $dep: omarchy pkg add $dep"
done
command -v checkupdates >/dev/null || warn "sin checkupdates, Hardware › Drivers no verá actualizaciones: omarchy pkg add pacman-contrib"
command -v omarchy-hwcheck >/dev/null || warn "sin omarchy-hwcheck, Hardware › Drivers no mostrará la salud: contrib/omarchy/hwcheck/install.sh"
command -v solaar >/dev/null || warn "sin solaar, Hardware › Devices no verá la batería de los Logitech: omarchy pkg add solaar"

# --------------------------------------------------------------- plugins
say "Widgets de la barra"
"$HERE/sync-ui.sh" # cada plugin lleva su copia de shared/ui (Omarchy no admite enlaces)
for id in "${RETIRED[@]}"; do
  if [[ -d $PLUGINS/$id ]]; then
    omarchy plugin disable "$id" >/dev/null 2>&1 || true
    backup "$PLUGINS/$id"
    rm -rf "${PLUGINS:?}/$id"
    say "  retirado (ahora es una pestaña de Nube o Hardware): $id"
  fi
done
# El Dropbox de Omarchy pasa a la pestaña Dropbox de Nube. Solo una vez: si lo
# vuelves a activar (omarchy plugin enable omarchy.dropbox), se respeta.
merged=${XDG_STATE_HOME:-$HOME/.local/state}/omarchy-bar-extras/dropbox-in-cloud
if [[ ! -e $merged ]]; then
  omarchy plugin disable omarchy.dropbox >/dev/null 2>&1 || true
  mkdir -p "$(dirname "$merged")" && touch "$merged"
  say "  omarchy.dropbox: ahora es la pestaña Dropbox de Nube (deshacer: omarchy plugin enable omarchy.dropbox)"
fi
for entry in "${WIDGETS[@]}" "${CLONES[@]/%/:}"; do
  id=${entry%%:*}
  omarchy plugin validate "$HERE/plugins/$id" >/dev/null || fail "manifiesto inválido: $id"
  install_dir "$HERE/plugins/$id" "$PLUGINS/$id"
done

# --------------------------------------------------------------- colectores
say "Colectores de uso (Antigravity, opencode)"
for c in "${COLLECTORS[@]}"; do
  install_file "$HERE/bin/$c" "$BIN/$c" 755 || say "  sin cambios: $BIN/$c"
done
for unit in omarchy-gcal.service omarchy-gcal.timer omarchy-agent-usage-extra.service omarchy-agent-usage-extra.timer omarchy-session.service omarchy-freeze-guard.service omarchy-memguard.service opencode-ollama-sync.service opencode-ollama-sync.path opencode-ollama-sync.timer; do
  install_file "$HERE/systemd/$unit" "$UNITS/$unit" 644 || say "  sin cambios: $UNITS/$unit"
done

# --------------------------------------------------------------- Google Drive
if command -v rclone >/dev/null && remote_exists; then
  say "Google Drive: montaje de $RCLONE_REMOTE en ~/GoogleDrive"
  install_file "$HERE/systemd/rclone-gdrive.service" "$UNITS/rclone-gdrive.service" 644 ||
    say "  sin cambios: $UNITS/rclone-gdrive.service"
else
  warn "sin el remoto rclone '$RCLONE_REMOTE': configúralo con 'rclone config' y vuelve a ejecutar (o RCLONE_REMOTE=nombre: $0)"
fi

# --------------------------------------------------------------- iCloud Drive
# The units are always installed (the widget offers "connect" when the remote
# is missing); the mount is only enabled once icloud-setup created "icloud:".
if command -v rclone >/dev/null; then
  say "iCloud Drive: montaje de icloud: en ~/iCloud (y Fotos en ~/iCloudPhotos, opcional)"
  for unit in rclone-icloud.service rclone-icloud-photos.service; do
    install_file "$HERE/systemd/$unit" "$UNITS/$unit" 644 || say "  sin cambios: $UNITS/$unit"
  done
fi

# --------------------------------------------------------------- Google Fotos
# Units always installed (the widget offers "connect"); timer and path are only
# enabled once gphotos-setup created "gphotos:".
if command -v rclone >/dev/null; then
  say "Google Fotos: subida con gphotos-sync (remoto gphotos:)"
  for unit in gphotos-sync.service gphotos-sync.timer gphotos-sync.path; do
    install_file "$HERE/systemd/$unit" "$UNITS/$unit" 644 || say "  sin cambios: $UNITS/$unit"
  done
  mkdir -p "$HOME/GoogleFotos"
fi

# --------------------------------------------------------------- Spotify
if command -v spotifyd >/dev/null; then
  say "Spotify: configuración de spotifyd"
  tmp=$(mktemp)
  sed "s|@HOME@|$HOME|" "$HERE/config/spotifyd/spotifyd.conf" >"$tmp"
  install_file "$tmp" "$CONFIG/spotifyd/spotifyd.conf" 644 || say "  sin cambios: $CONFIG/spotifyd/spotifyd.conf"
  rm -f "$tmp"
  mkdir -p "$HOME/.cache/spotifyd"
else
  warn "sin spotifyd: no se podrá reproducir en este PC sin la app. Instálalo con: omarchy pkg add spotifyd"
fi
if command -v spotify_player >/dev/null; then
  say "Spotify: spotify-player como cliente de la API (listas, dispositivos)"
  sp_conf=$CONFIG/spotify-player/app.toml
  tmp=$(mktemp)
  cp "$HERE/config/spotify-player/app.toml" "$tmp"
  # Keep the user's own client ID (set by spotify-bar-setup).
  if [[ -f $sp_conf ]] && id_line=$(grep -m1 -E '^\s*client_id\s*=' "$sp_conf"); then
    awk -v line="$id_line" 'BEGIN{d=0} /^\[/ && !d {print line; d=1} {print} END{if(!d) print line}' "$tmp" >"$tmp.2" && mv "$tmp.2" "$tmp"
  fi
  install_file "$tmp" "$sp_conf" 644 || say "  sin cambios: $sp_conf"
  rm -f "$tmp"
  install_file "$HERE/systemd/spotify-player.service" "$UNITS/spotify-player.service" 644 || say "  sin cambios: $UNITS/spotify-player.service"
else
  warn "sin spotify-player: el panel de Spotify no mostrará tus listas. Instálalo con: omarchy pkg add spotify-player"
fi

# --------------------------------------------------------------- opencode + Ollama
oc=$CONFIG/opencode/opencode.json
if command -v ollama >/dev/null && [[ -f $oc ]]; then
  if [[ $(jq -r '.provider.ollama // empty | type' "$oc") != object ]]; then
    say "opencode: proveedor de Ollama con los modelos instalados"
    backup "$oc"
    models=$(ollama list 2>/dev/null | awk 'NR > 1 && $1 !~ /embed/ {print $1}' |
      jq -R . | jq -s 'map({key: ., value: {name: (. + " (local)")}}) | from_entries')
    jq --argjson m "${models:-{\}}" '.provider.ollama = {npm: "@ai-sdk/openai-compatible", name: "Ollama (local)",
      options: {baseURL: "http://127.0.0.1:11434/v1"}, models: $m}' "$oc" >"$oc.tmp" && mv "$oc.tmp" "$oc"
  fi
fi

# --------------------------------------------------------------- ajustes del equipo
if $HOST; then
  say "Ajustes de este equipo"
  if lsusb -d 1d6c:0103 >/dev/null 2>&1; then
    install_file "$HERE/host/51-disable-nexigo-webcam-mic.conf" \
      "$CONFIG/wireplumber/wireplumber.conf.d/51-disable-nexigo-webcam-mic.conf" 644 &&
      systemctl --user restart wireplumber || true
  fi
  # Fan names measured on this PC (fan 2/5 = CPU cooler, fan 7 = AIO pump); never overwrite edits.
  [[ -e $CONFIG/omarchy-sysmon/fans.json ]] || install -D -m 644 "$HERE/host/fans.json" "$CONFIG/omarchy-sysmon/fans.json"
  if ! cmp -s "$HERE/host/claude-nct6775.conf" /etc/modules-load.d/claude-nct6775.conf 2>/dev/null; then
    say "  sensores de la placa: cargar nct6775 al arrancar (sudo)"
    sudo install -D -m 644 "$HERE/host/claude-nct6775.conf" /etc/modules-load.d/claude-nct6775.conf
    sudo modprobe nct6775 || true
  fi
  if command -v ollama >/dev/null; then
    dropin=/etc/systemd/system/ollama.service.d/context.conf
    if ! cmp -s "$HERE/host/ollama-context.conf" "$dropin" 2>/dev/null; then
      say "  Ollama a 32k de contexto (sudo)"
      sudo install -D -m 644 "$HERE/host/ollama-context.conf" "$dropin"
      sudo systemctl daemon-reload
      sudo systemctl restart ollama
    fi
  fi
fi

# --------------------------------------------------------------- activar
systemctl --user daemon-reload
systemctl --user enable --now omarchy-agent-usage-extra.timer >/dev/null
# opencode ve los modelos que Ollama tiene: al descargar/borrar uno y al iniciar sesión.
if [[ -d /var/lib/ollama/blobs ]]; then
  systemctl --user enable --now opencode-ollama-sync.path opencode-ollama-sync.timer >/dev/null || warn "opencode-ollama-sync no arrancó"
  "$BIN/opencode-ollama-sync" >/dev/null || true
fi
# Session restore: this session counts as already restored, so enabling it
# now does not reopen windows that are already open.
if ! systemctl --user is-active -q omarchy-session.service; then
  state=${XDG_STATE_HOME:-$HOME/.local/state}/omarchy-session
  mkdir -p "$state"
  [[ -n ${HYPRLAND_INSTANCE_SIGNATURE:-} ]] && printf '%s' "$HYPRLAND_INSTANCE_SIGNATURE" >"$state/restored-instance"
fi
systemctl --user enable --now omarchy-session.service >/dev/null
systemctl --user enable omarchy-freeze-guard.service >/dev/null
systemctl --user restart omarchy-freeze-guard.service >/dev/null || warn "omarchy-freeze-guard no arrancó: journalctl --user -u omarchy-freeze-guard"
systemctl --user enable omarchy-memguard.service >/dev/null
systemctl --user restart omarchy-memguard.service >/dev/null || warn "omarchy-memguard no arrancó: journalctl --user -u omarchy-memguard"
if [[ -e $UNITS/rclone-gdrive.service ]]; then
  systemctl --user enable --now rclone-gdrive.service >/dev/null || warn "el montaje de Drive no arrancó: journalctl --user -u rclone-gdrive"
fi
if [[ -e $UNITS/rclone-icloud.service ]] && RCLONE_REMOTE=icloud: remote_exists; then
  systemctl --user enable --now rclone-icloud.service >/dev/null || warn "el montaje de iCloud no arrancó: journalctl --user -u rclone-icloud (¿sesión caducada? icloud-setup reconnect)"
fi
if [[ -e $UNITS/gphotos-sync.timer ]] && RCLONE_REMOTE=gphotos: remote_exists; then
  systemctl --user enable --now gphotos-sync.timer gphotos-sync.path >/dev/null || warn "gphotos-sync no arrancó: journalctl --user -u gphotos-sync"
fi
if command -v spotifyd >/dev/null && compgen -G "$HOME/.cache/spotifyd/oauth/*" >/dev/null; then
  systemctl --user enable --now spotifyd.service >/dev/null || true
fi
if [[ -e $UNITS/spotify-player.service ]] && compgen -G "$HOME/.cache/spotify-player/*token.json" >/dev/null; then
  systemctl --user enable --now spotify-player.service >/dev/null || true
fi

say "Colocando los widgets en la barra"
omarchy-shell shell rescanPlugins >/dev/null 2>&1 || true
for entry in "${WIDGETS[@]}"; do
  id=${entry%%:*}
  after=${entry#*:}
  omarchy plugin enable "$id" >/dev/null
  # Solo se coloca la primera vez: si el usuario lo movió, se respeta.
  if [[ ! -e $PLUGINS/$id/.placed ]]; then
    omarchy bar move "$id" --section right --after "$after" >/dev/null 2>&1 ||
      omarchy bar move "$id" --section right >/dev/null 2>&1 || true
    touch "$PLUGINS/$id/.placed"
  fi
done
# Los clones reemplazan al widget original en su sitio (una vez: si luego lo quitas, se respeta).
for id in "${CLONES[@]}"; do
  if [[ ! -e $PLUGINS/$id/.placed ]]; then
    omarchy plugin enable "$id" >/dev/null || warn "no se pudo activar $id"
    touch "$PLUGINS/$id/.placed"
  fi
done
systemctl --user daemon-reload
systemctl --user enable --now omarchy-gcal.timer >/dev/null || warn "omarchy-gcal.timer no arrancó"
omarchy restart shell >/dev/null 2>&1 || true

cat <<EOF

Listo. Pasos que solo puedes hacer tú (una vez):
  - Spotify en este PC sin abrir la app (Premium): spotifyd authenticate && systemctl --user enable --now spotifyd
  - Listas de Spotify en el panel: spotify-bar-setup (o "Connect" en el panel) guía la creación de tu app
    de Spotify, guarda el Client ID y autoriza spotify-player.
  - Google Calendar en el reloj: omarchy-gcal setup (o "Connect Google Calendar" en el panel del reloj):
    tu cliente OAuth propio (el mismo proyecto sirve para Google Fotos) y el permiso en el navegador.
  - Google Fotos: gphotos-setup (o "Connect" en el panel): tu client ID de Google y el permiso; luego sube solo.
  - iCloud Drive: icloud-setup (o "Connect" en el panel de iCloud): Apple ID, contraseña y código 2FA.
    Apple caduca la sesión a los 30 días: icloud-setup reconnect (el widget avisa 3 días antes).
Respaldos de lo reemplazado: ${BACKUPS}
EOF
