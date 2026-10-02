# nwg-dock-hyprland con reordenamiento y selector de apps, para Omarchy

Esta rama (`feat/dnd-reorder`) agrega al dock, detrás de la opción `-dnd`:

- **Reordenar arrastrando:** arrastra un ícono anclado y los demás se corren en vivo. Al soltar dentro
  del dock se guarda el orden; si sueltas fuera, se cancela. Usa eventos simples del mouse, no el
  protocolo de arrastre de Wayland, que con Hyprland congelaba todo el escritorio.
- **"Add app…" (agregar app):** en el clic derecho de cualquier ícono, o en el botón del lanzador.
  Abre una lista buscable de las apps instaladas: un clic ancla o desancla. Esc o un clic fuera la cierra.
  Si no hay lanzador instalado (`nwg-drawer`), el clic izquierdo del botón del lanzador también abre la lista.
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
git clone -b feat/dnd-reorder https://github.com/JuanSebas7ian/nwg-dock-hyprland.git
cd nwg-dock-hyprland
go test ./...                       # pruebas unitarias
go build -o ~/.local/bin/nwg-dock-hyprland-dnd .

# el paquete oficial aporta los íconos y queda como respaldo
omarchy pkg add nwg-dock-hyprland

mkdir -p ~/.config/hypr/scripts ~/.config/nwg-dock-hyprland
cp contrib/omarchy/launch-dock.sh ~/.config/hypr/scripts/
cp contrib/omarchy/style.css ~/.config/nwg-dock-hyprland/style.css
```

Después agrega los tres bloques de `contrib/omarchy/hypr-snippets.lua` al final de `autostart.lua`,
`looknfeel.lua` y `bindings.lua` (en `~/.config/hypr/`), valida con
`hyprctl reload && hyprctl configerrors` y cierra sesión y vuelve a entrar,
o lanza el dock ya mismo con:

```bash
hyprctl dispatch 'hl.exec_cmd("uwsm-app -- '"$HOME"'/.config/hypr/scripts/launch-dock.sh")'
```

(El mensaje "expected a dispatcher" es normal; el comando sí se ejecuta.)

Importante: Omarchy configura Hyprland en Lua y **no lee** `hyprland.conf` ni `autostart.conf`;
el dock debe arrancarse desde `autostart.lua`.

## Actualizar el dock tras cambiar el código

```bash
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

## Diagnóstico

```bash
tail -f ~/.local/state/nwg-dock/dock.log   # salida del dock (con -debug, más detalle)
journalctl --user -t launch-dock -f        # reinicios y su motivo
```
