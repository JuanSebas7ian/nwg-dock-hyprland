#!/usr/bin/env bash
# Mantiene el dock vivo: una sola instancia, espera a Hyprland,
# backoff entre reinicios y límite de fallos seguidos.
# Usa el build con arrastrar y soltar (-dnd). Si no existe o falla
# 5 veces seguidas, vuelve al dock oficial.
#
# Protección para que un dock roto nunca bloquee el sistema:
# - corre en su propio scope de systemd con memoria y CPU limitadas;
# - con -dnd, el propio dock se cierra (código 2) si se cuelga 10 s;
# - su salida va a ~/.local/state/nwg-dock/dock.log para diagnosticar;
# - SUPER+CTRL+SHIFT+D lo mata a mano (bindings.lua) y este script lo relanza.
set -u

LOCK="${XDG_RUNTIME_DIR:-/tmp}/launch-dock.lock"
exec 9>"$LOCK"
flock -n 9 || exit 0

DOCK_ARGS=(-d -i 48 -mb 10 -f -w 8 -hd 300)
DND_BIN="$HOME/.local/bin/nwg-dock-hyprland-dnd"
OFFICIAL_BIN=/usr/bin/nwg-dock-hyprland
# Solo procesos cuyo ejecutable sea uno de los dos docks
PATTERN='^([^ ]*/)?nwg-dock-hyprland(-dnd)?( |$)'
LIMITS=(-p MemoryMax=400M -p CPUQuota=80%)
LOG_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/nwg-dock"
LOG="$LOG_DIR/dock.log"

mkdir -p "$LOG_DIR"

use_official() {
  BIN=$OFFICIAL_BIN
  ARGS=("${DOCK_ARGS[@]}")
}

# Conserva el registro anterior y empieza uno nuevo si pasa de 1 MB
rotate_log() {
  if [[ -f $LOG ]] && (( $(stat -c %s "$LOG") > 1048576 )); then
    mv -f "$LOG" "$LOG.1"
  fi
}

run_dock() {
  # 9>&- : el dock no hereda el lock, así un dock huérfano no bloquea al script
  if systemd-run --user --scope --quiet --collect "${LIMITS[@]}" -- true 2>/dev/null; then
    systemd-run --user --scope --quiet --collect "${LIMITS[@]}" -- "$BIN" "${ARGS[@]}" 9>&- >> "$LOG" 2>&1
  else
    "$BIN" "${ARGS[@]}" 9>&- >> "$LOG" 2>&1
  fi
}

if [[ -x $DND_BIN ]]; then
  BIN=$DND_BIN
  ARGS=("${DOCK_ARGS[@]}" -dnd)
else
  logger -t launch-dock "$DND_BIN no existe; uso el dock oficial"
  use_official
fi

for _ in $(seq 1 60); do
  hyprctl monitors >/dev/null 2>&1 && break
  sleep 0.5
done

fails=0
while true; do
  hyprctl monitors >/dev/null 2>&1 || exit 0
  pkill -f "$PATTERN" 2>/dev/null
  rotate_log
  echo "=== $(date '+%F %T') arranca $(basename "$BIN") ${ARGS[*]}" >> "$LOG"
  start=$SECONDS
  run_dock
  rc=$?
  runtime=$((SECONDS - start))
  case $rc in
    2) reason="se colgó y el vigilante lo cerró" ;;
    137) reason="lo mataron (SIGKILL u OOM)" ;;
    *) reason="código $rc" ;;
  esac
  logger -t launch-dock "$(basename "$BIN") salió ($reason) tras ${runtime}s; registro en $LOG"
  echo "=== $(date '+%F %T') salió: $reason tras ${runtime}s" >> "$LOG"

  if (( runtime < 10 )); then fails=$((fails + 1)); else fails=0; fi
  if (( fails >= 5 )); then
    if [[ $BIN == "$DND_BIN" ]]; then
      logger -t launch-dock "5 fallos seguidos con -dnd; vuelvo al dock oficial"
      use_official
      fails=0
    else
      logger -t launch-dock "5 fallos seguidos; me detengo"
      exit 1
    fi
  fi
  sleep $(( 2 * (fails > 0 ? fails : 1) ))
done
