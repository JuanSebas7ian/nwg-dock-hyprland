#!/usr/bin/env bash
# Respaldo de la configuración de usuario de Omarchy y del dock -dnd.
# Crea ~/omarchy-backups/omarchy-<fecha>.tar.gz con las rutas de abajo (las que existan),
# la lista de paquetes instalados y un RESTORE.md con los pasos para restaurar.
# Uso: backup-omarchy.sh [carpeta-destino]
set -euo pipefail

DEST=${1:-$HOME/omarchy-backups}
STAMP=$(date +%Y%m%d-%H%M%S)
NAME=omarchy-$STAMP
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

PATHS=(
  .config/hypr
  .config/omarchy
  .config/nwg-dock-hyprland
  .config/alacritty .config/foot .config/kitty .config/ghostty
  .config/btop .config/fastfetch .config/lazygit .config/starship.toml .config/git
  .config/systemd/user .config/gtk-3.0 .config/gtk-4.0 .config/mimeapps.list
  .config/uwsm .config/environment.d
  .local/state/omarchy
  .local/share/applications
  .local/bin/nwg-dock-hyprland-dnd
  .cache/nwg-dock-pinned
  # audio, opencode, colectores de la barra y validador de drivers
  .config/wireplumber .config/opencode/opencode.json .config/opencode/tui.json .config/spotifyd
  .local/bin/omarchy-agent-usage-antigravity .local/bin/omarchy-agent-usage-opencode
  .local/bin/omarchy-agent-usage-extra .local/bin/omarchy-hwcheck
  .local/state/omarchy-hwcheck
  # spotify, sesión, sensores y respaldo (sin la contraseña de restic)
  .config/spotify-player/app.toml .config/omarchy-sysmon .config/omarchy-session .config/gamemode.ini .config/MangoHud
  .config/omarchy-backup/config .config/omarchy-backup/excludes
  .local/bin/omarchy-session .local/bin/omarchy-backup .local/bin/omarchy-restore
  .local/bin/bt-pair-keyboard .local/bin/spotify-bar-setup
  .bashrc .bash_profile .profile
  # contexto, skill y agente de Claude para este equipo
  .claude/CLAUDE.md .claude/omarchy.md .claude/skills/omarchy-hardware .claude/agents
)

cd "$HOME"
existing=()
for p in "${PATHS[@]}"; do
  [[ -e $p ]] && existing+=("$p")
done

mkdir -p "$WORK/$NAME/meta"
pacman -Qqe > "$WORK/$NAME/meta/packages-explicit.txt"
pacman -Qqem > "$WORK/$NAME/meta/packages-aur.txt" || true
{
  echo "fecha: $(date -Iseconds)"
  echo "host: $(hostname)"
  echo "omarchy: $(omarchy version 2>/dev/null || pacman -Q omarchy 2>/dev/null || echo desconocida)"
  echo "hyprland: $(hyprctl version 2>/dev/null | head -1 || echo desconocida)"
  echo "nwg-dock-hyprland: $(pacman -Q nwg-dock-hyprland 2>/dev/null || echo no instalado)"
  echo "rutas: ${existing[*]}"
} > "$WORK/$NAME/meta/info.txt"

cat > "$WORK/$NAME/RESTORE.md" <<'EOF'
# Restaurar este respaldo de Omarchy

Contiene la configuración de usuario (no los paquetes). Rutas relativas a `$HOME`, en `home/`.

1. Si Omarchy no está instalado, instálalo primero (https://omarchy.org) e inicia sesión una vez.
2. Reinstala los paquetes que faltan (opcional):
   `sudo pacman -S --needed - < meta/packages-explicit.txt`
   Los de AUR (`meta/packages-aur.txt`) se instalan con `omarchy pkg aur add <paquete>`.
3. Guarda tu configuración actual por si acaso:
   `tar -czf ~/antes-de-restaurar-$(date +%s).tar.gz -C ~ .config/hypr .config/omarchy .config/nwg-dock-hyprland 2>/dev/null`
4. Copia los archivos del respaldo a tu home:
   `cp -a home/. ~/`
5. Recarga Hyprland y valida: `hyprctl reload && hyprctl configerrors`
6. Relanza el dock (o cierra sesión y vuelve a entrar):
   `hyprctl dispatch 'hl.exec_cmd("uwsm-app -- '"$HOME"'/.config/hypr/scripts/launch-dock.sh")'`
   (el mensaje "expected a dispatcher" es normal; el comando sí se ejecuta).

Si el binario `~/.local/bin/nwg-dock-hyprland-dnd` no funciona en el sistema nuevo, recompílalo desde
https://github.com/JuanSebas7ian/nwg-dock-hyprland (rama feat/dnd-reorder), ver contrib/omarchy/README.md.
EOF

mkdir -p "$WORK/$NAME/home"
cp -a --parents "${existing[@]}" "$WORK/$NAME/home/"

mkdir -p "$DEST"
tar -czf "$DEST/$NAME.tar.gz" -C "$WORK" "$NAME"
(cd "$DEST" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
echo "Respaldo: $DEST/$NAME.tar.gz ($(du -h "$DEST/$NAME.tar.gz" | cut -f1))"
