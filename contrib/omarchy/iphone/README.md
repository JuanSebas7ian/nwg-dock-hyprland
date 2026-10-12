# iPhone en Omarchy: widget, fotos, respaldos, AirPlay y KDE Connect

| Pieza | Qué hace | Cómo |
|---|---|---|
| Widget `juansebas7ian.iphone` (barra, tras Bluetooth) | Overview (batería, almacenamiento, confianza, internet por USB, AirPlay, KDE Connect) · Photos · Files · Backup · Mirror · Connect. Clic derecho = importar fotos | `bar/plugins/juansebas7ian.iphone/Panel.qml` |
| `omarchy-iphone` (Go, solo stdlib) | Backend del widget: envuelve `idevice_id`, `ideviceinfo -x`, `idevicepair`, `ifuse`, `idevicebackup2`, `kdeconnect-cli`, `systemctl --user` | `go/internal/iphone`, `go/cmd/omarchy-iphone` (18 pruebas) |
| `omarchy-iphone.service` | Al conectar un iPhone de confianza por USB: importa las fotos nuevas (`autoImport`) y hace el respaldo si toca (`autoBackupDays`); avisa con notificación; al desconectar libera los montajes | `systemd/` |
| `uxplay.service` | Receptor AirPlay: el iPhone duplica pantalla y sonido en el PC (nombre en `~/.config/omarchy-iphone/airplay.env`) | reemplaza la unidad del paquete `uxplay` |
| `setup-system.sh` (sudo) | `usbmuxd`, `ifuse`, `kdeconnect`, `uxplay` (AUR) y puertos de KDE Connect/AirPlay/mDNS abiertos **solo a la red de casa** en ufw; snapshot antes y después | `--remove` deshace |

```bash
contrib/omarchy/iphone/setup-system.sh   # una vez, con sudo
contrib/omarchy/iphone/install.sh        # backend + servicios + widget (sin root); --remove lo quita
```

**Por qué Go y no Swift ni Rust:** en Linux no hay librerías de Swift para el iPhone (los frameworks de Apple son
solo de macOS) y Swift ni está en los repos de Arch; Rust `idevice` es "research-stage". Las herramientas de
libimobiledevice (C) son las que usan GNOME y KDE; llamarlas desde Go mantiene el módulo `omarchy` sin dependencias
(`go-ios` le habría metido ~30).

- Fotos: se copian tal cual (HEIC/MOV) a `~/Pictures/iPhone/<año>/`, una sola vez (registro por dispositivo en
  `~/.local/state/omarchy-iphone/<udid>/imported.json`; si se pierde, las ya copiadas se reconocen por nombre y tamaño).
  Nunca se borra nada del iPhone.
- Respaldos: `~/iPhoneBackup` como subvolumen btrfs propio (fuera de los snapshots horarios de `/home`) y excluido de
  restic. El cifrado se activa desde el widget (abre una terminal para la contraseña): sin cifrar, iOS deja fuera
  contraseñas y Salud.
- Apple no permite: iMessage/SMS, sincronizar la biblioteca de música, instalar apps. Contactos y calendario van por
  iCloud/Google. Notificaciones del iPhone en el PC (ANCS por Bluetooth) y el modo Wi-Fi de usbmuxd: experimentales,
  pendientes de probar con el iPhone.
