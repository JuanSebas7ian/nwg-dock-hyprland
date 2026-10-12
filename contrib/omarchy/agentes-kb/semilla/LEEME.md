---
tipo: protocolo
actualizado: 2026-10-12
tags: [agentes, protocolo]
---

# Base de conocimiento de los agentes

Esta carpeta de Obsidian (`Agentes/`) es la memoria compartida de **todos** los agentes de este equipo:
Claude Code, Pi, opencode y Antigravity. Lo que uno aprende, decide o planea lo puede leer el siguiente.
Ruta estable sin espacios: `~/.agents/kb` (enlace a `~/Documents/Obsidian Vault/Agentes`).

## Antes de empezar una tarea

1. Busca si ya hay algo: `rg -il "<tema>" ~/.agents/kb --glob '*.md'` (o lista `Contexto/`, `Planes/`, `Conclusiones/`).
2. Lee las notas de contexto del sistema o proyecto en que vas a trabajar y respeta lo que digan.

## Dónde escribir

| Qué | Dónde | Nombre |
|---|---|---|
| Contexto de un sistema, equipo o proyecto (cómo está, rutas, trampas, cómo deshacer) | `Contexto/` | `<tema>.md` |
| Plan de trabajo por fases, antes de hacer algo grande | `Planes/` | `AAAA-MM-DD <tema>.md` |
| Conclusiones, decisiones, comparativas, diagnósticos, resultados de pruebas | `Conclusiones/` | `AAAA-MM-DD <tema>.md` |
| Procedimiento reutilizable (runbook) | `Skills/<nombre>/SKILL.md` | estándar Agent Skills |

- **Actualiza en vez de duplicar.** Si la nota existe, edítala y cambia `actualizado`. Si algo queda superado,
  pon `estado: obsoleto` y enlaza la nota nueva.
- Enlaza notas relacionadas con `[[Nombre de la nota]]`.
- Lo propio de un repositorio (README, código, `AGENTS.md`/`CLAUDE.md` del repo, specs versionadas) se queda en el
  repo; aquí va una nota corta que lo resume y da su ruta.
- `TODO.md` de trabajo en curso puede vivir en el repo; el plan y las conclusiones, aquí.

Cabecera obligatoria (YAML) de cada nota:

```yaml
---
tipo: contexto | plan | conclusion
proyecto: <nombre o "equipo">
estado: borrador | activo | hecho | obsoleto
autor: claude | pi | opencode | antigravity
creado: AAAA-MM-DD
actualizado: AAAA-MM-DD
tags: []
---
```

Un skill es una carpeta `Skills/<nombre>/` con `SKILL.md` (cabecera `name:` = nombre de la carpeta, en minúsculas
con guiones, y `description:` que diga cuándo usarlo) y, si hace falta, `scripts/` y `references/`. Queda disponible
en todos los agentes de forma automática (`agentes-kb-sync`).

## Límites

- **Solo escribes dentro de `Agentes/`.** El resto de la bóveda es del usuario: no lo edites.
- **`Privado/` no se lee ni se escribe nunca.** `Asistente/` es del proyecto asistente-local: no lo toques.
- Nunca guardes secretos (contraseñas, tokens, claves, `.env`): como mucho, dónde están.
- Escribe en español, claro y breve; nombres de archivo legibles, sin `: / \ ? * " < > |`.
- Las reglas propias de cada agente están en `Harness/<agente>.md`.
