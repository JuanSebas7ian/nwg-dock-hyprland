#!/usr/bin/env bash
# Genera las imágenes del tema de rEFInd (paleta Tokyo Night de Omarchy) en refind/themes/omarchy/.
# Requiere ImageMagick (magick) y la fuente JetBrains Mono Nerd Font. Las imágenes generadas se versionan.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
OUT=$HERE/refind/themes/omarchy
BG='#1a1b26' FG='#c0caf5' GREEN='#9ece6a' BLUE='#7aa2f7' DIM='#565f89'
FONT=$(fc-match -f '%{file}' 'JetBrainsMono Nerd Font:style=Regular')
FONTB=$(fc-match -f '%{file}' 'JetBrainsMono Nerd Font:style=Bold')
mkdir -p "$OUT"

# Fondo 2560x1440: color del tema, título y pie discretos.
magick -size 2560x1440 "xc:$BG" \
  -fill "$FG" -font "$FONTB" -pointsize 54 -gravity north -annotate +0+190 'Elige un sistema' \
  -fill "$DIM" -font "$FONT" -pointsize 30 -gravity south \
  -annotate +0+120 'Omarchy arranca solo en unos segundos  ·  Tab / F2 sobre Omarchy: snapshots y opciones' \
  "$OUT/background.png"

# Ícono de Omarchy: el logo oficial (verde) centrado en 256x256.
magick /usr/share/omarchy/icon.png -resize 176x176 -background none -gravity center -extent 256x256 "$OUT/os_omarchy.png"

# Ícono de Windows: cuatro cuadros en el azul del tema.
magick -size 256x256 xc:none -fill "$BLUE" \
  -draw 'rectangle 40,40 124,124' -draw 'rectangle 132,40 216,124' \
  -draw 'rectangle 40,132 124,216' -draw 'rectangle 132,132 216,216' "$OUT/os_windows.png"

# Ícono de Limine (submenú de snapshots): un reloj de historial con la Nerd Font.
magick -size 256x256 xc:none -fill "$GREEN" -font "$FONT" -pointsize 190 -gravity center \
  -annotate +0+0 "$(printf '\U000F02DA')" "$OUT/os_snapshots.png"

# Marcos de selección: tarjeta redondeada translúcida.
magick -size 288x288 xc:none -fill "rgba(122,162,247,0.18)" -stroke "$BLUE" -strokewidth 4 \
  -draw 'roundrectangle 4,4 283,283 36,36' "$OUT/selection_big.png"
magick -size 64x64 xc:none -fill "rgba(122,162,247,0.25)" -stroke "$BLUE" -strokewidth 2 \
  -draw 'roundrectangle 2,2 61,61 12,12' "$OUT/selection_small.png"

# Fuente de rEFInd: PNG de una fila con los glifos 32-126 + uno extra, todos del mismo ancho.
cell_w=17 cell_h=34
tmp=$(mktemp -d)
for code in $(seq 32 127); do
  if ((code == 127)); then ch='?'; else ch=$(printf "\\$(printf '%03o' "$code")"); fi
  magick -size ${cell_w}x${cell_h} xc:none -fill "$FG" -font "$FONT" -pointsize 28 -gravity center \
    -annotate +0+0 "$ch" "$tmp/$(printf '%03d' "$code").png"
done
magick "$tmp"/*.png +repage +append +repage "$OUT/font.png"
rm -r "$tmp"
ls -la "$OUT"
