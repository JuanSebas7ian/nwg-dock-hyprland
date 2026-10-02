# omarchy-hwcheck: validador de drivers, actualizaciones y conflictos

Revisa el hardware y los drivers de un equipo con Omarchy, valida que una actualización no dejó nada
roto antes de reiniciar, y detecta conflictos de paquetes y de configuración. Solo lee el sistema:
no necesita `sudo` y no cambia nada.

Es **determinista**: el mismo estado del sistema da siempre el mismo informe, byte a byte. Ordena
todo, no imprime fechas y ejecuta los comandos con `LC_ALL=C`. La única entrada que depende de la
red es `--online` (la consulta de actualizaciones pendientes).

## Instalar

```bash
git clone -b feat/dnd-reorder https://github.com/JuanSebas7ian/nwg-dock-hyprland.git
nwg-dock-hyprland/contrib/omarchy/hwcheck/install.sh
```

El instalador corre las pruebas unitarias y luego instala tres archivos:

| Archivo | Qué hace |
|---|---|
| `~/.local/bin/omarchy-hwcheck` | El validador |
| `~/.config/omarchy/hooks/post-update.d/omarchy-hwcheck.hook` | Al final de `omarchy update` (después de los paquetes y antes de ofrecer el reinicio), muestra el informe en la terminal y avisa si hay fallos |
| `~/.config/omarchy/hooks/post-boot.d/omarchy-hwcheck.hook` | En cada arranque, en segundo plano: espera 30 s a que los dispositivos se asienten (y hasta 150 s más al Bluetooth si la línea base lo tenía), compara con la línea base y avisa solo si hay fallos o regresiones |

Por último crea la línea base y hace una primera validación. Se puede ejecutar todas las veces que
quieras: si un archivo cambió, guarda el anterior en `~/.local/state/omarchy-hwcheck/backups/<fecha>/` (nunca dentro
de las carpetas de hooks, porque Omarchy ejecuta todo lo que hay ahí). Para desinstalar,
`install.sh --remove` (los informes quedan en `~/.local/state/omarchy-hwcheck/`).

Requisitos: `python3`, `pciutils` y `pacman`, que vienen con Omarchy. `--online` necesita además
`pacman-contrib`.

## Uso

```bash
omarchy-hwcheck                      # validación completa (= scan)
omarchy-hwcheck --only hw            # solo una categoría: hw, updates, conflicts (separadas por coma)
omarchy-hwcheck --online             # además, actualizaciones pendientes de kernel/drivers (checkupdates)
omarchy-hwcheck diff                 # qué cambió respecto de la línea base
omarchy-hwcheck baseline --force     # aceptar el estado actual como nueva referencia
omarchy-hwcheck state                # el estado normalizado que se compara (JSON)
omarchy-hwcheck scan --json          # salida para scripts
```

Código de salida: `0` todo bien, `1` avisos, `2` fallos o regresiones, `3` error de uso.

## Flujo para actualizar sin sorpresas

1. `omarchy-hwcheck --online` muestra qué drivers y kernels vienen en la actualización.
2. `omarchy update`. El hook valida antes del reinicio. Si dice **FAIL** en UP02/UP03 (por ejemplo,
   NVIDIA sin compilar para el kernel nuevo), **no reinicies**: arréglalo primero o tendrás pantalla negra.
3. Reinicia. El hook de arranque compara con la línea base. Si algo que funcionaba dejó de funcionar
   (un dispositivo sin driver, una interfaz de red o un adaptador Bluetooth que desapareció), te avisa.
   Si todo sigue igual o mejor, ese arranque pasa a ser la nueva línea base.

Informes: `~/.local/state/omarchy-hwcheck/last-post-update.txt` y `last-post-boot.txt`.

## Comprobaciones

Cada comprobación tiene un id estable, para buscarlo o compararlo entre ejecuciones.

| Id | Qué valida | Nivel si falla |
|---|---|---|
| **HW01** | Todo dispositivo PCI tiene driver (salvo puentes de host, ISA e IOMMU) | WARN |
| **HW02** | Dispositivos USB que no lograron conectarse en este arranque (`error -110/-71`, también en puertos de hubs); retrasan el arranque. Se listan sin conteo, para que la salida no cambie mientras el registro crece | WARN |
| **HW03** | Firmware que el kernel no pudo cargar (`Direct firmware load ... failed`), salvo los fallos inofensivos (`regulatory.db`, iwlwifi probando versiones) | FAIL |
| **HW04** | Paquete de firmware de Arch (`linux-firmware-amdgpu`, `-mediatek`, `-intel`...) para cada driver cargado | FAIL |
| **HW05** | Firmware GSP de NVIDIA para la versión de `nvidia-utils` (WARN si el módulo cargado es otro: falta reiniciar) | WARN o FAIL |
| **HW06** | Adaptador Bluetooth presente si `bluetooth.service` está habilitado | WARN |
| **HW07** | Radios bloqueadas por hardware (rfkill) | WARN |
| **UP01** | El kernel en uso sigue instalado; si no, hace falta reiniciar | WARN |
| **UP02** | `<kernel>-headers` a la par de cada kernel (DKMS los necesita) | FAIL |
| **UP03** | Cada módulo DKMS (NVIDIA, xpadneo...) compilado para cada kernel instalado | FAIL |
| **UP04** | Módulo NVIDIA cargado, módulo en disco **de cada kernel instalado**, `nvidia-utils` y `lib32-nvidia-utils` coinciden. Kernel y NVIDIA nuevos sin reiniciar = WARN; un kernel sin el módulo nuevo = FAIL | WARN o FAIL |
| **UP05** | Parámetros del kernel configurados (`KERNEL_CMDLINE[default]` de `/etc/default/limine` y los drop-ins; `=` reemplaza, `+=` agrega) que aún no están activos | WARN |
| **UP06** | Servicios de systemd fallidos (sistema y usuario) | WARN |
| **UP07** | Reinicio pendiente marcado por Omarchy o Hyprland actualizado en caliente | WARN |
| **UP08** | Errores en el registro de la última `omarchy update` (`/tmp/omarchy-update.log`), salvo un espejo caído (`failed retrieving file`) y la salida sangrada del propio validador | WARN o FAIL |
| **UP09** | (`--online`) Actualizaciones pendientes, separando las de kernel y drivers | INFO |
| **CF01** | Base de datos de pacman consistente (`pacman -Dk`): dependencias rotas o conflictos | FAIL |
| **CF02** | `.pacnew`/`.pacsave` sin resolver en `/etc` | WARN |
| **CF03** | Archivos que `omarchy update` apartó en `/var/lib/omarchy/replaced` para resolver un conflicto | WARN |
| **CF04** | Opciones de módulo con valores contradictorios entre `modprobe.d` y la línea de arranque | WARN |
| **CF05** | Opciones en `modprobe.d` para módulos integrados en el kernel, que no tienen efecto | WARN |
| **CF06** | El valor real de cada parámetro en `/sys/module` coincide con el pedido | WARN |
| **CF07** | Módulos en lista negra que igual están cargados (por ejemplo, `nouveau` junto a `nvidia`) | FAIL |
| **CF08** | Paquetes o servicios que compiten (`nvidia` y `nvidia-open`, `tlp` y `power-profiles-daemon`...) | WARN |
| **CF09** | Versiones a la par: todos los `linux-firmware-*`, `mesa` y `lib32-mesa`, los paquetes NVIDIA | WARN |

La comparación con la línea base (`diff`) marca como **regresión** (FAIL) que desaparezca un
dispositivo PCI, una interfaz de red o un adaptador Bluetooth, o que un dispositivo pierda su driver.
Si un dispositivo cambia de driver es un aviso. Los cambios de versión, de kernel, de línea de
arranque y de dispositivos USB conectados son informativos.

## Pruebas

```bash
cd contrib/omarchy/hwcheck
python3 -m unittest -v test_hwcheck
```

Las pruebas usan un sistema simulado (archivos de `/sys` y `/proc` y salidas de comandos fijas), así que
no dependen del equipo. Hay una prueba por comprobación (con el caso real de una actualización que trae kernel y
NVIDIA nuevos antes de reiniciar), más estas: que tres ejecuciones den una salida
idéntica, que `diff` detecte las regresiones y el ciclo de la línea base.
