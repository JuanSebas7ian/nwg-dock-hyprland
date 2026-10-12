#!/usr/bin/env bash
# Installs the shared agent knowledge base in Obsidian (Agentes/) and wires Claude Code, Pi, opencode and Antigravity
# to it. Idempotent. Backups go to ~/.local/state/agentes-kb/backups/<date>/ (never next to hook or skill folders).
#   install.sh            seed the vault (never overwrites notes), migrate, configure, enable the sync units
#   install.sh --remove   undo the wiring; the vault keeps every note, linked files are copied back as real files
# AGENTES_VAULT overrides the vault path; AGENTES_NO_SYSTEMD=1 skips systemctl (tests).
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
VAULT=${AGENTES_VAULT:-"$HOME/Documents/Obsidian Vault"}
KB="$VAULT/Agentes"
BIN="$HOME/.local/bin/agentes-kb-sync"
UNITS="$HOME/.config/systemd/user"
backup="$HOME/.local/state/agentes-kb/backups/$(date +%Y%m%d-%H%M%S)"
IMPORT_BEGIN="<!-- agentes-kb:begin -->"
IMPORT_END="<!-- agentes-kb:end -->"
say() { echo "agentes-kb: $*"; }
sysd() { [[ ${AGENTES_NO_SYSTEMD:-0} == 1 ]] || systemctl --user "$@"; }
save() { # save <path>: copy into the backup folder keeping the path below $HOME
  [[ -e $1 || -L $1 ]] || return 0
  local rel=${1#"$HOME"/}
  mkdir -p "$backup/$(dirname "$rel")"
  cp -a "$1" "$backup/$rel"
}
json_edit() { # json_edit <file> <jq filter> [jq args...]
  local f=$1 filter=$2; shift 2
  [[ -f $f ]] || return 0
  local tmp; tmp=$(mktemp "$f.XXXXXX")
  jq "$@" "$filter" "$f" >"$tmp"
  if cmp -s "$tmp" "$f"; then rm -f "$tmp"; else save "$f"; chmod --reference="$f" "$tmp"; mv -f "$tmp" "$f"; say "updated $f"; fi
}

remove() {
  sysd disable --now agentes-kb-sync.path agentes-kb-sync.timer 2>/dev/null || true
  rm -f "$UNITS"/agentes-kb-sync.{service,path,timer} "$BIN"
  sysd daemon-reload || true
  # linked files become real copies again, so nothing that points at them breaks
  for l in "$HOME/.claude/omarchy.md" "$HOME"/.claude/skills/* "$HOME"/.agents/skills/*; do
    [[ -L $l ]] || continue
    case $(readlink "$l") in
      "$KB"/*)
        if [[ $l == "$HOME"/.agents/skills/* || ! -e $l ]]; then rm -f "$l"   # dangling: the vault dropped it
        else t=$(readlink -f "$l"); rm -f "$l"; cp -a "$t" "$l"; say "restored real copy $l"; fi ;;
    esac
  done
  rm -f "$HOME/.agents/kb"
  rm -rf "$HOME/.gemini/config/plugins/obsidian-kb"
  for f in "$HOME/.config/opencode/AGENTS.md" "$HOME/.pi/agent/AGENTS.md"; do
    if ! [[ -f $f ]] || ! head -1 "$f" | grep -qF "<!-- GENERADO por agentes-kb-sync"; then continue; fi
    rm -f "$f"
    last=$(ls -1 "$f".bak.* 2>/dev/null | tail -1 || true)
    [[ -n $last ]] && cp -p "$last" "$f" && say "restored $f from $last"
  done
  if [[ -f $HOME/.claude/CLAUDE.md ]] && grep -qF "$IMPORT_BEGIN" "$HOME/.claude/CLAUDE.md"; then
    save "$HOME/.claude/CLAUDE.md"
    sed -i "/$IMPORT_BEGIN/,/$IMPORT_END/d" "$HOME/.claude/CLAUDE.md"
    say "removed the import from ~/.claude/CLAUDE.md"
  fi
  json_edit "$HOME/.claude/settings.json" '.permissions.additionalDirectories |= ((. // []) - [$kb]) | if .permissions.additionalDirectories == [] then del(.permissions.additionalDirectories) else . end' --arg kb "$KB"
  json_edit "$HOME/.config/opencode/opencode.json" 'if .permission.external_directory then .permission.external_directory |= del(.[$a], .[$b]) else . end' --arg a "$KB/**" --arg b "$HOME/.agents/kb/**"
  say "removed (the notes in $KB are untouched)"
}

if [[ ${1:-} == --remove ]]; then remove; exit 0; fi

command -v jq >/dev/null || { echo "agentes-kb: needs jq" >&2; exit 1; }
[[ -d $VAULT ]] || { echo "agentes-kb: no vault at $VAULT" >&2; exit 1; }

# 1. seed: folders and notes that do not exist yet (never overwrites)
mkdir -p "$KB"/{Contexto,Planes,Conclusiones,Skills,Harness} "$VAULT/Plantillas"
for f in LEEME.md Agentes.base Harness/claude.md Harness/pi.md Harness/opencode.md Harness/antigravity.md; do
  [[ -e $KB/$f ]] || { cp "$here/semilla/$f" "$KB/$f"; say "seeded Agentes/$f"; }
done
for f in "$here"/semilla/Plantillas/*.md; do
  [[ -e $VAULT/Plantillas/$(basename "$f") ]] || { cp "$f" "$VAULT/Plantillas/"; say "seeded Plantillas/$(basename "$f")"; }
done

# 2. migrate: the system context and the user's own Claude skills move into the vault, links stay behind
ctx="$HOME/.claude/omarchy.md"
if [[ -f $ctx && ! -L $ctx ]]; then
  if [[ -e $KB/Contexto/omarchy.md ]]; then
    echo "agentes-kb: both $ctx and Agentes/Contexto/omarchy.md exist; merge them by hand" >&2
  else
    save "$ctx"; mv "$ctx" "$KB/Contexto/omarchy.md"; ln -s "$KB/Contexto/omarchy.md" "$ctx"; say "moved $ctx into Agentes/Contexto/"
  fi
fi
for s in "$HOME"/.claude/skills/*/; do
  s=${s%/}; n=$(basename "$s")
  [[ -L $s || $n == synced || ! -f $s/SKILL.md ]] && continue   # Omarchy links and claude.ai-synced skills stay
  if [[ -e $KB/Skills/$n ]]; then echo "agentes-kb: Skills/$n already in the vault; $s left alone" >&2; continue; fi
  save "$s"; mv "$s" "$KB/Skills/$n"; ln -s "$KB/Skills/$n" "$s"; say "moved skill $n into Agentes/Skills/"
done

# 3. Claude Code: import the protocol and allow the vault folder
cm="$HOME/.claude/CLAUDE.md"
if [[ -f $cm ]] && ! grep -qF "$IMPORT_BEGIN" "$cm"; then
  save "$cm"
  printf '\n%s\n## Base de conocimiento compartida (Obsidian)\n\nContexto, planes, conclusiones y skills se escriben en la base compartida con los demás agentes:\n\n@~/.agents/kb/LEEME.md\n@~/.agents/kb/Harness/claude.md\n%s\n' "$IMPORT_BEGIN" "$IMPORT_END" >>"$cm"
  say "added the import to $cm"
fi
[[ -f $HOME/.claude/settings.json ]] && json_edit "$HOME/.claude/settings.json" \
  '.permissions.additionalDirectories = (((.permissions.additionalDirectories // []) + [$kb]) | unique)' --arg kb "$KB"

# 4. opencode: edit the vault folder without asking (omarchy-llm sync keeps this key)
json_edit "$HOME/.config/opencode/opencode.json" \
  '.permission.external_directory = ((.permission.external_directory // {}) + {($a): "allow", ($b): "allow"})' \
  --arg a "$KB/**" --arg b "$HOME/.agents/kb/**"

# 5. hand-written Pi rules are kept in the backup (their content now lives in Harness/pi.md)
[[ -f $HOME/.pi/agent/AGENTS.md ]] && ! head -1 "$HOME/.pi/agent/AGENTS.md" | grep -qF "<!-- GENERADO" && save "$HOME/.pi/agent/AGENTS.md"

# 6. sync now and on every change
install -Dm755 "$here/agentes-kb-sync" "$BIN"
mkdir -p "$UNITS"
install -m644 "$here"/systemd/agentes-kb-sync.{service,path,timer} "$UNITS/"
AGENTES_VAULT="$VAULT" "$BIN"
sysd daemon-reload
sysd enable --now agentes-kb-sync.path agentes-kb-sync.timer
[[ -d $backup ]] && say "backups in $backup"
say "done: knowledge base at $KB (~/.agents/kb)"
