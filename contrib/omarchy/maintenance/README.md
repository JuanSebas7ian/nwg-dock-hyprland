# Mantenimiento automático de Omarchy

Plan completo en `PLAN.md`. Flujo: implementa Sonnet, evalúa Opus, snapshot "estable" solo si todo pasa.

## Pasos (`apply.sh`)

| Paso | Qué hace |
|---|---|
| S0 / S1 | Snapshot snapper `pre` / `post` (el número queda en `~/.local/state/omarchy-maintenance/`) |
| 1 | TRIM a través de LUKS2: `cryptsetup refresh --allow-discards --persistent root` (pide la frase de LUKS), `fstrim.timer`, `fstrim -v /` |
| 2 | `PACCACHE_ARGS='-k3'` + `paccache.timer` + limpieza inicial |
| 3 | `btrfs-scrub@-.timer` (scrub mensual de `/` y `/home`) |
| 4 | `smartmontools`, `/etc/smartd.conf` y `/usr/local/lib/omarchy/smartd-notify` (copia de `smart-notify`: log + notificación crítica); valida con `smartd -q onecheck` y baja a un conjunto de directivas menor si smartd rechaza alguna |
| 5 | Quita `drive-bin-debug` y `gdrive-debug` si siguen huérfanos |
| 6 | Códecs GStreamer, `libva-utils`, `nvme-cli` |

El paso 7 (suspensión) es manual: `suspend-test.sh`. S2 (snapshot estable): `stable-snapshot.sh`, solo si `reward.py` da estable.

## Uso

```bash
./apply.sh --dry-run          # sin root, sin cambios
./apply.sh --only 2,3 --wait  # pasos sueltos, desde una terminal visible
./smoke-test.sh [--json] [--only ID,ID]
./smoke-test.sh --json > smoke.json && ./reward.py smoke.json
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -v
```

Logs de `apply.sh` en `~/.local/state/omarchy-maintenance/apply-<fecha>.log` (termina en `exit=N`).
Para repetir S0/S1 borra `pre-number` / `post-number` de esa carpeta.

## Deshacer

- 1: `sudo cryptsetup refresh --persistent root` (sin `--allow-discards`) · `sudo systemctl disable --now fstrim.timer`
- 2: restaurar `/etc/conf.d/pacman-contrib.bak.<fecha>` · `sudo systemctl disable --now paccache.timer`
- 3: `sudo systemctl disable --now btrfs-scrub@-.timer`
- 4: `sudo systemctl disable --now smartd` · restaurar `/etc/smartd.conf.bak.<fecha>` · `sudo rm /usr/local/lib/omarchy/smartd-notify` · `sudo pacman -Rns smartmontools`
- 6: `sudo pacman -Rns gst-plugins-good gst-plugins-bad gst-plugins-ugly gst-libav libva-utils nvme-cli`
- Todo: arrancar el snapshot S0 desde Limine.

## Reward

Pesos: TRIM 25, smartd 20, paccache 10, scrub 10, huérfanos 5, paquetes 10, `/boot` 5, servicios 10, `hyprctl configerrors` 5.
PASS = peso completo, WARN = mitad, FAIL = 0, SKIP = ignorado. Estable: nota >= 90 y ningún FAIL en TRIM ni smartd.
