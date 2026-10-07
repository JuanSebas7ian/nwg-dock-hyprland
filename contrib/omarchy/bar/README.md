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
| `juansebas7ian.cloud` | 󰅟 (↑N = archivos por subir en total; rojo = un servicio falló o la sesión de Apple caduca en ≤ 3 días) | izq. panel · der. abrir `~/GoogleDrive` | **Pestañas** Resumen (avisos y una tarjeta por servicio) · **Drive** (interruptor de montaje, espacio Drive · Gmail/Fotos · papelera, caché, transferencias y cola de subida, recientes) · **iCloud** (conectar y renovar la sesión de 30 días, montaje, documentos con su estado ☁ ◐ ✓ ⏳ ↑ y descarga, iCloud Fotos, recientes) · **Photos** (Google Fotos: pausa, progreso, orígenes con interruptor, subidas recientes) · **Dropbox** (pausa, espacio, recientes; sustituye a `omarchy.dropbox`). `backend/cloud.py status` consulta los cuatro en un proceso (~0,5 s); las acciones van a cada CLI (`gdrive.py`, `icloud.py`, `gphotos-sync`, `dropbox-cli`) |
| `juansebas7ian.ollama` | 󰧑 (% mientras descarga) | izq. panel · der. ollama.com | Modelos descargados y cargados, espacio real en disco, buscar en la biblioteca de ollama.com, descargar `nombre:tag`, chatear en terminal, borrar, liberar memoria |
| `juansebas7ian.hardware` | 󰔏 + temperatura de la CPU (rojo con cualquier alerta; N = actualizaciones de drivers + BIOS) | izq. panel · der. btop | **Pestañas** Summary (alertas y tarjetas: CPU, GPU, RAM, bomba, disco, drivers, Bluetooth, baterías) · **CPU** (gráficas de 2 min de temperatura, carga y enfriador; hilos; memoria; bomba y ventiladores con su papel; todas las temperaturas; disco y red) · **GPU** (NVML en vivo, procesos, Radeon, driver, DKMS por kernel, GSP, CUDA probado con `libcuda`, cuDNN) · **Disks** (discos y salud NVMe, qué ocupa con navegación por carpetas, Steam por juego, firmware y TBW, disponible, qué liberar) · **Drivers** (BIOS frente a ASUS, `omarchy-hwcheck`, actualizaciones, drivers cargados) · **Devices** (Bluetooth con batería, conectar, reconexión al arrancar por dispositivo, visibilidad; baterías Logitech por solaar; teclados, ratones y mandos con `pad-keepalive`; audio; cámaras; árbol USB con los errores del arranque). Teclas: h/l o 1-6 pestañas, j/k desplazar, r revisar, b btop |

Cada widget es un plugin con `manifest.json`, un `Panel.qml` y un ayudante en Python sin
dependencias que imprime JSON (se puede probar solo, p. ej. `plugins/juansebas7ian.ollama/ollama-ctl.py status`).

### Hardware y Nube: estructura

```
plugins/juansebas7ian.hardware/
  Panel.qml            ícono, estado, pestañas, teclado; habla con el backend por stdin/stdout
  tabs/*Tab.qml        una pestaña por archivo, cargada solo al abrirla (recibe el panel como `hw`)
  backend/hardwared.py un único proceso para todo: sensores cada 1 s (los emite cada 1 s solo con una
                       pestaña en vivo abierta, si no cada 5 s); du, checkupdates y solaar en hilos y
                       solo para la pestaña abierta; CUDA en un proceso corto; alertas en un solo sitio
  backend/hw/          sensors, gpu, drivers, storage, peripherals, alerts, util (pruebas: tests/test_hardware.py)
plugins/juansebas7ian.cloud/
  Panel.qml, tabs/*Tab.qml (recibe el panel como `cloud`), backend/cloud.py (+ gdrive.py, icloud.py)
shared/ui/             componentes comunes (Theme, Section, Pair, Gauge, Graph, Tile, TabBar, FileRow…)
sync-ui.sh             copia shared/ui a cada plugin con `.uses-shared-ui` (Omarchy no admite enlaces
                       simbólicos en un plugin); una prueba falla si alguna copia difiere
```

Umbrales de las alertas de Hardware (opcional) en `~/.config/omarchy-hardware/config.json`:
`{"cpuWarn": 85, "cpuCrit": 90, "gpuWarn": 83, "pumpMinRpm": 500, "ramWarn": 0.9, "diskFreeWarn": 0.1,
"batteryWarn": 15, "notify": true}`. Notifica las críticas (bomba parada, CPU ≥ cpuCrit, SMART) y las
baterías bajas una sola vez hasta que se resuelven; las actualizaciones de NVIDIA/CUDA/kernel, una vez por versión.
Nombres y papeles de los ventiladores: `~/.config/omarchy-sysmon/fans.json` (ruta de antes, por compatibilidad).

Pruebas: `cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_hardware tests.test_icloud tests.test_gphotos tests.test_session`.
Capturas sin ratón: `omarchy-shell juansebas7ian.hardware tab cpu` (o `juansebas7ian.cloud tab icloud`) y `grim`.

Reemplazan (2026-10-07) a `gdrive`, `icloud`, `gphotos`, `sysmon`, `cooling`, `storage`, `nvidia`, `drivers` y
`omarchy.dropbox`: `install.sh` los retira con respaldo en `~/.local/state/omarchy-bar-extras/backups/<fecha>/`.

## Colectores del panel de agentes

`omarchy.agents` (el ícono de Claude) muestra una pestaña por cada JSON en
`~/.local/state/omarchy/agents/usage/`. `bin/omarchy-agent-usage-antigravity` (tokens de las conversaciones
de Antigravity e IDE; cuota por modelo del servidor local mientras Antigravity está abierto) y
`bin/omarchy-agent-usage-opencode` (tokens por modelo; los locales marcados `· local`; VRAM y modelos de
Ollama como topes) los escribe `omarchy-agent-usage-extra` cada 2 min (`omarchy-agent-usage-extra.timer`).

## opencode: los modelos de Ollama, siempre al día

`bin/opencode-ollama-sync` escribe en `~/.config/opencode/opencode.json` (bloque `provider.ollama.models`) los
modelos que Ollama tiene de verdad: lee `/api/tags` y `/api/show`, omite los de solo embeddings, pone
`tool_call`, `reasoning`, visión y `limit` (contexto = mínimo entre el modelo y `OLLAMA_CONTEXT_LENGTH` del
servicio, salida = un cuarto, máx. 8192). Conserva los nombres que les pongas y cualquier otra clave; quita los
modelos borrados; con Ollama apagado no toca nada; respaldo diario `opencode.json.bak.<fecha>`.
`opencode-ollama-sync.path` lo ejecuta al descargar o borrar un modelo (vigila `/var/lib/ollama/blobs` y los
manifiestos) y `opencode-ollama-sync.timer` al iniciar sesión. `--dry-run` muestra el cambio.
Pruebas: `python3 -m unittest tests.test_opencode_sync`.

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
- `rclone-icloud.service`: monta el remoto `icloud:` (tipo `iclouddrive`, rclone ≥ 1.69) en `~/iCloud`, caché VFS completa
  (máx. 20 GB, 7 días) y socket `$XDG_RUNTIME_DIR/rclone-icloud.sock`. iCloud no avisa de cambios: las carpetas se releen cada 5 min.
  `rclone-icloud-photos.service` (opcional, desde el panel o `icloud-setup photos`): iCloud Fotos **solo lectura** en `~/iCloudPhotos`.
  Se instalan siempre; el montaje se activa cuando existe el remoto. Limitaciones de Apple: contraseña normal (no las de app),
  "Acceder a los datos de iCloud en la web" activado en el iPhone, y la sesión caduca a los 30 días (`icloud-setup reconnect`;
  la fecha queda en `~/.local/state/omarchy-icloud/authenticated`). Con Protección de datos avanzada, `Missing X-APPLE-WEBAUTH-TOKEN cookie`
  suele ser que hay condiciones de iCloud sin aceptar: entrar en icloud.com, aceptarlas y `icloud-setup reconnect` (rclone #9658/#9919).
  Tras 3 fallos en 15 min el montaje deja de reintentar (no insiste ante Apple). Pruebas: `python3 -m unittest tests.test_icloud`.
- `gphotos-sync.{service,timer,path}` + `~/.local/bin/gphotos-sync`: sube a Google Fotos (remoto `gphotos:`, tipo `googlephotos`)
  `~/GoogleFotos` (subcarpeta = álbum), las capturas (`~/Pictures`, sin subcarpetas → "Capturas de pantalla"), `~/Dropbox/Cargas de cámara`
  ("Cámara <año>") y las fotos y videos de Google Drive (`rclone:`, "Drive · <carpeta>", sin `Backups/`). Cada 30 min, 10 min tras
  arrancar y al momento cuando cambia `~/GoogleFotos` o `~/Pictures`. **Solo sube**: desde el 2025-03-31 la API de Google Fotos no deja
  leer la biblioteca salvo lo que subió la propia app, así que lo subido se anota en `~/.local/state/gphotos-sync/uploaded.jsonl`
  y nunca se vuelve a enviar (borrar una línea = volver a subir ese archivo). Ancho de banda limitado a 2,5 MB/s de 08:00 a 23:30
  (`bwlimit` en `~/.config/gphotos-sync/config.json`, junto con los orígenes y su álbum). Si Google corta por cuota diaria, el estado
  pasa a `quota` y la siguiente pasada sigue. `gphotos-sync plan` muestra qué falta por álbum. Requiere **client ID propio**
  (el compartido de rclone se retira en 2026): `gphotos-setup` guía su creación (proyecto, API Photos Library, pantalla de
  consentimiento **publicada** — en "Prueba" el permiso caduca a los 7 días —, cliente de escritorio). Pruebas:
  `python3 -m unittest tests.test_gphotos` (remotos `alias` de rclone hacia carpetas temporales, sin Google).
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
