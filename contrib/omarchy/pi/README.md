# Pi en este equipo: configuración y Docker sí o no

Pi 1.1.0 (`mise`, `~/.local/bin/pi`) usa por defecto **llama.cpp** a través del proveedor `llama-local`
de `~/.pi/agent/models.json`, que genera `omarchy-llm sync`. El router carga el modelo elegido en Pi y
lo descarga tras 15 min sin uso. También funciona en modo no interactivo (`pi -p`, subagentes).

| Pieza | Dónde | Coste por petición (medido) |
|---|---|---|
| Núcleo + herramientas integradas | Pi | 1.449 tokens |
| `AGENTS.md` global (TODO.md, PLAN.md por fases, subagentes con `pi -p`, seguridad) | `~/.pi/agent/AGENTS.md` | 237 |
| MCP `fetch`, `filesystem` (~/src, ~/ai/projects), `git`; `brave-search` apagado (sin clave) | `~/.pi/agent/mcp.json`, exposición `deferred` | 274 (con `direct` serían miles) |
| Extensión `pi-subagents@0.77.0` | `settings.json` → `packages` | 1.284 |
| Extensión `pi-web-access@0.38.0` | **bajo demanda**: `pi-web` | 3.205 (por eso no va siempre) |
| Skills `omarchy`, `diagnose-crash` | `~/.pi/agent/skills` | 632 |
| **Total `pi`** | | **3.856** (antes de quitar pi-web-access: 7.061) |

Medir de nuevo: `omarchy-agent-usage-pi --measure` (un mensaje por variante, en sesiones temporales,
con el modelo local más pequeño). El panel de agentes de la barra (pestaña **Pi**) muestra cada pieza
con lo que cuesta; el widget Local AI › Pi, todos los detalles.

Lo que el vídeo daba por hecho y aquí no se cumple:
- "menos de 900 tokens": Pi 1.1.0 ya trae 1.449 solo con el núcleo.
- `pi-local-models`: un único lanzamiento, sin repositorio y 379 descargas al mes; no hace falta,
  porque `models.json` ya conecta con el router. **No se instaló.**
- `subagent` (npm) es un SDK de Morph, no una extensión de Pi; la extensión es `pi-subagents`.

## ¿Docker o no?

| | `pi` nativo | `pi-box` (Docker) |
|---|---|---|
| Qué puede tocar | todo tu `$HOME` (credenciales, `~/.ssh`, otros repos) | solo la carpeta actual (`/workspace`) |
| Herramientas | todo el sistema: go, cargo, mise, docker, hyprctl… | node, git, ripgrep, python, uv |
| Arranque | ~1 s | ~1,6 s (+0,5 s) |
| Modelos locales | sí | sí (`--network host` → 127.0.0.1:8080) |
| Imagen | — | 29 s de construcción, 221 MB |
| Si un comando destruye algo | git + snapshots de `/home` cada hora + restic | el daño queda en la carpeta (git) |

**Decisión:** los dos, cada uno para lo suyo.
- **`pi` nativo, para trabajar contigo delante** en repos propios: necesita las herramientas del
  sistema, y git más los snapshots horarios de `/home` cubren los errores.
- **`pi-box` para todo lo que corre solo** (sesiones largas sin supervisión, como las de dos días),
  para repos de terceros, y siempre que se quiera el "modo YOLO". Pi no pide confirmación antes de
  `bash`, y un modelo local de 7-27B se equivoca más que uno grande.

`pi-box` monta solo la carpeta actual, guarda su configuración en `~/.local/share/pi-box/home` (copia
`models.json`, `mcp.json`, `AGENTS.md`, `settings.json`; nunca `auth.json`), corre con tu usuario y
grupo, limita la memoria (6 GB) y los procesos, y usa `no-new-privileges`. Se niega a correr en `$HOME`.

```bash
cd ~/src/mi-proyecto && pi-box                       # interactivo
pi-box -p "escribe las pruebas de x.go" </dev/null    # subagente aislado
pi-box --build                                        # reconstruir la imagen (Pi fijado a 1.1.0)
pi-web                                                # Pi + búsqueda web para esta sesión
```

Probado el 2026-10-11: `pi-box` creó `hello.txt` con el dueño correcto y `ls /home` dentro solo veía el
contenedor. Con qwen2.5-coder-7b las llamadas a herramientas salen como texto (el modelo no las
respeta); con qwen2.5-7b funcionan.

## Entorno: web, seguridad y estabilidad (2026-10-12, `install.sh`)

`contrib/omarchy/pi/install.sh` (idempotente; `--remove` lo quita) instala:

| Pieza | Qué hace | Coste |
|---|---|---|
| `extensions/omarchy-web.ts` | `web_search` (SearXNG privado en `127.0.0.1:8888`, sin claves ni cuentas) y `web_fetch` (página o PDF a texto, recortado; el texto completo queda en `/tmp/pi-web/`) | +209 tokens (5.431 → 5.640); `pi-web-access` sumaba ~3.200 y busca en Exa y resume con modelos en la nube |
| `searxng/` | `searxng.service` (usuario, Docker, imagen fijada por digest, 512 MB, solo 127.0.0.1); motores que responden desde esta IP: google cse, yahoo, duckduckgo web, yep, mwmbl (+ wikipedia, github, stackoverflow, arxiv). ~0,8-1,5 s por búsqueda, ~135 MB | — |
| `extensions/omarchy-guard.ts` | A partir de los ejemplos oficiales `permission-gate`, `protected-paths` y `notify`: confirma comandos peligrosos (sudo, `rm -r`, force push, `reset --hard`, dd/mkfs, `curl \| sh`, `pacman -R`, apagar…); nunca lee ni escribe `Privado/`, `~/.ssh`, `~/.gnupg`, la contraseña de restic ni `auth.json`; no escribe `.env`; confirma escrituras **fuera del proyecto** (herramientas y bash: `>`, `tee`, `mkdir`, `cp`, `mv`, `sed -i`…), salvo `~/.agents/kb` y `/tmp/pi-*`; corta la misma llamada repetida 3 veces; notificación al terminar una tarea larga. Sin interfaz (`pi -p`) bloquea en vez de preguntar | 0 tokens |
| `settings.json` | compactación para el modelo de 32k (`keepRecentTokens` 10.000) y reintentos | — |
| `mcp.json` | MCP `fetch` apagado (lo reemplaza `web_fetch`) | −1 herramienta |

Probado con Pi real: busca y cita la fuente (21 s); bloquea `rm -rf build`, escribir en `/tmp/rev2` (también cuando el
modelo intentó rodearlo con `bash`) y leer `Privado/`. Pruebas: `node --test tests/guard.test.ts` (5).
