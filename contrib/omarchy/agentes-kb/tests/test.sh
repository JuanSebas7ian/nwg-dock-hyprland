#!/usr/bin/env bash
# End-to-end tests of install.sh + agentes-kb-sync in a fake HOME (no systemctl, no real vault).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fails=0; n=0
ok() { n=$((n+1)); if eval "$2"; then echo "ok   $1"; else echo "FAIL $1"; fails=$((fails+1)); fi; }

H=$(mktemp -d); trap 'rm -rf "$H"' EXIT
export HOME=$H AGENTES_NO_SYSTEMD=1 AGENTES_VAULT="$H/Documents/Obsidian Vault"
V=$AGENTES_VAULT; KB="$V/Agentes"
mkdir -p "$V/Plantillas" "$V/Privado" "$H/.claude/skills/mine/scripts" "$H/.claude/skills/synced/x" "$H/.pi/agent" \
  "$H/.config/opencode" "$H/.gemini/config/plugins" "$H/.agents/skills" "$H/omarchy-share/omarchy"
echo "# contexto del equipo" >"$H/.claude/omarchy.md"
printf -- '---\nname: mine\ndescription: test skill\n---\nbody\n' >"$H/.claude/skills/mine/SKILL.md"
echo 'echo hi' >"$H/.claude/skills/mine/scripts/run.sh"
printf -- '---\nname: omarchy\ndescription: x\n---\n' >"$H/omarchy-share/omarchy/SKILL.md"
ln -s "$H/omarchy-share/omarchy" "$H/.claude/skills/omarchy"
ln -s "$H/omarchy-share/omarchy" "$H/.agents/skills/omarchy"
printf '# Instrucciones globales\n\n@~/.claude/omarchy.md\n' >"$H/.claude/CLAUDE.md"
echo '{"model":"sonnet","permissions":{"allow":["Bash(ls)"]}}' >"$H/.claude/settings.json"
echo '{"$schema":"x","model":"llama/q","provider":{"llama":{}}}' >"$H/.config/opencode/opencode.json"
echo "# Working rules (hand-written)" >"$H/.pi/agent/AGENTS.md"

"$here/install.sh" >"$H/out1" 2>&1; ok "install exits 0" '[[ $? -eq 0 ]] || { cat "$H/out1"; false; }'
"$here/install.sh" >"$H/out2" 2>&1; ok "second install exits 0" '[[ $? -eq 0 ]]'

ok "seeded protocol and harness notes" '[[ -f $KB/LEEME.md && -f $KB/Harness/pi.md && -f $KB/Agentes.base && -f "$V/Plantillas/Agente - Plan.md" ]]'
ok "stable link ~/.agents/kb" '[[ $(readlink "$H/.agents/kb") == "$KB" ]]'
ok "omarchy.md moved, link left behind" '[[ -L $H/.claude/omarchy.md && $(cat "$H/.claude/omarchy.md") == "# contexto del equipo" && -f $KB/Contexto/omarchy.md ]]'
ok "own skill moved with its scripts" '[[ -f $KB/Skills/mine/scripts/run.sh && $(readlink "$H/.claude/skills/mine") == "$KB/Skills/mine" ]]'
ok "own skill linked for Pi/opencode" '[[ $(readlink "$H/.agents/skills/mine") == "$KB/Skills/mine" ]]'
ok "Omarchy skill links and synced skills untouched" '[[ $(readlink "$H/.claude/skills/omarchy") == "$H/omarchy-share/omarchy" && -d $H/.claude/skills/synced && ! -L $H/.claude/skills/synced ]]'
ok "Pi rules generated without front matter" 'head -1 "$H/.pi/agent/AGENTS.md" | grep -q GENERADO && grep -q "Reglas de trabajo de Pi" "$H/.pi/agent/AGENTS.md" && grep -q "Base de conocimiento" "$H/.pi/agent/AGENTS.md" && ! sed -n 3p "$H/.pi/agent/AGENTS.md" | grep -q "^---$"'
ok "hand-written Pi rules backed up once" '[[ $(ls "$H"/.pi/agent/AGENTS.md.bak.* | wc -l) -eq 1 ]]'
ok "opencode global rules generated" 'grep -q "Reglas de opencode" "$H/.config/opencode/AGENTS.md"'
ok "opencode permission added, other keys kept" '[[ $(jq -r --arg k "$KB/**" ".permission.external_directory[\$k]" "$H/.config/opencode/opencode.json") == allow && $(jq -r .model "$H/.config/opencode/opencode.json") == llama/q ]]'
ok "Antigravity plugin with rules and skills" '[[ -f $H/.gemini/config/plugins/obsidian-kb/plugin.json ]] && grep -q "Reglas de Antigravity" "$H/.gemini/config/plugins/obsidian-kb/rules/AGENTS.md" && [[ -f $H/.gemini/config/plugins/obsidian-kb/skills/mine/SKILL.md ]]'
ok "CLAUDE.md imports the protocol exactly once" '[[ $(grep -c "@~/.agents/kb/LEEME.md" "$H/.claude/CLAUDE.md") -eq 1 ]] && grep -q "@~/.claude/omarchy.md" "$H/.claude/CLAUDE.md"'
ok "Claude may use the vault folder, permissions kept" '[[ $(jq -r ".permissions.additionalDirectories[0]" "$H/.claude/settings.json") == "$KB" && $(jq -r ".permissions.allow[0]" "$H/.claude/settings.json") == "Bash(ls)" ]]'
ok "Privado untouched" '[[ -z $(ls -A "$V/Privado") ]]'

sync="$H/.local/bin/agentes-kb-sync"
echo "- regla nueva de prueba" >>"$KB/LEEME.md"
"$sync" >/dev/null 2>&1
ok "edit in Obsidian reaches every harness" 'grep -q "regla nueva de prueba" "$H/.pi/agent/AGENTS.md" && grep -q "regla nueva de prueba" "$H/.config/opencode/AGENTS.md" && grep -q "regla nueva de prueba" "$H/.gemini/config/plugins/obsidian-kb/rules/AGENTS.md"'
ok "sync is idempotent (silent second run)" '[[ -z $("$sync" 2>&1) ]]'
mkdir -p "$KB/Skills/nueva"; printf -- '---\nname: nueva\ndescription: d\n---\n' >"$KB/Skills/nueva/SKILL.md"
"$sync" >/dev/null 2>&1
ok "new vault skill appears in Claude and ~/.agents" '[[ -L $H/.claude/skills/nueva && -L $H/.agents/skills/nueva ]]'
rm -r "$KB/Skills/nueva"; "$sync" >/dev/null 2>&1
ok "deleted vault skill: stale links removed" '[[ ! -L $H/.claude/skills/nueva && ! -L $H/.agents/skills/nueva ]]'
mkdir -p "$KB/Skills/mal"; printf -- '---\nname: otro\ndescription: d\n---\n' >"$KB/Skills/mal/SKILL.md"
ok "warns when name: and folder disagree" '"$sync" 2>&1 | grep -q "should be .mal."'
rm -r "$KB/Skills/mal"
mkdir -p "$KB/Skills/yaml"; printf -- '---\nname: yaml\ndescription: Builds things. Triggers: latex, cv\n---\n' >"$KB/Skills/yaml/SKILL.md"
ok "warns on unquoted description with ': '" '"$sync" 2>&1 | grep -q "invalid YAML"'
printf -- '---\nname: yaml\ndescription: >\n  Builds things. Triggers: latex, cv\n---\n' >"$KB/Skills/yaml/SKILL.md"
ok "folded description passes" '! "$sync" 2>&1 | grep -q "invalid YAML"'
rm -r "$KB/Skills/yaml"

"$here/install.sh" --remove >"$H/out3" 2>&1; ok "remove exits 0" '[[ $? -eq 0 ]] || { cat "$H/out3"; false; }'
ok "remove: omarchy.md is a real file again" '[[ -f $H/.claude/omarchy.md && ! -L $H/.claude/omarchy.md && $(head -1 "$H/.claude/omarchy.md") == "# contexto del equipo" ]]'
ok "remove: skill is a real copy again" '[[ -f $H/.claude/skills/mine/scripts/run.sh && ! -L $H/.claude/skills/mine ]]'
ok "remove: Pi rules restored" '[[ $(cat "$H/.pi/agent/AGENTS.md") == "# Working rules (hand-written)" ]]'
ok "remove: generated and plugin gone, import removed" '[[ ! -e $H/.config/opencode/AGENTS.md && ! -e $H/.gemini/config/plugins/obsidian-kb && ! -e $H/.agents/kb && ! -L $H/.agents/skills/mine ]] && ! grep -q agentes-kb "$H/.claude/CLAUDE.md"'
ok "remove: permissions cleaned, notes kept" '[[ $(jq -r ".permissions.additionalDirectories // \"none\"" "$H/.claude/settings.json") == none && $(jq -r ".permission.external_directory | length" "$H/.config/opencode/opencode.json") == 0 && -f $KB/Contexto/omarchy.md ]]'
ok "remove: Omarchy skill link untouched" '[[ $(readlink "$H/.claude/skills/omarchy") == "$H/omarchy-share/omarchy" ]]'

echo "$((n-fails))/$n passed"; ((fails == 0))
