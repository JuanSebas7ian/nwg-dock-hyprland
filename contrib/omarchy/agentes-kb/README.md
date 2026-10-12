# agentes-kb: base de conocimiento compartida en Obsidian

Claude Code, Pi, opencode y Antigravity escriben contexto, planes, conclusiones y skills en una sola carpeta de la
bóveda de Obsidian (`~/Documents/Obsidian Vault/Agentes`, ruta estable `~/.agents/kb`). La bóveda manda: aquí solo
están el instalador, el sincronizador y las notas semilla.

| Programa | Cómo la reconoce |
|---|---|
| Claude Code | `@~/.agents/kb/LEEME.md` y `Harness/claude.md` importados en `~/.claude/CLAUDE.md`; la carpeta en `permissions.additionalDirectories`; `~/.claude/omarchy.md` es un enlace a `Contexto/omarchy.md` |
| Pi | `~/.pi/agent/AGENTS.md` generado (LEEME + `Harness/pi.md`); skills por `~/.agents/skills` |
| opencode | `~/.config/opencode/AGENTS.md` generado (LEEME + `Harness/opencode.md`); permiso `external_directory` para la carpeta; skills por `~/.agents/skills` y `~/.claude/skills` |
| Antigravity | plugin `~/.gemini/config/plugins/obsidian-kb` (`rules/AGENTS.md` generado + `skills` → `Agentes/Skills`) |

`agentes-kb-sync` regenera lo anterior y enlaza cada `Agentes/Skills/<n>` en `~/.agents/skills` y `~/.claude/skills`
(borra los enlaces de skills que ya no existen y avisa si un `SKILL.md` tiene `name:` distinto de su carpeta o una
`description` con `: ` sin comillas, que Pi y Antigravity descartan). Corre con `agentes-kb-sync.path` (al editar
`LEEME.md`, `Harness/` o `Skills/`, ~4 s) y `agentes-kb-sync.timer` (al iniciar sesión y cada hora).

```bash
contrib/omarchy/agentes-kb/install.sh            # siembra (sin sobrescribir notas), migra, configura, activa
contrib/omarchy/agentes-kb/install.sh --remove   # deshace la integración; las notas quedan
bash contrib/omarchy/agentes-kb/tests/test.sh    # 30 pruebas en un HOME falso
```

La migración (una vez, con respaldo en `~/.local/state/agentes-kb/backups/<fecha>/`) mueve `~/.claude/omarchy.md` y
los skills propios de `~/.claude/skills` (carpetas reales con `SKILL.md`; no los enlaces de Omarchy ni `synced/`) a la
bóveda y deja enlaces en su lugar. `--remove` vuelve a dejarlos como archivos reales.
