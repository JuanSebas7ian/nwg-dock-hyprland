#!/usr/bin/env bash
# Pi environment on this machine: web tools backed by a private SearXNG, a safety/stability guard, and settings
# tuned for the local llama.cpp models. Idempotent; backups in ~/.local/state/pi-env/backups/<date>/.
#   install.sh            run the tests, install extensions + SearXNG (user service, Docker), tune settings
#   install.sh --remove   remove the extensions and SearXNG, restore the MCP fetch server (settings keys stay)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
AGENT="$HOME/.pi/agent"
EXT="$AGENT/extensions"
UNITS="$HOME/.config/systemd/user"
backup="$HOME/.local/state/pi-env/backups/$(date +%Y%m%d-%H%M%S)"
say() { echo "pi-env: $*"; }
save() { [[ -e $1 ]] || return 0; mkdir -p "$backup"; cp -a "$1" "$backup/"; }
json_edit() { # json_edit <file> <filter> [jq args]
  local f=$1 filter=$2 tmp; shift 2
  [[ -f $f ]] || return 0
  tmp=$(mktemp "$f.XXXXXX"); jq "$@" "$filter" "$f" >"$tmp"
  if cmp -s "$tmp" "$f"; then rm -f "$tmp"; else save "$f"; chmod --reference="$f" "$tmp"; mv -f "$tmp" "$f"; say "updated $f"; fi
}

if [[ ${1:-} == --remove ]]; then
  systemctl --user disable --now searxng.service 2>/dev/null || true
  rm -f "$UNITS/searxng.service" "$EXT/omarchy-web.ts" "$EXT/omarchy-guard.ts"
  systemctl --user daemon-reload
  json_edit "$AGENT/mcp.json" 'if .mcpServers.fetch then .mcpServers.fetch |= del(.enabled) else . end'
  say "removed (the SearXNG image stays: docker rmi searxng/searxng)"; exit 0
fi

command -v jq >/dev/null && command -v node >/dev/null && command -v docker >/dev/null || { echo "pi-env: needs jq, node and docker" >&2; exit 1; }
node --test "$here/tests/guard.test.ts" >/dev/null || { node --test "$here/tests/guard.test.ts"; exit 1; }

# 1. extensions (Pi loads every .ts in ~/.pi/agent/extensions)
mkdir -p "$EXT"
for f in "$here"/extensions/*.ts; do
  cmp -s "$f" "$EXT/$(basename "$f")" || { save "$EXT/$(basename "$f")"; install -m644 "$f" "$EXT/"; say "installed $(basename "$f")"; }
done

# 2. SearXNG on 127.0.0.1:8888 (started on demand by web_search; enabled so the first search is instant)
mkdir -p "$HOME/.config/searxng"
cmp -s "$here/searxng/settings.yml" "$HOME/.config/searxng/settings.yml" || { save "$HOME/.config/searxng/settings.yml"; install -m644 "$here/searxng/settings.yml" "$HOME/.config/searxng/"; say "installed SearXNG settings"; }
[[ -f $HOME/.config/searxng/env ]] || { (umask 077; echo "SEARXNG_SECRET=$(openssl rand -hex 32)" >"$HOME/.config/searxng/env"); say "created SearXNG secret"; }
cmp -s "$here/searxng/searxng.service" "$UNITS/searxng.service" || { install -Dm644 "$here/searxng/searxng.service" "$UNITS/searxng.service"; say "installed searxng.service"; }
systemctl --user daemon-reload
systemctl --user enable searxng.service >/dev/null 2>&1
systemctl --user restart searxng.service

# 3. settings: compaction sized for the 32k-context model (prompt ~5.7k + 10k kept + 4k reserve leaves room to work)
json_edit "$AGENT/settings.json" '.compaction.modelOverrides["llama-local/qwen3.6-35b-a3b-ud-iq4_xs"] = {"reserveTokens": 4096, "keepRecentTokens": 10000}
  | .retry = ((.retry // {}) + {"enabled": true, "maxRetries": 3})'

# 4. web_fetch replaces the "fetch" MCP server (same job; one tool is clearer for small models)
json_edit "$AGENT/mcp.json" 'if .mcpServers.fetch then .mcpServers.fetch.enabled = false else . end'

for _ in $(seq 40); do curl -sf http://127.0.0.1:8888/healthz >/dev/null && break; sleep 1; done
curl -sf 'http://127.0.0.1:8888/search?q=test&format=json' | jq -e '.results | length > 0' >/dev/null && say "SearXNG answers" || say "warning: SearXNG gave no results yet"
[[ -d $backup ]] && say "backups in $backup"
say "done"
