#!/usr/bin/env bash
# Run one of this module's commands from the repo's current source:
#   go/tool.sh omarchy-reward smoke.json
# go build is incremental (a no-op build takes ~0.2 s), so scripts in the repo
# never run a stale installed binary. Binaries go to ~/.cache/omarchy-go/.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
name=${1:?usage: tool.sh <command> [args...]}
shift
[ -d "$here/cmd/$name" ] || { echo "tool.sh: no command $name" >&2; exit 3; }
out=${XDG_CACHE_HOME:-$HOME/.cache}/omarchy-go
mkdir -p "$out"
(cd "$here" && go build -o "$out/$name" "./cmd/$name") >&2
exec "$out/$name" "$@"
