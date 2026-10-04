# Extras de la barra de Omarchy

Widgets propios para la barra de Omarchy (Quickshell) y colectores para su panel de agentes.
Todo vive en la configuración del usuario: no se toca `/usr/share/omarchy`, así que sobrevive a
`omarchy update`.

```bash
contrib/omarchy/bar/install.sh            # instala o actualiza (idempotente)
contrib/omarchy/bar/install.sh --host     # + ajustes de este equipo (WirePlumber, Ollama 32k; pide sudo)
contrib/omarchy/bar/install.sh --remove   # quita los widgets y colectores
```

## Widgets

| Plugin | Ícono | Clics | Qué muestra |
|---|---|---|---|
| `juansebas7ian.spotify` | Spotify | izq. panel · central pausa · der. siguiente · rueda | **Independiente de la app de Spotify.** La reproducción es spotifyd (dispositivo "Omarchy") por MPRIS: canción con carátula, progreso, aleatorio, repetir, volumen; en frío despierta a spotifyd (`TransferPlayback`) y reanuda o pone la última canción; abrir una lista, álbum o canción usa `OpenUri` de spotifyd (sin API). Pestañas Recientes, Listas, Álbumes y Top del mes leídas de la API con el token de tu propia app (`spotify-bar-setup`), que spotify-player guarda y renueva; se leen directamente porque spotify-player 0.24.1 no tolera los campos que Spotify quitó en 2026 (`tracks`, `popularity`) |
| `juansebas7ian.gdrive` | Drive (↑N = archivos por subir) | izq. panel · der. abrir carpeta | Interruptor de montaje, espacio (Drive · Gmail/Fotos · papelera), caché local, transferencias y cola de subida en vivo, archivos recientes de este PC |
| `juansebas7ian.ollama` | 󰧑 (% mientras descarga) | izq. panel · der. ollama.com | Modelos descargados y cargados, espacio real en disco, buscar en la biblioteca de ollama.com, descargar `nombre:tag`, chatear en terminal, borrar, liberar memoria |
| `juansebas7ian.sysmon` | 󰍛 (rojo si CPU ≥ 85 °C, GPU ≥ 83 °C o RAM ≥ 90 %) | izq. panel · der. btop | CPU (uso, frecuencia, temperatura, carga), RAM y swap, DDR5, ventiladores y temperaturas de la placa (`nct6775`; nombres en `~/.config/omarchy-sysmon/fans.json`), NVIDIA vía NVML (núcleo, ancho de banda de memoria, VRAM, consumo, temperatura, ventilador, PCIe Gen/ancho y tráfico, codificador/decodificador, procesos y su uso), Radeon integrada, NVMe, disco y red |
| `juansebas7ian.storage` | 󰋊 (rojo con < 10 % libre o un aviso de salud) | izq. panel · der. `dua` | Cada disco con sus particiones y la salud NVMe por UDisks2 sin root; qué usa el disco del sistema con clic para entrar en carpetas; **Steam por juego** (juego, prefijo de Proton, shaders, última partida) y herramientas; **salud y firmware** ("Samsung Magician" para Linux): firmware instalado frente al último de Samsung (su página de herramientas) o de LVFS (fwupd), TBW consumidos frente a los garantizados, apagados inseguros, autoprueba SMART (pide contraseña por polkit) y enlace al ISO de firmware; lo disponible; lo que se puede liberar |
| `juansebas7ian.cooling` | 󰔏 + temperatura de la CPU en vivo (rojo a 90 °C o si la bomba se detiene) | izq. panel · der. btop | Gráficas de 2 min de temperatura de la CPU, carga y RPM del enfriador; carga y reloj por hilo; bomba AIO y ventiladores con su papel (`fans.json` → `_roles`: `pump`, `cpu`); Tctl, CCD, socket, placa, DDR5 y GPU. **Notificación crítica si la bomba baja de 500 RPM** |
| `juansebas7ian.nvidia` | GPU (N = actualizaciones de NVIDIA/CUDA/kernel; rojo si falla el driver o CUDA) | izq. panel · der. revisar | Módulo cargado (open/propietario) frente a los paquetes, DKMS por kernel, firmware GSP, modeset; CUDA probado en vivo con `libcuda` (cuInit, dispositivo, compute capability), toolkit y su compatibilidad, cuDNN; actualizaciones de los repos con **notificación** la primera vez que aparecen |
| `juansebas7ian.drivers` | (N = actualizaciones de drivers + BIOS; rojo si falla un driver) | izq. panel · der. revisar de nuevo | Placa y BIOS instalada frente a la última de ASUS (con notas y enlace), salud de `omarchy-hwcheck`, paquetes de drivers con actualización (`checkupdates`, sin root), drivers cargados |

Cada widget es un plugin con `manifest.json`, un `Panel.qml` y un ayudante en Python sin
dependencias que imprime JSON (se puede probar solo, p. ej. `plugins/juansebas7ian.ollama/ollama-ctl.py status`).

## Colectores del panel de agentes

`omarchy.agents` (el ícono de Claude) muestra una pestaña por cada JSON en
`~/.local/state/omarchy/agents/usage/`. `bin/omarchy-agent-usage-antigravity` (tokens de las conversaciones
de Antigravity e IDE; cuota por modelo del servidor local mientras Antigravity está abierto) y
`bin/omarchy-agent-usage-opencode` (tokens por modelo; los locales marcados `· local`; VRAM y modelos de
Ollama como topes) los escribe `omarchy-agent-usage-extra` cada 2 min (`omarchy-agent-usage-extra.timer`).

## Sesión: abrir lo mismo que había al apagar

`bin/omarchy-session` + `systemd/omarchy-session.service`: guarda en cada cambio (eventos de Hyprland, con 3 s de
espera) las ventanas, su escritorio, si flotan con su posición y tamaño, y el escritorio activo, en
`~/.local/state/omarchy-session/session.json`. Al iniciar sesión las vuelve a abrir, una vez por sesión de Hyprland:

- terminales en la misma carpeta (y con `btop`, `nvim`, `lazygit`… si estaban delante);
- web apps de Omarchy con su URL; el resto por su `.desktop` o su línea de comandos;
- apps con varias ventanas (navegadores) se lanzan una vez y cada ventana nueva va al siguiente escritorio guardado
  de su clase.

Omarchy cierra todas las ventanas antes de cerrar sesión, reiniciar o apagar: un cierre masivo congela el guardado
30 s, así que se conserva la sesión de antes (pruebas en `tests/test_session.py`). `omarchy-session show` muestra
qué se abriría; `~/.config/omarchy-session/config.json` admite `enabled`, `restore`, `exclude` (clases) y `rerun`.

## Bluetooth: teclados que piden código

`bin/bt-pair-keyboard ["Wave Keys"]` empareja teclados BLE de Logitech (Wave Keys, MX Keys, K380…) que solo
terminan si escribes en ellos un código de 6 dígitos: lo muestra en grande, y luego marca el teclado como de confianza y lo conecta.

## Servicios

- `rclone-gdrive.service`: monta el remoto `rclone:` en `~/GoogleDrive` con caché VFS completa y un socket
  de control privado (`$XDG_RUNTIME_DIR/rclone-gdrive.sock`) que lee el widget. Otro nombre de remoto:
  `RCLONE_REMOTE=midrive: install.sh`.
- `spotifyd.service` (del paquete `spotifyd`): reproductor de Spotify Connect, configurado en
  `~/.config/spotifyd/spotifyd.conf`.

## Pasos manuales (una vez)

1. Spotify en este PC (Premium): `omarchy pkg add spotifyd spotify-player`, `spotifyd authenticate` y
   `systemctl --user enable --now spotifyd`.
2. Listas en el panel: `spotify-bar-setup` (o "Connect" en el panel): abre el panel de desarrollador de Spotify,
   explica qué poner (Redirect URI `http://127.0.0.1:8989/login`, Web API), guarda tu Client ID en
   `~/.config/spotify-player/app.toml` y autoriza `spotify-player`.

## Notas

- Tras editar un `Panel.qml`, reinicia la barra (`omarchy restart shell`): la recarga en caliente deja viva la
  instancia anterior y la IPC sigue apuntando a ella.
- Los respaldos van a `~/.local/state/omarchy-bar-extras/backups/<fecha>/`, nunca junto a los plugins.
