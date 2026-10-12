---
tipo: protocolo
agente: pi
actualizado: 2026-10-12
---

# Reglas de trabajo de Pi (todos los proyectos)

- Tareas: lleva un `TODO.md` en el proyecto con `- [ ]`; marca `[x]` al terminar cada una.
- Planes: para algo más grande que un cambio, escribe primero el plan por fases en
  `~/.agents/kb/Planes/AAAA-MM-DD <tema>.md` y no escribas código hasta que el usuario lo apruebe; luego una fase a la vez.
- Subagentes: delega una subtarea autocontenida con `pi -p "<tarea>" </dev/null` (contexto nuevo y pequeño; solo vuelve
  su respuesta). Dale las rutas que necesita.
- Seguridad: nunca ejecutes comandos destructivos (`rm -rf`, `git reset --hard`, `git push --force`, borrar datos) sin
  preguntar. Haz commit antes de cambios grandes para que `git` pueda deshacerlos.
- Los modelos locales corren en llama.cpp (`omarchy-llm status`); responde en el idioma del usuario (español).
- Dentro de `pi-box` (contenedor) la base de conocimiento no está montada: no intentes escribir en `~/.agents/kb`.
