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

## Deshacer

`sudo rm /etc/pacman.d/hooks/90-omarchy-compat.hook /usr/local/lib/omarchy/compat-hook /usr/local/lib/omarchy/compat.py`.
Una reinstalación real deja `<archivo>.bak.<fecha>` junto a cada archivo de `/etc` que reemplaza, y el snapshot `pre` en Limine.
