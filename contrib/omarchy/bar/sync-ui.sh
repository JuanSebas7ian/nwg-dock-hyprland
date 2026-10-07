#!/usr/bin/env bash
# Copia la biblioteca común de componentes (shared/ui) a cada plugin que la usa.
# Omarchy no admite enlaces simbólicos dentro de un plugin, así que cada uno lleva
# su copia; tests/test_shared_ui.py falla si alguna difiere. Edita shared/ui y
# ejecuta esto (install.sh también lo hace).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
for p in "$HERE"/plugins/*/; do
  if [[ -e $p/.uses-shared-ui ]]; then
    rsync -a --delete "$HERE/shared/ui/" "$p/ui/"
  fi
done
