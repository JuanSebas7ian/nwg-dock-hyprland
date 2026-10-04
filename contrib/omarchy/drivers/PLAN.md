# Drivers: manifiesto, (re)instalación y compatibilidad — plan (2026-10-04)

Objetivo: (a) todo el stack de drivers de este equipo descrito en un manifiesto declarativo y reinstalable de forma
idempotente en un Omarchy nuevo o roto; (b) un validador que, **antes** de actualizar, diga si la actualización pendiente
rompe drivers u otras aplicaciones, y **después**, que todo sigue funcionando. Regla de oro: **nada de esto puede romper el
sistema**: en este equipo la reinstalación solo se ejecuta en modo `--check`/`--dry-run` (ya está todo instalado); el gancho
de pacman **solo avisa**, nunca aborta una transacción.

Flujo: **implementa Sonnet → evalúa Opus → corrige → snapshot estable** (igual que `../maintenance/`).

## Estado actual (2026-10-04, verificado)

| Grupo | Paquetes / archivos |
|---|---|
| Kernel | `linux` + `linux-headers` 7.2.3.arch1-3; `amd-ucode`; `linux-firmware` + `linux-firmware-{amdgpu,atheros,broadcom,cirrus,intel,mediatek,nvidia,other,radeon,realtek,whence}` 20260810 |
| NVIDIA | `nvidia-open-dkms`, `nvidia-utils`, `lib32-nvidia-utils`, `opencl-nvidia` 610.57.04; `egl-wayland`, `egl-wayland2`, `egl-gbm`, `egl-x11`, `libva-nvidia-driver`, `libvdpau`. DKMS: `nvidia/610.57.04` instalado para 7.2.3 |
| CUDA / IA | `cuda` 13.3.1, `cccl`, `cudnn` 9.25.1.1, `ollama` + `ollama-cuda` 0.33.3 |
| AMD iGPU | `mesa`, `lib32-mesa`, `vulkan-radeon`, `lib32-vulkan-radeon` 26.2.2 |
| Vulkan/VA | `vulkan-icd-loader`, `lib32-vulkan-icd-loader`, `vulkan-tools`, `libva-utils` |
| Periféricos | `bluez`, `bluez-utils`, `solaar`, `openrgb`, `fwupd`, `lm_sensors`, `smartmontools`, `nvme-cli`; **AUR**: `xpadneo-dkms` (DKMS `hid-xpadneo` 0.10.4) |
| Juegos | `steam`, `gamemode`, `lib32-gamemode`, `mangohud`, `lib32-mangohud`, `gamescope` |
| `/etc` propios | `modprobe.d/nvidia.conf` (Omarchy: solo `options nvidia_drm modeset=1`; `PreserveVideoMemoryAllocations=1` y `UseKernelSuspendNotifiers=1` vienen de `/usr/lib/modprobe.d/nvidia-sleep.conf` de nvidia-utils y de los valores por defecto del driver; se verifican en `/proc/driver/nvidia/params`, C13j). Los archivos que genera Omarchy (`nvidia.conf` de modprobe y de mkinitcpio, `blacklist-xpad.conf`, `xpadneo.conf`) llevan `policy: if-missing`, `modules-load.d/{claude-nct6775,xpadneo}.conf`, `limine-entry-tool.d/claude-{bluetooth-mt7925,usb-fast-timeout}.conf`, `mkinitcpio.conf.d/nvidia.conf` (MODULES nvidia…), `systemd/system/ollama.service.d/context.conf`, `smartd.conf`, `/usr/local/lib/omarchy/smartd-notify`, `conf.d/pacman-contrib`, `/usr/local/lib/bt-guardian/` + unidades |
| Servicios | `ollama`, `bluetooth`, `smartd`, `fstrim.timer`, `paccache.timer`, `btrfs-scrub@-.timer`, `bt-guardian.timer` |
| De Omarchy (no tocar, solo verificar) | `omarchy-*.conf` en `/etc/limine-entry-tool.d`, `mkinitcpio.conf.d/omarchy_*.conf`, `modprobe.d/omarchy-usb-autosuspend.conf`, pacman hooks de limine/dkms en `/usr/share/libalpm/hooks` |

`omarchy update` solo ofrece el gancho `post-update` (`~/.config/omarchy/hooks/post-update.d/`, ya ejecuta `omarchy-hwcheck`);
no hay `pre-update`. Para avisar antes se usan: el comando `preflight` (a mano o desde la barra) y un gancho **PreTransaction de
pacman** que solo imprime avisos.

## Archivos (`contrib/omarchy/drivers/`)

| Archivo | Qué hace |
|---|---|
| `manifest.json` | Declarativo: `groups` → `{repo: [...], aur: [...]}`, `etc_files` (ruta destino → copia en `etc/` del repo, modo, dueño), `services` (system/user), `dkms` (módulos esperados), `omarchy_owned` (solo verificar). Sin versiones fijadas (Arch es rolling); las versiones exactas van en el manifiesto de estado de cada snapshot estable |
| `etc/` | Copias de los `/etc` propios (no los de Omarchy). Sin secretos |
| `capture.sh` | Sin root. Regenera `manifest.json` (preservando la estructura) y `etc/` desde el sistema; muestra el diff; nunca escribe fuera del repo. `--state <dir>` escribe además el estado con versiones exactas (`drivers-state.json`: paquetes y versiones, DKMS, kernel, driver cargado, CUDA, `nvidia-smi`, `/proc/cmdline`, BIOS) — lo llama `../maintenance/stable-snapshot.sh` |
| `install.sh` | (Re)instala desde el manifiesto, idempotente. `--check` (sin root): lista lo que falta o difiere (paquetes, `etc`, servicios, DKMS) y sale 0 si no falta nada. `--dry-run`: lo que haría. Real (root, terminal visible): snapshot `pre` (`-c number`), `pacman -S --needed` por grupos, AUR con `omarchy pkg aur add` (o `yay`/`paru` si existe), copia de `etc` con respaldo `.bak.<fecha>` **solo si difiere**, `dkms autoinstall` si falta un módulo, `limine-update` solo si cambió algún `limine-entry-tool.d`, `mkinitcpio -P` solo si cambió `mkinitcpio.conf.d` o `modprobe.d`, habilitar servicios, snapshot `post`, y al final `compat.py post`. `--groups nvidia,cuda` para grupos sueltos. Log + `exit=N` |
| `compat.py` | Validador, Python stdlib. `preflight`: lee las actualizaciones pendientes con `checkupdates` (sin root, base temporal; `--from-file` para pruebas) y aplica las reglas de abajo sobre el estado **final** (versión instalada o la nueva). `post`: comprueba que todo funciona ahora. `--json`. Salida 0 OK, 1 avisos, 2 rompería algo |
| `pacman-hook/90-omarchy-compat.hook` + `compat-hook` | Gancho `PreTransaction` (`Operation = Upgrade/Install/Remove`, `Target = *`, `NeedsTargets`, **sin `AbortOnFail`**): llama `compat.py hook` con los objetivos por stdin; imprime avisos en la salida de pacman; tiempo máximo 5 s; si algo falla, sale 0 en silencio. Se instala en `/etc/pacman.d/hooks/` y `/usr/local/lib/omarchy/` |
| `smoke-test.sh` | Sin root: `install.sh --check` limpio, `compat.py post` sin FAIL, gancho instalado y ejecutable, `etc/` del repo igual al sistema |
| `tests/` | `unittest`: reglas de `compat.py` con escenarios simulados (cada regla con un caso OK y uno que rompe), `install.sh --check/--dry-run` con `ROOT=` falso y binarios simulados en `PATH` (pacman, systemctl, dkms), `capture.sh` determinista (dos ejecuciones = mismo resultado), y el gancho: nunca devuelve ≠ 0 ni tarda > 5 s |
| `README.md` | Uso, cómo reinstalar en un equipo nuevo, cómo deshacer |

## Reglas de compatibilidad (`compat.py`)

Cada una con id, nivel (FAIL = rompería, WARN = riesgo), y texto en español.

| Id | Regla | Nivel |
|---|---|---|
| C01 | `nvidia-utils` = `lib32-nvidia-utils` = `opencl-nvidia` = `nvidia-open-dkms` (versión final) | FAIL |
| C02 | `linux` = `linux-headers` (versión final) | FAIL |
| C03 | Si cambia el kernel y `nvidia-open-dkms` no cambia: el DKMS debe compilar para el nuevo; WARN si el salto es de versión mayor/menor (7.2 → 7.3) | WARN |
| C04 | Módulos DKMS de AUR (`xpadneo-dkms`): si cambia el kernel, WARN (el AUR no se actualiza con pacman; revisar `dkms status` después) | WARN |
| C05 | CUDA vs driver: versión mínima del driver por CUDA mayor (tabla en el código: 12.x ≥ 525, 13.x ≥ 580); FAIL si la final no la cumple | FAIL |
| C06 | `cudnn` y `ollama-cuda` dependen de `cuda`: si cambia la versión mayor de `cuda` y ellos no, WARN | WARN |
| C07 | `mesa` = `lib32-mesa` = `vulkan-radeon` = `lib32-vulkan-radeon` | FAIL |
| C08 | `vulkan-icd-loader` = `lib32-vulkan-icd-loader`; `gamemode`/`lib32-gamemode` y `mangohud`/`lib32-mangohud` iguales | WARN |
| C09 | Espacio en `/boot`: ≥ 200 MB libres si cambia el kernel, un `linux-firmware*` o `amd-ucode` (UKI + snapshots de Limine) | FAIL |
| C10 | Espacio en `/`: ≥ 5 GB libres antes de una actualización | WARN |
| C11 | `hyprland`, `quickshell` o `omarchy` cambian: WARN de que se reinicia la sesión/barra (los plugins propios usan la API de Quickshell; revisar `omarchy restart shell`) | WARN |
| C12 | Paquetes de AUR (`pacman -Qqm`) con librerías que no resuelven después (`post`: `ldd` sobre los binarios de esos paquetes, solo ELF, "not found") | FAIL en `post` |
| C13 | `post`: driver cargado (`/proc/driver/nvidia/version`) = `nvidia-utils`; `dkms status` todos `installed` para el kernel en uso; `nvidia-smi` responde; `cuInit` = 0 (ctypes); `vulkaninfo --summary` lista NVIDIA y RADV; `vainfo` con NVDEC; `ollama` responde en 127.0.0.1:11434; `hyprctl configerrors` vacío; ningún servicio fallido | FAIL |
| C14 | `post`: `.pacnew` nuevos en `/etc` desde el último snapshot estable | WARN |

## Integración

- `../maintenance/stable-snapshot.sh`: llama `capture.sh --state "$MAN"` (`drivers-state.json`) y exige `compat.py post` sin FAIL
  antes de crear el snapshot (además del reward).
- `../maintenance/smoke-test.sh` + `reward.py`: nuevo grupo `DRIVERS` (peso 15, bloqueante) con `install.sh --check` limpio y
  `compat.py post` sin FAIL; los pesos se reescalan a 100.
- `../restore/bin/omarchy-restore full`: tras restaurar `~`, ejecutar `drivers/install.sh` (real) antes de los demás instaladores.
- Barra: el plugin `juansebas7ian.drivers` puede leer `compat.py preflight --json` (cacheado 1 h) — **opcional, solo si sobra**.
- `~/.claude/omarchy.md` y `~/.claude/agents/omarchy-admin.md`: lo actualiza la sesión principal.

## Ejecución en este equipo (no romper nada)

1. Pruebas unitarias; `capture.sh` (genera manifest y `etc/`); `install.sh --check` debe salir limpio (todo ya está).
2. `install.sh --dry-run` debe decir que no hay nada que hacer.
3. `compat.py post` y `compat.py preflight` (lo que haya pendiente hoy).
4. **Única acción con root**: instalar el gancho de pacman (`install.sh --hook-only`, con snapshot `pre/post`), en una terminal
   visible. Probarlo con una transacción inocua: `sudo pacman -S --needed vulkan-tools` (ya instalado → "nada que hacer", pero el
   gancho no corre si no hay transacción; usar `pacman -S vulkan-tools` reinstalando el mismo paquete, que sí la dispara).
5. Smoke test + reward (maintenance, con el grupo DRIVERS) → evaluación de Opus → snapshot estable.

## Deshacer

`sudo rm /etc/pacman.d/hooks/90-omarchy-compat.hook /usr/local/lib/omarchy/compat-hook /usr/local/lib/omarchy/compat.py`;
el resto son archivos del repo y del estado del usuario. O el snapshot `pre` en Limine.
