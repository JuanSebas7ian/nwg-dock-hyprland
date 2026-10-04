# Drivers: manifiesto, reinstalación y compatibilidad

Plan en `PLAN.md`. Flujo: implementa Sonnet, evalúa Opus, snapshot estable. **Nada de esto puede romper el sistema**: en este
equipo `install.sh` solo se ejecuta con `--check`/`--dry-run`; el gancho de pacman **solo avisa** y siempre sale con 0.

## Uso

```bash
./capture.sh                       # regenera manifest.json y etc/ desde el sistema (sin root) y muestra el diff
./capture.sh --state DIR --no-repo # solo escribe DIR/drivers-state.json (versiones exactas); lo usa stable-snapshot.sh
./install.sh --check               # sin root: qué falta o difiere (0 = nada)
./install.sh --dry-run             # lo que haría una reinstalación
./install.sh [--groups nvidia,cuda] --wait   # real (terminal visible, sudo): snapshot pre/post, paquetes, /etc, DKMS, servicios
./install.sh --hook-only --wait    # solo el gancho de pacman
python3 compat.py preflight        # ¿rompen drivers u otras apps las actualizaciones pendientes? (checkupdates)
python3 compat.py post             # ¿funciona todo ahora? (driver, DKMS, nvidia-smi, cuInit, Vulkan, VA-API, ollama, Hyprland)
./smoke-test.sh [--json]           # prueba sin root del módulo
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests
```

`compat.py`: salida `OK|WARN|FAIL|SKIP <id> <texto>`; código 0 todo bien, 1 avisos, 2 rompería algo, 3 no se pudo evaluar (sin red / `checkupdates` falló, o uso incorrecto). Reglas C01-C14 en `PLAN.md`.

## Reinstalar con seguridad

- **Ejecútalo en un sistema totalmente sincronizado** (`omarchy update` antes). `install.sh` usa `pacman -S --needed`, nunca `-Sy`;
  si hay actualizaciones pendientes y faltan paquetes, una ejecución real se detiene (`--allow-pending` lo salta; `--dry-run` solo avisa).
- Si falla la instalación de paquetes, se omiten los `/etc`, DKMS, initramfs y servicios (todos, no por grupo) y se informa.
- Una sola reconstrucción del initramfs/UKI: `limine-update` (Omarchy no tiene presets de mkinitcpio; `mkinitcpio -P` solo si existen).
- Los archivos con `"policy": "if-missing"` los genera Omarchy (nvidia.conf de modprobe y mkinitcpio, blacklist-xpad, xpadneo): solo se
  instalan si faltan, nunca se sobrescriben ni se comparan.
- `modules-load.d/claude-xpad.conf` carga `xpad` aunque Omarchy deje `blacklist xpad` (la lista negra solo impide la carga
  automática): `xpadneo` solo maneja Bluetooth, y sin `xpad` los controles Xbox con cable (p. ej. Xbox 360, `045e:028e`) no tienen driver.

## Manifiesto

`manifest.json`: `groups` (repo/aur por grupo), `etc_files` (destino → copia en `etc/`; lo de fuera de `/etc` va en `etc/_root/`),
`services`, `dkms`, `omarchy_owned` (solo se verifica), `hook` (gancho de pacman) e `installers` (referencias, p. ej. bt-guardian).
Sin versiones: Arch es rolling; las versiones exactas quedan en `drivers-state.json` de cada snapshot estable.
`capture.sh` no copia archivos que parezcan contener secretos. `xpadneo-dkms` está en el repo `omarchy`, no en el AUR.

## Gancho de pacman

`pacman-hook/90-omarchy-compat.hook` (`PreTransaction`, `NeedsTargets`, sin `AbortOnFail`) llama a `compat-hook`, que corre
`compat.py hook` con `timeout 5` y sale siempre con 0. Imprime `[omarchy-compat] ...` con los avisos y un resumen. Limitación:
pacman pasa solo nombres, así que el gancho solo se activa para `Install`/`Upgrade` (no para `Remove`). Si `pacman -Si` no responde imprime "no se pudo evaluar".

## Equipo nuevo

`../restore/bin/omarchy-restore full` ejecuta `install.sh` (real) antes de los demás instaladores. A mano:
`omarchy pkg add git && git clone -b feat/dnd-reorder https://github.com/JuanSebas7ian/nwg-dock-hyprland.git ~/src/nwg-dock-hyprland-dnd`,
luego `contrib/omarchy/drivers/install.sh --wait` y reiniciar.

## Control Xbox 360 con cable: `xpad` y `pad-keepalive`

- `etc/modules-load.d/claude-xpad.conf` carga `xpad` (ver arriba): sin él, el control con cable (`045e:028e`) no tiene driver.
- **Cortes USB jugando** (2026-10-04, ELDEN RING): el puerto del chipset (`usb6-port2`, `xhci-pci-prom21`) registraba
  `disabled by hub (EMI?), re-enabling...` y desconexiones limpias, cada ~2 min y solo con el juego abierto. Se descartaron el driver,
  el autosuspend (`power/control = on`), la vibración (20 s al máximo y 3.000 órdenes en 30 s) y el cable (30 s doblándolo).
  En un puerto trasero del controlador de la CPU (`3-2.2`, `0b:00.4`) no se ha repetido.
- `pad-keepalive/` (usuario, sin root; `python-evdev`, grupo `input`): crea un **mando virtual permanente** (uinput, mismos IDs,
  `phys=pad-keepalive/input0`), acapara el físico (`EVIOCGRAB`) y le reenvía botones, ejes y vibración. Si el físico se cae, el
  virtual sigue (se sueltan los botones) y el físico se retoma al volver (~1 s): el juego no ve la desconexión. Probado
  desconectando el cable con el juego abierto: Steam y `winedevice.exe` abrieron el virtual sin reiniciar el juego.
  Instalar: `pad-keepalive/install.sh` (idempotente; respaldos en `~/.local/state/omarchy-drivers/backups/`). Quitar: `--remove`.
  Registro: `journalctl --user -u pad-keepalive`.
  Pruebas (sin hardware: sysfs y dispositivos falsos): `cd pad-keepalive && PYTHONDONTWRITEBYTECODE=1 /usr/bin/python3 -m unittest test_pad_keepalive` (9).
  Detalles: mientras el control falta lo busca en sysfs cada 0,25 s sin abrir ningún dispositivo; si otro proceso lo tiene acaparado,
  lo registra una vez y reintenta cada 5 s; al reconectar copia los botones y palancas que estén pulsados; el servicio no tiene
  límite de reinicios. Un único control por proxy: un segundo Xbox 360 con cable no pasa por él.
- **Limitación conocida:** Steam sigue listando el físico (mudo) como mando 0 y el virtual como 1. Si un juego solo escucha al
  mando 0, en Steam → Configuración → Mando poner el virtual primero. La solución completa sería una regla udev que oculte el
  físico (`TAG-="uaccess"`, `MODE="0600"`, `ENV{ID_INPUT_JOYSTICK}=""`) con el proxy como servicio del sistema; **no está
  incluida**: pendiente de la decisión del usuario.

## Deshacer

`sudo rm /etc/pacman.d/hooks/90-omarchy-compat.hook /usr/local/lib/omarchy/compat-hook /usr/local/lib/omarchy/compat.py`.
Una reinstalación real deja `<archivo>.bak.<fecha>` junto a cada archivo de `/etc` que reemplaza, y el snapshot `pre` en Limine.
