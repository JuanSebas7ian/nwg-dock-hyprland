# Mantenimiento automático de Omarchy: plan (2026-10-04)

Origen: auditoría general del 2026-10-04 (`~/.claude/omarchy.md`, "Auditoría general").
Equipo: Omarchy 4.0.4 (Arch, Hyprland Lua), LUKS2 + btrfs en WD SN770 (`/dev/mapper/root`, `cryptdevice=PARTUUID=d714e6bc-…:root`,
hook `encrypt`), Samsung 980 con Windows, snapper `root` (NUMBER_LIMIT 5, NUMBER_LIMIT_IMPORTANT 5) y `home`.

Flujo: **implementa Sonnet → evalúa Opus → corrige → snapshot "estable"**. Nada se da por bueno sin el smoke test y el reward.

## Pasos

| # | Paso | Cómo | Verificación (smoke test) |
|---|---|---|---|
| S0 | Snapshot previo | `snapper -c root create -t pre -p -d "maintenance: before"` (guarda el número para el `post`) | número en el log |
| 1 | **TRIM a través de LUKS** | LUKS2 guarda el flag en la cabecera: `cryptsetup refresh --allow-discards --persistent root` (pide la frase de LUKS; aplica en caliente, sin tocar la línea de arranque ni el initramfs). Después `systemctl enable --now fstrim.timer` y un `fstrim -v /` inicial | `/sys/block/dm-*/queue/discard_max_bytes` del mapeo `root` > 0; `fstrim.timer` enabled+active |
| 2 | Limpieza de la caché de pacman | `PACCACHE_ARGS='-k3'` en `/etc/conf.d/pacman-contrib` (respaldo `.bak.<fecha>`); `systemctl enable --now paccache.timer`; una pasada inicial `paccache -rk3` + `paccache -ruk0` | `paccache.timer` enabled+active; ninguna versión con más de 3 copias en la caché |
| 3 | Scrub mensual de btrfs | `systemctl enable --now btrfs-scrub@-.timer` (cubre `/` y `/home`, mismo sistema de archivos). No lanzar un scrub ahora (lee 172 GB) | timer enabled+active |
| 4 | Vigilancia SMART con aviso | paquete `smartmontools`; `/etc/smartd.conf` (respaldo) con `DEVICESCAN -a -n standby,q -W 0,70,80 -m <nomailer> -M exec /usr/local/lib/omarchy/smartd-notify`; `smartd-notify` manda `notify-send -u critical` a la sesión del usuario (uid 1000, `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus`) con `$SMARTD_DEVICE`/`$SMARTD_MESSAGE`, y lo registra en el journal (`logger -t smartd-notify`). `systemctl enable --now smartd` | `smartd` active; el config tiene `-M exec` hacia un script ejecutable; `smartctl -H` sin root no aplica: se comprueba `journalctl -u smartd` sin errores de configuración |
| 5 | Quitar huérfanos | `pacman -Rns drive-bin-debug gdrive-debug` (solo si siguen huérfanos) | `pacman -Qdtq` vacío |
| 6 | Códecs y diagnóstico | `pacman -S --needed gst-plugins-good gst-plugins-bad gst-plugins-ugly gst-libav libva-utils nvme-cli` | paquetes instalados; `vainfo` lista perfiles con el driver `nvidia` |
| 7 | Suspensión | **No automático.** Script `suspend-test.sh` para que el usuario lo lance cuando quiera: `rtcwake -m mem -s 30` (despierta solo a los 30 s) y luego revisa `journalctl -b` (`PM: suspend exit`, sin `NVRM: Xid`) | manual |
| 8 | `/boot` | Solo vigilancia | uso de `/boot` < 85 % |
| S1 | Snapshot posterior | `snapper -c root create -t post --pre-number <S0> -d "maintenance: after"` | número en el log |
| S2 | **Snapshot estable** | Solo si el smoke test pasa, el reward ≥ 90 y la evaluación de Opus no tiene bloqueantes: `snapper -c root create -d "stable 2026-10-04: maintenance" -u important=yes` + `snapper -c home create -d "stable …"` + manifiesto de versiones en `~/.local/state/omarchy-stable/<fecha>/` (`pacman -Q`, kernel, driver NVIDIA, `omarchy-hwcheck --json`, `smoke.json`, `reward.json`, commit del fork) + tag git `stable-<fecha>` en `fork` | snapshot listado con `important=yes`; manifiesto completo |

## Archivos (`contrib/omarchy/maintenance/`)

| Archivo | Qué hace |
|---|---|
| `apply.sh` | Ejecuta S0, pasos 1-6 y S1 como root (`sudo`), idempotente: cada paso comprueba antes y dice `skip` si ya está hecho. `--dry-run` imprime lo que haría sin cambiar nada (sin root). `--only 2,3` para pasos sueltos. Log en `~/.local/state/omarchy-maintenance/apply-<fecha>.log` y `exit=N` al final. Nunca toca `/usr/share/omarchy/` |
| `smart-notify` | El script `-M exec` de smartd (se instala en `/usr/local/lib/omarchy/smartd-notify`) |
| `smoke-test.sh` | Sin root, solo lectura, determinista. Una línea por comprobación `PASS|FAIL|WARN|SKIP <id> <texto>` y `--json` para el reward. Variables `SYSFS_ROOT`, `PROC_ROOT` para probar con un árbol falso |
| `reward.py` | Lee el JSON del smoke test y da una nota de 0 a 100 por pesos (TRIM 25, smartd 20, paccache 10, scrub 10, huérfanos 5, paquetes 10, `/boot` 5, sin servicios fallidos 10, `hyprctl configerrors` vacío 5). FAIL = 0 de su peso, WARN = la mitad. Umbral estable: 90 y ningún FAIL en TRIM ni smartd. Salida JSON + resumen; código 0 si es estable |
| `stable-snapshot.sh` | S2 (root): se niega si `reward.py` no da estable |
| `suspend-test.sh` | Paso 7, manual |
| `tests/` | `unittest` de `reward.py` (pesos, umbral, WARN, FAIL bloqueante, JSON roto) y del smoke test contra árboles falsos (discard 0 vs > 0, `/boot` lleno) |
| `README.md` | Qué hace cada paso, cómo deshacerlo, cómo correrlo |

## Deshacer

- 1: `cryptsetup refresh --persistent root` sin `--allow-discards` (pide la frase) · `systemctl disable --now fstrim.timer`
- 2: restaurar `/etc/conf.d/pacman-contrib.bak.<fecha>` · `systemctl disable --now paccache.timer`
- 3: `systemctl disable --now btrfs-scrub@-.timer`
- 4: `systemctl disable --now smartd` · restaurar `/etc/smartd.conf.bak.<fecha>` · `rm /usr/local/lib/omarchy/smartd-notify` · `pacman -Rns smartmontools`
- 6: `pacman -Rns gst-plugins-good gst-plugins-bad gst-plugins-ugly gst-libav libva-utils nvme-cli`
- Todo: arrancar el snapshot S0 desde Limine.

## Reglas

- Root solo en `apply.sh` y `stable-snapshot.sh`, lanzados en una terminal visible (el usuario escribe la contraseña):
  `hyprctl dispatch "hl.exec_cmd(\"uwsm-app -- xdg-terminal-exec <script>\")"`, y esperar `exit=` en el log.
- Probar primero `--dry-run` y las pruebas unitarias; después la ejecución real.
- Commits en inglés en `feat/dnd-reorder`, push solo a `fork`; verificar con un clon limpio (`.gitignore` ignora `bin`).
- Documentar en `~/.claude/omarchy.md` (tabla de cambios, cómo deshacer).
