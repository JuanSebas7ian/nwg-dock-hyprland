#!/usr/bin/env bash
# Run one of this module's commands from the repo's current source:
#   go/tool.sh omarchy-reward smoke.json
#   go/tool.sh --build omarchy-compat      only build; prints the binary's path
# go build is incremental (a no-op build takes ~0.2 s), so scripts in the repo
# never run a stale installed binary. -trimpath makes the output independent
# of where the repo lives. Binaries go to ~/.cache/omarchy-go/.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
build_only=0
if [ "${1:-}" = --build ]; then build_only=1; shift; fi
name=${1:?usage: tool.sh [--build] <command> [args...]}
shift
[ -d "$here/cmd/$name" ] || { echo "tool.sh: no command $name" >&2; exit 3; }
out=${OMARCHY_GO_OUT:-${XDG_CACHE_HOME:-$HOME/.cache}/omarchy-go}
mkdir -p "$out"
(cd "$here" && go build -trimpath -o "$out/$name" "./cmd/$name") >&2
if [ "$build_only" -eq 1 ]; then echo "$out/$name"; exit 0; fi
exec "$out/$name" "$@"
