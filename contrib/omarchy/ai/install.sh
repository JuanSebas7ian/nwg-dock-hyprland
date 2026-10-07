#!/usr/bin/env bash
# Instala (o reproduce) el entorno de IA en ~/ai desde este directorio del fork.
#   contrib/omarchy/ai/install.sh            ai-lab + unsloth, kernels de Jupyter, verificación
#   contrib/omarchy/ai/install.sh --relock   vuelve a resolver versiones (uv lock --upgrade) y guarda los uv.lock aquí
# Sin root. Lo de sistema (llama.cpp CUDA, nvtop) va aparte: omarchy pkg add llama-cpp ggml-cuda nvtop
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
AI=${AI_HOME:-$HOME/ai}
RELOCK=false
[[ ${1:-} == --relock ]] && RELOCK=true

say() { printf '==> %s\n' "$*"; }
command -v uv >/dev/null || { echo "falta uv (omarchy pkg add uv)" >&2; exit 1; }
uv python install 3.12 >/dev/null

sync_env() { # src-dir dest-dir kernel-name display-name
  local src=$1 dest=$2
  mkdir -p "$dest"
  install -m 644 "$src/pyproject.toml" "$dest/pyproject.toml"
  if [[ -f $src/uv.lock && $RELOCK == false ]]; then
    install -m 644 "$src/uv.lock" "$dest/uv.lock"
    say "$dest: uv sync --locked"
    (cd "$dest" && uv sync --locked --python 3.12)
  else
    say "$dest: resolviendo versiones (uv lock$($RELOCK && echo ' --upgrade'))"
    (cd "$dest" && uv lock --python 3.12 $($RELOCK && echo --upgrade) && uv sync --locked --python 3.12)
    install -m 644 "$dest/uv.lock" "$src/uv.lock"
    say "uv.lock guardado en $src (haz commit)"
  fi
  "$dest/.venv/bin/python" -m ipykernel install --user --name "$3" --display-name "$4" >/dev/null
  say "kernel de Jupyter: $4"
}

sync_env "$HERE" "$AI" ai-lab "AI Lab (CUDA)"
sync_env "$HERE/unsloth-env" "$AI/unsloth-env" ai-unsloth "Unsloth (CUDA)"
install -m 644 "$HERE/verify.py" "$AI/verify.py"
mkdir -p "$AI/projects"

say "Verificación ai-lab"
"$AI/.venv/bin/python" "$AI/verify.py"
say "Verificación unsloth"
"$AI/unsloth-env/.venv/bin/python" "$AI/verify.py"
