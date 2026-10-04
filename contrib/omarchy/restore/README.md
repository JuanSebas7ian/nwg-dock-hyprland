# Respaldo y autorrestauración

Tres capas, de la más rápida a la más segura:

| Capa | Protege de | Cómo se restaura |
|---|---|---|
| snapper `root` (antes de cada cambio) | un cambio que rompe el sistema | menú de Limine al arrancar |
| snapper `home`, cada hora (6 por hora, 7 diarios, 2 semanales) | borrar archivos o carpetas sin querer | `omarchy-restore file <ruta>` (sin contraseña) |
| restic en Google Drive, diario y cifrado (7 diarios, 4 semanales, 6 mensuales) | perder el disco o el equipo | `omarchy-restore file` o `omarchy-restore full` |

Además, **autorrestauración**: al arrancar, `omarchy-restore heal` repone desde el snapshot más reciente la
configuración que haya desaparecido (Hyprland, la barra, los scripts, los servicios, el contexto de Claude…) y
avisa con una notificación. Solo repone lo que ya no existe; nunca sobrescribe.

```bash
contrib/omarchy/restore/install.sh --snapper   # instala todo y crea el repositorio cifrado en Drive
omarchy-restore list                           # qué se puede restaurar
omarchy-restore file ~/Proyectos/x             # recuperar un archivo o carpeta
systemctl --user start omarchy-backup          # respaldar ya (si no, una vez al día)
```

Qué se respalda: tu carpeta personal entera menos lo que se puede volver a descargar o regenerar (Steam,
cachés, Dropbox, el montaje de Google Drive, `node_modules`, toolchains, modelos de Ollama), más un inventario
del sistema (paquetes, archivos propios de `/etc`, servicios activos) y la copia de configuración de
`backup-omarchy.sh`. Estado: `~/.local/state/omarchy-backup/status.json`; si falla, llega una notificación.

## Reconstruir un equipo desde cero

En un Omarchy recién instalado, con tu cuenta de Google y la contraseña de restic (guardada en el gestor de contraseñas de Google y en papel):

```bash
curl -fsSL https://raw.githubusercontent.com/JuanSebas7ian/nwg-dock-hyprland/feat/dnd-reorder/contrib/omarchy/restore/bin/omarchy-restore -o /tmp/omarchy-restore
bash /tmp/omarchy-restore full
```

Instala rclone y restic, conecta Google Drive (navegador), pide la contraseña, restaura tu carpeta personal en
una carpeta aparte y la copia sin pisar archivos más nuevos, reinstala los paquetes del inventario, vuelve a
ejecutar los instaladores del dock, el validador y la barra, y reactiva los servicios.

**La contraseña de restic es la llave de todo:** está en `~/.config/omarchy-backup/restic-password` y debe estar
también fuera del equipo: en el gestor de contraseñas de Google (passwords.google.com, con verificación en dos pasos y
*cifrado en el dispositivo* activados, porque la copia también está en esa cuenta) y en una copia en papel. Sin ella,
la copia de Drive no se puede leer. En el equipo hay además `~/.config/omarchy-backup/.env` (0600, para usar `restic` a mano)
y una nota en Obsidian; ninguna de las dos sirve si se pierde el disco.
