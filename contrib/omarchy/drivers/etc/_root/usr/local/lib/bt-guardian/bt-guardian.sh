#!/usr/bin/env bash
# bt-guardian: mantiene disponible el Bluetooth MT7925 de la ASUS ROG STRIX B850-A.
#
# - Si hay adaptador (hciN): no hace nada. BlueZ (AutoEnable=true) lo enciende solo, y si el usuario
#   apagó el Bluetooth (bloqueo rfkill, que Omarchy usa como estado), se respeta.
# - Si no hay adaptador: intenta recuperarlo sin tocar el resto del USB: desactiva y reactiva solo el
#   puerto USB interno del chip y, si no basta, recarga btusb. Como máximo MAX_ATTEMPTS por arranque.
#
# Lo ejecuta bt-guardian.service (al arrancar, cada 2 min por bt-guardian.timer y al despertar).
# Uso manual: bt-guardian.sh [--dry-run]   (--dry-run: solo informa, no necesita root)
set -uo pipefail

PORT=${BT_GUARDIAN_PORT:-/sys/bus/usb/devices/6-0:1.0/usb6-port11}
MAX_ATTEMPTS=${BT_GUARDIAN_MAX_ATTEMPTS:-3}
STATE_DIR=/run/bt-guardian
DRY_RUN=false
[[ ${1:-} == --dry-run ]] && DRY_RUN=true

log() {
  echo "$*"
  $DRY_RUN || logger -t bt-guardian -- "$*"
}

adapter() {
  compgen -G '/sys/class/bluetooth/hci*' > /dev/null
}

# Espera hasta `seconds` a que aparezca el adaptador
wait_adapter() {
  local seconds=$1
  for ((i = 0; i < seconds; i++)); do
    adapter && return 0
    sleep 1
  done
  return 1
}

if adapter; then
  $DRY_RUN && log "adaptador presente ($(basename /sys/class/bluetooth/hci* | head -1)): nada que hacer"
  exit 0
fi

if $DRY_RUN; then
  log "sin adaptador; puerto $(basename "$PORT"): estado=$(cat "$PORT/state" 2>/dev/null || echo '?')"
  log "haría: reiniciar ese puerto y, si no basta, recargar btusb (máximo $MAX_ATTEMPTS intentos por arranque)"
  exit 0
fi

[[ $EUID -eq 0 ]] || {
  echo "necesita root (o usa --dry-run)" >&2
  exit 1
}

mkdir -p "$STATE_DIR"
attempts=$(cat "$STATE_DIR/attempts" 2> /dev/null || echo 0)
if ((attempts >= MAX_ATTEMPTS)); then
  # ya se avisó al llegar al límite; no llenar el registro cada 2 minutos
  exit 0
fi
echo $((attempts + 1)) > "$STATE_DIR/attempts"
log "intento $((attempts + 1))/$MAX_ATTEMPTS: no hay adaptador Bluetooth; puerto $(basename "$PORT") estado=$(cat "$PORT/state" 2> /dev/null || echo '?')"

# 1. Reiniciar solo el puerto del chip
if [[ -w $PORT/disable ]]; then
  echo 1 > "$PORT/disable"
  sleep 2
  echo 0 > "$PORT/disable"
  if wait_adapter 10; then
    log "recuperado reiniciando el puerto $(basename "$PORT")"
    exit 0
  fi
  log "reinicio del puerto: el chip sigue sin responder"
else
  log "no puedo reiniciar el puerto: $PORT/disable no existe"
fi

# 2. Recargar el driver
modprobe -r btusb 2> /dev/null && modprobe btusb
if wait_adapter 10; then
  log "recuperado recargando btusb"
  exit 0
fi

log "recarga de btusb: sigue sin adaptador"
if ((attempts + 1 >= MAX_ATTEMPTS)); then
  log "sin éxito tras $MAX_ATTEMPTS intentos en este arranque. Suele hacer falta un corte total de energía " \
    "(fuente apagada 30 s) y evitar el Inicio rápido de Windows; ver references/bluetooth-mt7925.md"
fi
# No recuperar el adaptador no es un fallo del servicio: queda en el registro, y así systemd no lo marca
# como unidad fallida cada 2 minutos.
exit 0
