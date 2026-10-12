---
tipo: protocolo
agente: claude
actualizado: 2026-10-12
---

# Reglas de Claude Code

- `~/.claude/omarchy.md` es un enlace a `Contexto/omarchy.md` de esta base: mantenerlo al día sigue siendo obligatorio
  (ver `~/.claude/CLAUDE.md`), y ya queda a la vista de los demás agentes.
- La memoria automática de Claude (`~/.claude/projects/*/memory/`) es solo para preferencias del usuario sobre cómo
  trabajar; lo que sirva a otros agentes (contexto, decisiones, conclusiones) va aquí.
- Los skills propios nuevos se crean en `~/.agents/kb/Skills/<nombre>/` (no en `~/.claude/skills/`): `agentes-kb-sync`
  los enlaza en `~/.claude/skills/` y en los demás agentes.
