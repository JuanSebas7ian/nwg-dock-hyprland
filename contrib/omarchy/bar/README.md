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
| `juansebas7ian.spotify` | Spotify | izq. panel · central pausa · der. siguiente · rueda | Canción con carátula y progreso (clic para saltar), controles, aleatorio/repetir, volumen, dispositivos ("Omarchy" = spotifyd en este PC), cola, escuchado recientemente, favoritos del mes y tus listas. Sin cuenta conectada usa MPRIS (solo con la app abierta) |
| `juansebas7ian.gdrive` | Drive (↑N = archivos por subir) | izq. panel · der. abrir carpeta | Interruptor de montaje, espacio (Drive · Gmail/Fotos · papelera), caché local, transferencias y cola de subida en vivo, archivos recientes de este PC |
| `juansebas7ian.ollama` | 󰧑 (% mientras descarga) | izq. panel · der. ollama.com | Modelos descargados y cargados, espacio real en disco, buscar en la biblioteca de ollama.com, descargar `nombre:tag`, chatear en terminal, borrar, liberar memoria |
| `juansebas7ian.sysmon` | 󰍛 (rojo si CPU ≥ 85 °C, GPU ≥ 83 °C o RAM ≥ 90 %) | izq. panel · der. btop | CPU (uso, frecuencia, temperatura, carga), RAM y swap, DDR5, NVIDIA vía NVML (núcleo, ancho de banda de memoria, VRAM, consumo, temperatura, ventilador, PCIe Gen/ancho y tráfico, codificador/decodificador, procesos y su uso), Radeon integrada, NVMe, disco y red |
| `juansebas7ian.drivers` | (N = actualizaciones de drivers + BIOS; rojo si falla un driver) | izq. panel · der. revisar de nuevo | Placa y BIOS instalada frente a la última de ASUS (con notas y enlace), salud de `omarchy-hwcheck`, paquetes de drivers con actualización (`checkupdates`, sin root), drivers cargados |

Cada widget es un plugin con `manifest.json`, un `Panel.qml` y un ayudante en Python sin
dependencias que imprime JSON (se puede probar solo, p. ej. `plugins/juansebas7ian.ollama/ollama-ctl.py status`).

## Colectores del panel de agentes

`omarchy.agents` (el ícono de Claude) muestra una pestaña por cada JSON en
`~/.local/state/omarchy/agents/usage/`. `bin/omarchy-agent-usage-antigravity` (tokens de las conversaciones
de Antigravity e IDE; cuota por modelo del servidor local mientras Antigravity está abierto) y
`bin/omarchy-agent-usage-opencode` (tokens por modelo; los locales marcados `· local`; VRAM y modelos de
Ollama como topes) los escribe `omarchy-agent-usage-extra` cada 2 min (`omarchy-agent-usage-extra.timer`).

## Servicios

- `rclone-gdrive.service`: monta el remoto `rclone:` en `~/GoogleDrive` con caché VFS completa y un socket
  de control privado (`$XDG_RUNTIME_DIR/rclone-gdrive.sock`) que lee el widget. Otro nombre de remoto:
  `RCLONE_REMOTE=midrive: install.sh`.
- `spotifyd.service` (del paquete `spotifyd`): reproductor de Spotify Connect, configurado en
  `~/.config/spotifyd/spotifyd.conf`.

## Pasos manuales (una vez)

1. Spotify en este PC (Premium): `omarchy pkg add spotifyd`, `spotifyd authenticate` y
   `systemctl --user enable --now spotifyd`.
2. Panel de Spotify: crea una app en <https://developer.spotify.com/dashboard> (Web API, Redirect URI
   `http://127.0.0.1:8898/callback`), guarda el Client ID en `~/.config/spotify-bar/client_id` y pulsa
   "Connect Spotify". El token queda en `~/.config/spotify-bar/token.json` (0600). Spotify cerró a las apps
   nuevas las recomendaciones y las listas editoriales; el panel usa lo reciente, los favoritos y tus listas.

## Notas

- Tras editar un `Panel.qml`, reinicia la barra (`omarchy restart shell`): la recarga en caliente deja viva la
  instancia anterior y la IPC sigue apuntando a ella.
- Los respaldos van a `~/.local/state/omarchy-bar-extras/backups/<fecha>/`, nunca junto a los plugins.
