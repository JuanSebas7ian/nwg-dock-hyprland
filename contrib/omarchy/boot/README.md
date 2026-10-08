# Arranque: pantalla con íconos (rEFInd) y disco que se desbloquea solo (TPM2)

Para este equipo: Omarchy en el WD SN770 (LUKS2 + btrfs, UKI de Limine) y Windows 11 en el Samsung 980.

```
sudo contrib/omarchy/boot/install.sh refind          # pantalla de íconos delante de Limine
sudo contrib/omarchy/boot/install.sh tpm2            # initramfs con systemd + llave LUKS en el TPM2
sudo contrib/omarchy/boot/install.sh remove-refind   # el firmware vuelve a Limine
sudo contrib/omarchy/boot/install.sh remove-tpm2     # vuelve a pedir la contraseña del disco
     contrib/omarchy/boot/install.sh check           # estado, sin root
contrib/omarchy/boot/make-assets.sh                  # regenera las imágenes del tema (se versionan)
```

## Cómo queda el arranque

```
Firmware (BootOrder: rEFInd, Limine, Windows, UEFI OS)
└─ rEFInd: [Omarchy]  [Windows 11]          ← 4 s y arranca Omarchy
   ├─ Omarchy → /EFI/Linux/omarchy_linux.efi (UKI, con su línea del kernel dentro)
   │   └─ Tab / F2 / Insert → "Snapshots y menu completo (Limine)" → menú de Limine
   └─ Windows 11 → /EFI/Microsoft/Boot/bootmgfw.efi en su propia ESP (PARTUUID 8d884047…)
initramfs (systemd) → sd-encrypt → llave del TPM2 → sin contraseña
```

- **Limine no se toca**: sigue siendo el gestor de los snapshots (`limine-snapper-sync`) y el respaldo.
  Si rEFInd fallara, el firmware pasa a la siguiente entrada (Limine) o se elige con F8.
- `refind-bootorder.service` (cada arranque) vuelve a poner rEFInd primero si Windows o la BIOS
  cambian el orden. Solo escribe en la NVRAM si hace falta.
- Limine no muestra íconos (es un menú de texto); por eso rEFInd va delante.
- El paquete `refind` no actualiza el ESP solo: tras una versión nueva, `install.sh refind` otra vez.

## TPM2

- `zz-claude-tpm2.conf` (en `/etc/mkinitcpio.conf.d/`) traduce los hooks de Omarchy a los de systemd:
  `udev→systemd`, `keymap/consolefont→sd-vconsole`, `encrypt→sd-encrypt`,
  `btrfs-overlayfs→sd-btrfs-overlayfs` (arrancar snapshots), `resume` lo hace systemd.
- La línea del kernel conserva `cryptdevice=` (los snapshots anteriores llevan su UKI vieja, con busybox,
  y siguen pidiendo la contraseña) y añade `rd.luks.name=<uuid>=root rd.luks.options=tpm2-device=auto`
  con `kernel-param.sh` (drop-in `claude-tpm2.conf`).
- La llave se sella con **PCR 0+7** (firmware + estado de Secure Boot): sobrevive a actualizaciones de
  kernel, de Limine y de rEFInd. **Tras actualizar la BIOS** el TPM no la suelta, el arranque pide la
  contraseña y hay que volver a sellarla: `sudo install.sh tpm2` (o `systemd-cryptenroll --wipe-slot=tpm2
  --tpm2-device=auto --tpm2-pcrs=0+7 /dev/disk/by-partuuid/d714e6bc-…`).
- La contraseña del disco sigue en su ranura: es el respaldo.
- **Seguridad**: con Secure Boot apagado, quien tenga el equipo completo puede arrancar otro sistema
  y pedirle la llave al TPM. Sacar solo el disco no sirve: sigue cifrado. Para cerrarlo: Secure Boot
  propio (`sbctl`) y sellar también PCR 7 con él, o añadir un PIN (`--tpm2-with-pin=yes`).

## Probado antes de aplicarlo

- El initramfs con los hooks nuevos se compila como usuario con la configuración real + el drop-in, y
  trae `systemd-cryptsetup`, el token TPM2, `libtss2`, Plymouth, `overlayfs-setup.service` y los módulos
  NVIDIA. `install.sh tpm2` repite esa prueba como root antes de reconstruir, y después comprueba la
  línea del kernel y el initramfs **dentro de la UKI nueva** (`objcopy`) antes de sellar la llave.
