# Plan de optimización de Omarchy (octubre 2026)

Equipo: ASUS ROG STRIX B850-A · Ryzen 5 9600X · RTX 3060 · Omarchy 4.0.4 en WD SN770 (LUKS + btrfs) ·
Windows 11 en Samsung 980. Contexto completo del sistema: `~/.claude/omarchy.md`.

Este archivo es el **control de avance**: cada tarea tiene estado, qué cambia, cómo se verifica y cómo se
deshace. Al final está la **guía de recuperación** si el sistema deja de arrancar o se pierde.
Está en el fork público (`contrib/omarchy/`): se puede leer desde otro equipo o desde un USB de rescate.

## Leyenda

| Estado | Significado |
|---|---|
| ⬜ | Pendiente |
| 🔄 | En curso |
| ✅ | Hecho y verificado |
| ⚠️ | Hecho con observaciones (ver bitácora) |
| 👤 | Lo hace el usuario (Windows, BIOS, cables, cuentas) |
| ⏸ | Decisión del usuario: no se aplica sin que lo pida |

## Avance

| ID | Tarea | Quién | Riesgo | Root | Estado | Fecha | Evidencia |
|---|---|---|---|---|---|---|---|
| P0.1 | Copia completa de la configuración (tarball) | Claude | nulo | no | ✅ | 2026-10-07 | `omarchy-20261007-095131.tar.gz` (27 MB), sha256 OK |
| P0.2 | Snapshot de `/home` | Claude | nulo | no | ✅ | 2026-10-07 | snapshot home #54 |
| P0.3 | Línea base (arranque, hwcheck, smoke test) | Claude | nulo | parcial | ✅ | 2026-10-07 | arranque 2 min 35 s (firmware 10 s, Limine 7,5 s); hwcheck 0; compat post 0; reward 100 estable |
| P0.4 | Snapshot de root antes de los cambios de sistema | Claude | nulo | sí | ✅ | 2026-10-07 | snapshot root #35; `limine.conf.bak.20261007-095450` |
| P1 | Bluetooth: dejar de ser visible y emparejable siempre | Claude | bajo | no | ✅ | 2026-10-07 | `Discoverable: no`, `Pairable: no` |
| P2 | VRR en el monitor (solo pantalla completa) | Claude | bajo | no | ✅ | 2026-10-07 | `misc:vrr = 2`, `configerrors` vacío |
| P3 | Menú de Limine: espera de 3 s | Claude | bajo | sí | ✅ | 2026-10-07 | `timeout: 3` (antes sin línea = 5 s); Omarchy y Windows en el menú. Medir en el próximo arranque |
| P4 | `nvidia-persistenced` (CUDA/Ollama arrancan antes) | Claude | bajo | sí | ✅ | 2026-10-07 | servicio activo, `persistence_mode=Enabled` |
| P5 | Actualizaciones pendientes con validación | Claude + usuario | bajo | sí | ✅ | 2026-10-07 | preflight OK (C01-C11); `omarchy update` OK; 0 pendientes; `compat post` OK |
| P6 | Archivos de Windows de solo lectura (`windows-files`) | Claude | bajo | polkit | ✅ | 2026-10-07 | montado `ntfs3 ro` en `/run/media/…/24B2E7EDB2E7C202` y desmontado |
| P7 | Validación final, documentación y publicación | Claude | nulo | no | ✅ | 2026-10-07 | hwcheck 0, reward 100 estable; `omarchy.md` al día; commit publicado |
| U1 | Windows: desactivar Inicio rápido | Usuario | — | — | 👤 | | |
| U2 | Windows: reloj en UTC (`RealTimeIsUniversal`) | Usuario | — | — | 👤 | | |
| U3 | Probar "Windows 11" desde el menú de Limine | Usuario | — | — | 👤 | | |
| U4 | BIOS: Memory Context Restore + Fast Boot | Usuario | medio | — | 👤 | | |
| U5 | Webcam NexiGo en un puerto USB directo de la placa | Usuario | — | — | 👤 | | |
| U6 | Conectar Google Fotos (`gphotos-setup`) | Usuario | — | — | 👤 | | |
| U7 | Probar con el ratón los widgets Hardware y Nube | Usuario | — | — | 👤 | | |
| D1 | Perfil de energía `balanced` en vez de `performance` | Decisión | bajo | — | ⏸ | | |
| D2 | Modelo de ~14B en Ollama para opencode (~9 GB) | Decisión | bajo | — | ⏸ | | |
| D3 | Secure Boot (`sbctl`) + desbloqueo LUKS con TPM2 | Decisión | alto | — | ⏸ | | |
| D4 | BIOS 1804 (beta según ASUS) | Decisión | medio | — | ⏸ | | |
| D5 | Desenfoque global de Hyprland | Decisión | bajo | — | ⏸ | | |

---

## Fase 0 · Puntos de restauración y línea base

Nada se cambia sin esto. Si algo sale mal, se vuelve aquí.

### P0.1 Copia completa de la configuración
- **Comando:** `contrib/omarchy/backup-omarchy.sh` → `~/omarchy-backups/omarchy-<fecha>.tar.gz` (+ `.sha256`, `RESTORE.md` dentro).
- **Verificación:** `sha256sum -c ~/omarchy-backups/omarchy-<fecha>.tar.gz.sha256`.
- **Además** el respaldo diario `omarchy-backup` (restic a Google Drive) ya corre solo: `jq . ~/.local/state/omarchy-backup/status.json`.

### P0.2 Snapshot de `/home`
- **Comando:** `snapper -c home create -d "plan: antes de P1-P6" -p` (sin root).
- **Deshacer un archivo:** `omarchy-restore file <ruta> <N>`.

### P0.3 Línea base
- `systemd-analyze` (tiempos de firmware, Limine, kernel, servicios).
- `omarchy-hwcheck` (debe salir 0) y `omarchy-hwcheck diff`.
- `contrib/omarchy/drivers/compat.py post` y `contrib/omarchy/maintenance/smoke-test.sh` + `reward.py`.
- Se anota en la bitácora para comparar al final (P7).

### P0.4 Snapshot de root
- Lo crea el script de sistema (`contrib/omarchy/plan/apply-system.sh`) antes de tocar nada:
  `sudo snapper -c root create -d "plan: antes de P3-P5" -p`. Aparece en el menú de Limine (Snapshots).
- Respaldo de `/boot/limine.conf` en `/etc/omarchy-backups/limine.conf.bak.<fecha>`.

---

## Fase 1 · Cambios sin root

### P1 Bluetooth: no visible ni emparejable de forma permanente
- **Por qué:** con `DISCOVERABLE=yes` + `PAIRABLE=yes` y el agente de Omarchy (`bt-agent -c NoInputNoOutput`,
  acepta todo), cualquiera cerca podía vincular un dispositivo (por ejemplo, un teclado falso) sin confirmar.
- **Cambio:** `~/.config/bt-autopower.conf` → `DISCOVERABLE=no`, `PAIRABLE=no`; `bluetoothctl discoverable off`,
  `bluetoothctl pairable off`. `bt-autopower` relee el archivo en cada ciclo.
- **No cambia:** encendido al arrancar y reconexión de los dispositivos de confianza (JBL Grip).
- **Para vincular algo nuevo:** el panel Bluetooth de Omarchy (activa la búsqueda y el emparejamiento solo mientras está abierto)
  o el interruptor "Visible" en Hardware › Devices.
- **Verificación:** `bluetoothctl show | grep -E 'Discoverable|Pairable'` → `no`; Hardware › Devices sin el aviso rojo.
- **Deshacer:** `DISCOVERABLE=yes` y `PAIRABLE=yes` en el mismo archivo (o el interruptor del widget).

### P2 VRR (frecuencia variable) solo en pantalla completa
- **Por qué:** el monitor (DP-1, 2560×1440, ~165 Hz, en la RTX 3060) admite VRR; en juegos y video a pantalla
  completa evita tirones y desgarro; con `2` el escritorio no cambia (sin parpadeo).
- **Cambio:** bloque al final de `~/.config/hypr/looknfeel.lua` (respaldo `looknfeel.lua.bak.<fecha>`):
  ```lua
  -- VRR solo en pantalla completa (plan P2)
  hl.config({ misc = { vrr = 2 } })
  ```
- **Verificación:** `hyprctl reload && hyprctl configerrors` (vacío) y `hyprctl getoption misc:vrr` → `2`.
  Prueba real: un juego a pantalla completa con MangoHud (los FPS variables no deben desgarrar).
- **Deshacer:** borrar el bloque (o restaurar el `.bak`) y `hyprctl reload`. Si un juego parpadea: `vrr = 0`.

### P6 Archivos de Windows de solo lectura
- **Por qué:** copiar algo del disco de Windows sin arrancarlo. **Solo lectura:** con Inicio rápido o hibernación
  activos, escribir en NTFS corrompe datos de Windows.
- **Cambio:** `~/.local/bin/windows-files [mount|unmount|status]` (fuente: `contrib/omarchy/plan/windows-files`).
  Monta `nvme0n1p3` (PARTUUID `b74588a7-7fd7-4da8-a43b-006e2333b527`) con `udisksctl mount -o ro` (driver `ntfs3`
  del kernel; pide la contraseña con el agente polkit de Omarchy) y abre la carpeta en Nautilus.
- **Verificación:** `windows-files status` → `ro`; `findmnt -no OPTIONS <punto>` contiene `ro`.
- **Deshacer:** `windows-files unmount` y borrar el script.

---

## Fase 2 · Cambios de sistema (root, en una terminal en pantalla)

Script único: `contrib/omarchy/plan/apply-system.sh` (pide la contraseña una vez, registra todo en
`~/.local/state/omarchy-maintenance/plan-<fecha>.log` y escribe `exit=N` al final). Hace P0.4, P3 y P4, y verifica.

### P3 Menú de Limine: 3 s de espera
- **Por qué:** Limine tardó 7,5 s en el último arranque. Con Windows en el menú, 3 s siguen dando tiempo a elegir
  (cualquier tecla detiene la cuenta).
- **Cambio:** línea `timeout: 3` en `/boot/limine.conf` (respaldo antes). `limine-entry-tool` conserva las opciones
  globales al regenerar las entradas. La inscripción de la configuración está desactivada
  (`ENABLE_ENROLL_LIMINE_CONFIG=no`), así que editarla no impide arrancar; aun así el script ejecuta
  `limine-enroll-config` si alguna vez se activa.
- **Verificación:** `sudo grep -n '^timeout' /boot/limine.conf` → `timeout: 3`; en el próximo arranque,
  `systemd-analyze` → "loader" ≈ 3-4 s.
- **Deshacer:** restaurar `/etc/omarchy-backups/limine.conf.bak.<fecha>` o volver a poner el valor anterior (queda en el log).
- **Ojo:** `omarchy refresh limine` reescribe este archivo con los valores de Omarchy.

### P4 `nvidia-persistenced`
- **Por qué:** mantiene el driver inicializado entre trabajos: el primer `cuInit`/Ollama no espera a que la GPU
  despierte. Coste: unos 3-5 W en reposo.
- **Cambio:** `sudo systemctl enable --now nvidia-persistenced`.
- **Verificación:** `nvidia-smi --query-gpu=persistence_mode --format=csv,noheader` → `Enabled`;
  Hardware › GPU muestra "persistence Enabled"; `compat.py post` sin FAIL.
- **Deshacer:** `sudo systemctl disable --now nvidia-persistenced`.

### P5 Actualizaciones pendientes
- **Pendientes (2026-10-07):** aether, claude-desktop, dropbox, dropbox-cli, google-chrome, nautilus-dropbox,
  openclaw, visual-studio-code-bin. Ninguna de kernel ni drivers: no hace falta reiniciar.
- **Antes:** `contrib/omarchy/drivers/compat.py preflight` (0 = sin riesgos conocidos).
- **Actualizar:** `omarchy update` en una terminal (el usuario confirma). El gancho de pacman `[omarchy-compat]` avisa
  de incompatibilidades; el de `omarchy-hwcheck` valida NVIDIA antes de ofrecer el reinicio.
- **Después:** `compat.py post`, `omarchy-hwcheck`, `smoke-test.sh` + `reward.py` (estable ≥ 90).
- **Deshacer:** el snapshot de P0.4 desde Limine (y `omarchy snapshot restore` para fijarlo), o
  `sudo pacman -U /var/cache/pacman/pkg/<paquete-versión-anterior>.pkg.tar.zst`.

---

## Fase 3 · Lo que hace el usuario

| ID | Pasos |
|---|---|
| U1 | Windows → Panel de control → Opciones de energía → "Elegir el comportamiento de los botones" → "Cambiar la configuración actualmente no disponible" → desmarcar **Activar inicio rápido**. O, en PowerShell como administrador: `powercfg /h off`. Para volver a Omarchy: **Apagar**, no Reiniciar (el Bluetooth MT7925 queda trabado si no). |
| U2 | PowerShell como administrador: `reg add "HKLM\SYSTEM\CurrentControlSet\Control\TimeZoneInformation" /v RealTimeIsUniversal /t REG_DWORD /d 1 /f` y reiniciar Windows. Omarchy ya usa UTC (`timedatectl`: RTC in local TZ = no). |
| U3 | Reiniciar, elegir la entrada de Windows en Limine. Si falla: F8 en el POST → Windows Boot Manager (siempre funciona). Contar el resultado para la bitácora. |
| U4 | BIOS (F2/Supr): Ai Tweaker/DRAM → **Memory Context Restore = Enabled**; Boot → **Fast Boot = Enabled** (con CSM desactivado). Detalle: `~/.claude/skills/omarchy-hardware/references/bios-b850a.md`. Si no arranca: borrar CMOS (botón Clear CMOS) y volver a los valores por defecto. Medir con `systemd-analyze`. |
| U5 | Cambiar la webcam NexiGo del hub ASMedia (`3-2.4`) a un puerto trasero directo. Si su micrófono funciona allí (`journalctl -k -g 'usb_set_interface'` sin errores), se puede quitar `~/.config/wireplumber/wireplumber.conf.d/51-disable-nexigo-webcam-mic.conf`. |
| U6 | `gphotos-setup` (o la llave en Nube › Photos): proyecto en Google Cloud, Photos Library API, pantalla de consentimiento **publicada**, cliente "App de escritorio". |
| U7 | Abrir Hardware y Nube con el ratón: interruptores de Bluetooth, montar/desmontar, navegar carpetas, Esc para volver. Anotar lo que falle. |

## Decisiones pendientes (no se aplican sin pedirlo)

| ID | A favor | En contra |
|---|---|---|
| D1 | Menos consumo y calor en reposo; las ráfagas suben igual | Latencia mínima algo mayor; es preferencia |
| D2 | Herramientas de opencode mucho más fiables (cabe en 12 GB de VRAM) | ~9 GB de descarga; más lento que un 7B |
| D3 | Arranque sin escribir la frase de LUKS; protección del arranque | Complejo; la entrada de Windows desde Limine y juegos con anti-trampas cambian; riesgo de bloquearse fuera |
| D4 | AGESA nueva, mejoras de memoria | Beta; puede volver a trabar el Bluetooth; hay que revalidar todo |
| D5 | Efecto cristal completo | Afecta a todas las ventanas transparentes; más GPU |

---

## Recuperación: si algo se rompe

Ordenado de lo más leve a lo más grave. Datos de este equipo:

| | |
|---|---|
| Disco de Omarchy | WD_BLACK SN770 (el nombre `nvme0`/`nvme1` cambia entre arranques: identificar por modelo) |
| Partición EFI de Omarchy | PARTUUID `c5b9b2d9-57de-40e7-886b-14cdb1461f6d` (2 GB, vfat, `/boot`) |
| Partición LUKS | PARTUUID `d714e6bc-ac05-42c0-9474-6e504b34d918` → `/dev/mapper/root` (btrfs) |
| Subvolúmenes | `@` (/), `@home`, `@pkg` (/var/cache/pacman/pkg), `@log` (/var/log) |
| Windows | Samsung 980, EFI propia PARTUUID `8d884047-b96c-43fc-a50a-9b8c1e967d90` |
| Snapshot estable | root **#28** + home **#16**, tag `stable-2026-10-04` en el fork |

### Nivel 1 · El escritorio se ve mal o la barra no carga
- Hyprland: `hyprctl configerrors`; restaurar el `.bak.<fecha>` del archivo tocado; `hyprctl reload`.
- Barra: `omarchy restart shell`; registro en `/run/user/1000/quickshell/by-id/*/log.log`.
- Un widget: `contrib/omarchy/bar/install.sh` (idempotente) o restaurar `~/.local/state/omarchy-bar-extras/backups/<fecha>/`.
- Cualquier archivo de `~`: `omarchy-restore file <ruta> [N]` (snapshot de home; si no está, de restic en Drive).
- La autorrestauración (`omarchy-restore heal`) repone sola, 20 s tras arrancar, lo vigilado que falte.

### Nivel 2 · Arranca pero algo del sistema falla (driver, servicio)
- `omarchy-hwcheck` y `omarchy-hwcheck diff` dicen qué cambió respecto de la línea base.
- `contrib/omarchy/drivers/compat.py post` revisa NVIDIA, CUDA, Vulkan, VA-API, Ollama.
- `contrib/omarchy/drivers/install.sh --check` compara el sistema con el manifiesto de drivers; sin `--check` lo repone.
- Volver un paquete: `sudo pacman -U /var/cache/pacman/pkg/<paquete>-<versión>.pkg.tar.zst` (paccache guarda 3 versiones).

### Nivel 3 · No arranca (pantalla negra, error tras actualizar)
1. En el menú de Limine elegir **Snapshots** → el último anterior al cambio (P0.4 o el #28 estable).
2. Si arranca bien, fijarlo: `omarchy snapshot restore` (`limine-snapper-restore`) y reiniciar.
3. También hay una entrada *fallback* del kernel en el menú (initramfs completo).
4. NVIDIA sin compilar (pantalla negra con el kernel nuevo): arrancar el kernel/snapshot anterior y
   `sudo dkms autoinstall` o `sudo pacman -S nvidia-open-dkms`; `omarchy-hwcheck` (UP02-UP04) lo detecta antes del reinicio.

### Nivel 4 · No aparece el menú de Limine
1. En el POST pulsar **F8** (menú de arranque de ASUS):
   - **Limine** o **UEFI OS** (`EFI/BOOT/BOOTX64.EFI`, copia de Limine: `ENABLE_LIMINE_FALLBACK=yes`).
   - **Windows Boot Manager** arranca Windows directamente, sin Limine.
2. Si Windows se puso primero: BIOS → Boot → Boot Option Priorities, o desde Omarchy `sudo efibootmgr -o 0001,0000,0002`.
3. Si `limine.conf` quedó mal: copiar el respaldo de `/etc/omarchy-backups/` (necesita arrancar; si no, Nivel 5).

### Nivel 5 · Rescate con USB (Omarchy o Arch ISO)
```bash
cryptsetup open /dev/disk/by-partuuid/d714e6bc-ac05-42c0-9474-6e504b34d918 root   # frase de LUKS
mount -o subvol=@ /dev/mapper/root /mnt
mount -o subvol=@home /dev/mapper/root /mnt/home
mount -o subvol=@pkg /dev/mapper/root /mnt/var/cache/pacman/pkg
mount -o subvol=@log /dev/mapper/root /mnt/var/log
mount /dev/disk/by-partuuid/c5b9b2d9-57de-40e7-886b-14cdb1461f6d /mnt/boot
arch-chroot /mnt
# dentro: arreglar (p. ej. cp /etc/omarchy-backups/limine.conf.bak.<fecha> /boot/limine.conf,
#         pacman -U <paquete anterior>, dkms autoinstall) y regenerar el arranque:
limine-update
exit; umount -R /mnt; cryptsetup close root; reboot
```
Volver a un snapshot desde el USB: los snapshots de root están en `/mnt/.snapshots/<N>/snapshot`
(`snapper -c root list` dentro del chroot); lo más simple es arrancar el snapshot desde el menú de Limine (Nivel 3).

### Nivel 6 · Disco perdido o reinstalación desde cero
1. Instalar Omarchy desde su USB (mismo disco o uno nuevo).
2. `curl -fsSL https://raw.githubusercontent.com/JuanSebas7ian/nwg-dock-hyprland/feat/dnd-reorder/contrib/omarchy/restore/bin/omarchy-restore -o /tmp/omarchy-restore && bash /tmp/omarchy-restore full`
   - Pide la **contraseña de restic** (gestor de contraseñas de Google: entrada `restic.omarchy.local`; copia en papel).
   - Restaura `~` desde Google Drive (`rclone:Backups/omarchy-restic`), clona el fork y ejecuta los instaladores:
     drivers (`drivers/install.sh`, con el manifiesto y `/etc` propios), `hwcheck`, dock, barra, mantenimiento.
3. Pasos manuales: `rclone config` si no vino en la copia, `spotifyd authenticate`, `icloud-setup reconnect`,
   parámetros del kernel (`~/.claude/skills/omarchy-hardware/scripts/kernel-param.sh`, ver la tabla de cambios de `omarchy.md`).
4. Volver a añadir Windows a Limine: `~/.claude/skills/omarchy-hardware/scripts/add-windows-entry.sh`.
5. Reaplicar este plan: los scripts de `contrib/omarchy/plan/` son idempotentes.

---

## Bitácora

Una línea por acción: fecha y hora · ID · qué se hizo · resultado o evidencia.

| Fecha | ID | Acción | Resultado |
|---|---|---|---|
| 2026-10-07 | — | Plan creado | — |
| 2026-10-07 09:51 | P0.1-P0.3 | Copia, snapshot home #54, línea base | La 1.ª línea base dio reward 87,5 y `compat post` = 2: falso positivo de `hyprctl configerrors` dejado por `hyprctl dispatch 'hl.exec_cmd(...)'` (lo usaba para abrir terminales y también `omarchy-session` al restaurar ventanas). Cambiado a `hl.dsp.exec_cmd` en `omarchy.md`, la skill, `omarchy-session`, `install.sh`, `README.md`, `backup-omarchy.sh` y `maintenance/PLAN.md`; tras `hyprctl reload`: reward 100, estable |
| 2026-10-07 | P1 | `DISCOVERABLE=no`, `PAIRABLE=no` | Verificado con `bluetoothctl show`. Además se quitó de `~/.config/bt-autopower.conf` una línea `AUTOCONNECT_EXCLUDE` con MAC falsas que una prueba escribió en el archivo real antes de corregir `write_bt_conf` (sin efecto: no eran dispositivos reales); las pruebas ya no tocan archivos reales |
| 2026-10-07 | P2 | `hl.config({ misc = { vrr = 2 } })` en `looknfeel.lua` | `getoption misc:vrr` = 2; VRR se activa solo en pantalla completa |
| 2026-10-07 | P6 | Script `windows-files` (udisks, `-o ro`) | `status` OK; montar pide autorización de polkit |
| 2026-10-07 09:54 | P0.4, P3, P4, P5, P6 | `plan/apply-system.sh` (log `~/.local/state/omarchy-maintenance/plan-run.log`) | exit=0: snapshot root #35; Limine 5 s → 3 s; `nvidia-persistenced` Enabled; 8 actualizaciones de aplicaciones (sin kernel ni drivers, sin reinicio); `compat post`, hwcheck y smoke estables; Windows montado `ro` y desmontado |
| 2026-10-07 | P7 | Plan, `omarchy.md` y publicación | Pendiente para el usuario: U1-U7 y medir el arranque (`systemd-analyze`) tras reiniciar |
