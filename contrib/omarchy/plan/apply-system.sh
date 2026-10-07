#!/usr/bin/env bash
# Cambios de sistema del plan (contrib/omarchy/PLAN-optimizacion.md): P0.4, P3, P4, P5 y la prueba de P6.
# Se ejecuta en una terminal en pantalla (pide la contraseña). Idempotente: lo ya hecho se salta.
#
#   apply-system.sh [log]      log por defecto: ~/.local/state/omarchy-maintenance/plan-<fecha>.log
#   SKIP_UPDATE=1 apply-system.sh   sin P5 (actualizaciones)
#
# Al final escribe "exit=N" en el log (0 = todo bien; 1 = algún paso falló, ver FAIL en el log).
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
OMARCHY_DIR=$(cd "$HERE/.." && pwd)
STAMP=$(date +%Y%m%d-%H%M%S)
LOG=${1:-$HOME/.local/state/omarchy-maintenance/plan-$STAMP.log}
LIMINE_TIMEOUT=${LIMINE_TIMEOUT:-3}
mkdir -p "$(dirname "$LOG")"
: >"$LOG"
FAILED=0

say() { printf '%s\n' "$*" | tee -a "$LOG"; }
ok() { say "OK   $*"; }
fail() { say "FAIL $*"; FAILED=1; }
finish() {
  say "exit=$FAILED"
  echo
  read -r -p "Pulsa Enter para cerrar esta ventana…" _
  exit "$FAILED"
}

say "== Plan de optimización: cambios de sistema ($STAMP)"
say "Escribe tu contraseña cuando la pida sudo."
sudo -v || { fail "sudo"; finish; }
# Mantener sudo vivo mientras dure (omarchy update puede tardar).
(while kill -0 $$ 2>/dev/null; do sudo -n true 2>/dev/null; sleep 50; done) &

# ------------------------------------------------------------------ P0.4
say "-- P0.4 Snapshot de root y respaldo de limine.conf"
if SNAP=$(sudo snapper -c root create -d "plan $STAMP: antes de P3-P5" -p); then
  ok "snapshot root #$SNAP (aparece en Limine › Snapshots)"
else
  fail "no se pudo crear el snapshot: no sigo con cambios de sistema"
  finish
fi
BACKUP=/etc/omarchy-backups/limine.conf.bak.$STAMP
sudo install -D -m 600 /boot/limine.conf "$BACKUP" && ok "respaldo $BACKUP" || { fail "respaldo de limine.conf"; finish; }

# ------------------------------------------------------------------ P3
say "-- P3 Espera del menú de Limine: ${LIMINE_TIMEOUT} s"
current=$(sudo sed -n 's/^timeout:[[:space:]]*//p' /boot/limine.conf | head -1)
say "valor actual: ${current:-(sin línea timeout: Limine usa 5 s)}"
if [[ $current == "$LIMINE_TIMEOUT" ]]; then
  ok "ya estaba en $LIMINE_TIMEOUT"
else
  if [[ -n $current ]]; then
    sudo sed -i "s/^timeout:.*/timeout: $LIMINE_TIMEOUT/" /boot/limine.conf
  else
    sudo sed -i "1i timeout: $LIMINE_TIMEOUT" /boot/limine.conf
  fi
  # Solo si alguna vez se activa la inscripción de la configuración (hoy: no).
  if grep -qs '^ENABLE_ENROLL_LIMINE_CONFIG=yes' /etc/default/limine /etc/limine-entry-tool.d/*.conf; then
    sudo limine-enroll-config && ok "configuración reinscrita"
  fi
  now=$(sudo sed -n 's/^timeout:[[:space:]]*//p' /boot/limine.conf | head -1)
  if [[ $now == "$LIMINE_TIMEOUT" ]] && sudo grep -q '^/+Omarchy' /boot/limine.conf && sudo grep -qi 'bootmgfw.efi' /boot/limine.conf; then
    ok "timeout: $now; Omarchy y Windows siguen en el menú (antes: ${current:-5 por defecto})"
  else
    fail "limine.conf no quedó como se esperaba: restaurando el respaldo"
    sudo cp "$BACKUP" /boot/limine.conf
  fi
fi

# ------------------------------------------------------------------ P4
say "-- P4 nvidia-persistenced"
sudo systemctl enable --now nvidia-persistenced.service >>"$LOG" 2>&1
pm=$(nvidia-smi --query-gpu=persistence_mode --format=csv,noheader 2>/dev/null | head -1)
if systemctl is-active -q nvidia-persistenced.service && [[ $pm == Enabled ]]; then
  ok "nvidia-persistenced activo; persistence_mode=$pm"
else
  fail "nvidia-persistenced: servicio $(systemctl is-active nvidia-persistenced.service), persistence_mode=$pm"
fi

# ------------------------------------------------------------------ P5
if [[ ${SKIP_UPDATE:-0} != 1 ]]; then
  say "-- P5 Actualizaciones"
  "$OMARCHY_DIR/drivers/compat.py" preflight 2>&1 | tee -a "$LOG"
  pre=${PIPESTATUS[0]}
  if ((pre >= 2)); then
    fail "compat.py preflight = $pre: la actualización rompería algo; no actualizo"
  else
    say ">>> omarchy update: confirma lo que pregunte. Si ofrece reiniciar, puedes decir que no (no hay kernel ni drivers)."
    omarchy update
    up=$?
    ((up == 0)) && ok "omarchy update" || fail "omarchy update terminó con $up"
  fi
fi

# ------------------------------------------------------------------ comprobaciones
say "-- Comprobaciones"
"$OMARCHY_DIR/drivers/compat.py" post >>"$LOG" 2>&1 && ok "compat.py post" || fail "compat.py post (ver arriba)"
omarchy-hwcheck >>"$LOG" 2>&1; hw=$?
((hw <= 1)) && ok "omarchy-hwcheck = $hw" || fail "omarchy-hwcheck = $hw"
smoke=$(mktemp)
"$OMARCHY_DIR/maintenance/smoke-test.sh" --json >"$smoke" 2>/dev/null
verdict=$(python3 "$OMARCHY_DIR/maintenance/reward.py" "$smoke" 2>&1)
printf '%s\n' "$verdict" >>"$LOG"
[[ $verdict == *'"stable": true'* ]] && ok "smoke test estable ($(printf '%s\n' "$verdict" | grep -m1 '^reward:'))" || fail "smoke test no estable"
rm -f "$smoke"

# ------------------------------------------------------------------ P6
say "-- P6 Archivos de Windows (solo lectura): prueba"
if command -v windows-files >/dev/null; then
  say ">>> udisks puede pedir tu contraseña otra vez para montar el disco de Windows."
  if windows-files mount >>"$LOG" 2>&1; then
    st=$(windows-files status)
    [[ $st == *" ro,"* ]] && ok "solo lectura: $st" || fail "no quedó en solo lectura: $st"
    windows-files unmount >>"$LOG" 2>&1 && ok "desmontado"
  else
    fail "windows-files mount (¿Windows hibernado? ver el log)"
  fi
fi

say "== Fin: $( ((FAILED == 0)) && echo 'todo bien' || echo 'revisa las líneas FAIL')"
finish
