# nwg-dock-hyprland con reordenamiento y selector de apps, para Omarchy

Esta rama (`feat/dnd-reorder`) agrega al dock, detrás de la opción `-dnd`:

- **Reordenar arrastrando:** arrastra un ícono anclado y los demás se corren en vivo. Al soltar dentro
  del dock se guarda el orden; si sueltas fuera, se cancela. Usa eventos simples del mouse, no el
  protocolo de arrastre de Wayland, que con Hyprland congelaba todo el escritorio.
- **"Add app…" (agregar app):** en el clic derecho de cualquier ícono, o en el botón del lanzador.
  Abre una lista buscable de las apps instaladas, que crece hacia arriba según el espacio del monitor:
  un clic ancla o desancla. Se cierra con Esc, con ✕ o al sacar el mouse de la lista 1,5 s.
  Si no hay lanzador instalado (`nwg-drawer`), el clic izquierdo del botón del lanzador también abre la lista.
- **Auto-ocultado ágil (con `-d`):** el dock se oculta medio segundo después de que el mouse sale,
  o a los 5 s si el mouse queda quieto encima. Un menú de clic derecho que no usas se cierra solo
  (0,8 s después de que el mouse se va, nunca antes de 2 s) y el dock se oculta con él; el selector
  se cierra a los 4 s si nunca le pasas el mouse. Mientras arrastras, el dock se queda visible. Es una verificación periódica, porque en Wayland el
  evento de salida a veces no llega (tras un menú o el selector) y el dock se quedaba visible.
- **Vigilante anti-cuelgue:** si el dock deja de responder 10 s, se cierra solo (código 2) para que
  `launch-dock.sh` lo relance.

Sin `-dnd`, el dock se comporta igual que la versión oficial 0.4.11.

## Protección para que un fallo nunca bloquee el sistema

| Capa | Qué hace |
|---|---|
| Sin arrastre de Wayland | El compositor nunca entra en modo arrastre, así que no puede quedar atrapado esperando un drop. |
| Teclado "on-demand" | El selector de apps solo recibe el teclado cuando le haces clic; nunca lo captura en exclusiva. |
| Vigilante interno | Si el bucle de GTK se cuelga 10 s, el dock sale con código 2. Ignora la vuelta de una suspensión. |
| `launch-dock.sh` | Relanza el dock cuando se cae y espera más entre intentos si falla seguido. Tras 5 fallos rápidos con `-dnd`, vuelve al dock oficial. |
| Scope de systemd | El dock corre con `MemoryMax=400M` y `CPUQuota=80%`. Si se descontrola, solo muere el dock. |
| Atajo de emergencia | `SUPER+CTRL+SHIFT+D` lo mata con `kill -9`; el script lo relanza. |
| Guardado atómico | El orden se escribe en un archivo temporal que luego se renombra; nunca queda el archivo de anclados a medias. Una copia `nwg-dock-pinned.dnd.bak` por sesión. |
| Registro | `~/.local/state/nwg-dock/dock.log` (rota a `.log.1` al pasar de 1 MB), además de `journalctl --user -t launch-dock`. |

## Instalar en Omarchy

```bash
omarchy pkg add go nwg-dock-hyprland gtk-layer-shell   # requisitos (nwg-dock-hyprland aporta los íconos y queda de respaldo)
git clone -b feat/dnd-reorder https://github.com/JuanSebas7ian/nwg-dock-hyprland.git
cd nwg-dock-hyprland
contrib/omarchy/install.sh --start
```

El instalador:

1. verifica los requisitos y que la configuración de Hyprland sea la de Omarchy, en Lua;
2. corre las pruebas unitarias y compila el dock en `~/.local/bin/nwg-dock-hyprland-dnd`;
3. instala `launch-dock.sh` en `~/.config/hypr/scripts/` y el estilo cristal en `~/.config/nwg-dock-hyprland/`;
4. agrega a `autostart.lua`, `looknfeel.lua` y `bindings.lua` las líneas de `hypr-snippets.lua`, solo si no están;
5. recarga Hyprland y se detiene si `hyprctl configerrors` reporta algo;
6. con `--start`, lanza el dock ya mismo; sin esa opción, arranca en el próximo inicio de sesión.

Todo archivo que reemplaza o modifica queda respaldado como `<archivo>.bak.<fecha>`. Se puede ejecutar
cuantas veces quieras: para actualizar, `git pull` y de nuevo `contrib/omarchy/install.sh --start`.

Importante: Omarchy configura Hyprland en Lua y **no lee** `hyprland.conf` ni `autostart.conf`;
el dock debe arrancarse desde `autostart.lua`, que es lo que hace el instalador.

## Actualizar el dock tras cambiar el código

```bash
contrib/omarchy/install.sh --start
# o, solo el binario:
go test ./... && go build -o ~/.local/bin/nwg-dock-hyprland-dnd .
pkill -x nwg-dock-hyprla            # launch-dock.sh lo relanza con el binario nuevo
```

## Volver al dock oficial

```bash
mv ~/.local/bin/nwg-dock-hyprland-dnd ~/.local/bin/nwg-dock-hyprland-dnd.off
pkill -f 'bash .*launch-dock.sh$'; pkill -x nwg-dock-hyprla
hyprctl dispatch 'hl.exec_cmd("uwsm-app -- '"$HOME"'/.config/hypr/scripts/launch-dock.sh")'
```

Sin el binario `-dnd`, el script usa `/usr/bin/nwg-dock-hyprland`. El archivo de anclados
(`~/.cache/nwg-dock-pinned`) tiene el mismo formato en los dos docks.

## Respaldo y restauración de toda la configuración de Omarchy

```bash
contrib/omarchy/backup-omarchy.sh          # crea ~/omarchy-backups/omarchy-<fecha>.tar.gz
```

El respaldo incluye `~/.config/hypr`, `~/.config/omarchy`, el estilo del dock, los terminales,
`~/.local/state/omarchy`, las web apps de `~/.local/share/applications`, el binario del dock,
el archivo de anclados y la lista de paquetes instalados. **No se sube a GitHub** porque es
configuración personal: cópialo a otro disco o a la nube.

Para restaurar:

```bash
tar -xzf ~/omarchy-backups/omarchy-<fecha>.tar.gz -C /tmp
less /tmp/omarchy-<fecha>/RESTORE.md       # pasos detallados
cp -a /tmp/omarchy-<fecha>/home/. ~/       # copia la configuración
hyprctl reload && hyprctl configerrors
```

## Validador de drivers y actualizaciones (omarchy-hwcheck)

`hwcheck/` contiene un validador determinista de solo lectura. Escanea el hardware, revisa los drivers
y el firmware, valida las actualizaciones (DKMS/NVIDIA antes de reiniciar) y detecta conflictos
(`pacman -Dk`, `.pacnew`, opciones de módulo contradictorias o sin efecto). Se engancha a `omarchy update`
y al arranque.

```bash
contrib/omarchy/hwcheck/install.sh         # instala ~/.local/bin/omarchy-hwcheck y los hooks
omarchy-hwcheck                            # validación completa
```

Detalle de cada comprobación: [`hwcheck/README.md`](hwcheck/README.md).

## Extras de la barra

`bar/` agrega a la barra de Omarchy widgets de Spotify (sin abrir la app, vía spotifyd), Google Drive
(rclone), Ollama, monitor del sistema (CPU, RAM, temperaturas, GPU con NVML) y drivers (salud, actualizaciones,
BIOS de ASUS), más los colectores de uso de Antigravity y opencode para el panel de agentes.

```bash
contrib/omarchy/bar/install.sh             # widgets, colectores y servicios de usuario
```

Detalle y pasos manuales (Spotify): [`bar/README.md`](bar/README.md).

## Respaldo y autorrestauración

`restore/` agrega snapshots de `/home` cada hora, un respaldo diario cifrado en Google Drive (restic) y
`omarchy-restore`, que recupera archivos, repone sola la configuración que falte al arrancar y reconstruye un
equipo nuevo desde Drive. Detalle: [`restore/README.md`](restore/README.md).

## Diagnóstico

```bash
tail -f ~/.local/state/nwg-dock/dock.log   # salida del dock (con -debug, más detalle)
journalctl --user -t launch-dock -f        # reinicios y su motivo
```
