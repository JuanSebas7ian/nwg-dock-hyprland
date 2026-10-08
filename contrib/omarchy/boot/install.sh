#!/usr/bin/env bash
# Pantalla de inicio con íconos (rEFInd) y desbloqueo del disco con el TPM2, para este Omarchy.
#
#   sudo contrib/omarchy/boot/install.sh refind          # rEFInd primero en el arranque, Limine detrás
#   sudo contrib/omarchy/boot/install.sh tpm2            # initramfs con systemd + clave LUKS en el TPM2
#   sudo contrib/omarchy/boot/install.sh remove-refind   # el firmware vuelve a arrancar Limine
#   sudo contrib/omarchy/boot/install.sh remove-tpm2     # vuelve a pedir la contraseña del disco
#   sudo contrib/omarchy/boot/install.sh splash          # UKI sin firmware de amdgpu + logo de Plymouth desde el inicio
#   sudo contrib/omarchy/boot/install.sh remove-splash
#   sudo contrib/omarchy/boot/install.sh splash-nodebug  # quita plymouth.debug tras revisar un arranque
#        contrib/omarchy/boot/install.sh check           # estado, sin root
#
# Nunca toca archivos de Omarchy: el tema y la configuración de rEFInd van en /boot/EFI/refind, los
# hooks del initramfs en un drop-in propio (/etc/mkinitcpio.conf.d/zz-claude-tpm2.conf) y la línea
# del kernel en /etc/limine-entry-tool.d/claude-tpm2.conf (con kernel-param.sh). Antes de cada
# cambio de sistema crea un snapshot de snapper (arrancable desde Limine).
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
ESP=/boot
REFIND_DIR=$ESP/EFI/refind
REFIND_LOADER='\EFI\refind\refind_x64.efi'
LUKS_DEV=/dev/disk/by-partuuid/d714e6bc-ac05-42c0-9474-6e504b34d918
TPM2_PCRS=${TPM2_PCRS:-0+7}
DROPIN=/etc/mkinitcpio.conf.d/zz-claude-tpm2.conf
NOKMS=/etc/mkinitcpio.conf.d/zz-claude-nokms.conf
GUARD=/usr/local/lib/omarchy/refind-bootorder
GUARD_UNIT=/etc/systemd/system/refind-bootorder.service
KERNEL_PARAM=${KERNEL_PARAM:-/home/juansebas7ian/.claude/skills/omarchy-hardware/scripts/kernel-param.sh}
BACKUPS=/etc/omarchy-backups
STAMP=$(date +%Y%m%d-%H%M%S)

say() { printf '==> %s\n' "$*"; }
warn() { printf 'AVISO: %s\n' "$*" >&2; }
fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}
need_root() { [[ $EUID -eq 0 ]] || fail "ejecútalo con sudo"; }

snapshot() {
  local n try
  for try in $(seq 1 24); do
    n=$(snapper -c root create -c number -d "$1" --print-number 2>/dev/null) && break
    say "  snapper ocupado, reintento en 5 s ($try/24)"
    sleep 5
  done
  [[ -n ${n:-} ]] || fail "no se pudo crear el snapshot: no sigo"
  say "snapshot root #$n: $1"
}

refind_bootnum() {
  efibootmgr | awk -v l='refind_x64.efi' 'tolower($0) ~ l {sub(/^Boot/, "", $1); sub(/\*$/, "", $1); print $1; exit}'
}

esp_disk_part() {
  local src
  src=$(findmnt -no SOURCE "$ESP")
  printf '/dev/%s %s\n' "$(lsblk -no PKNAME "$src")" "$(lsblk -no PARTN "$src" | tr -d ' ')"
}

luks_uuid() { cryptsetup luksUUID "$LUKS_DEV"; }

# ------------------------------------------------------------------ rEFInd
install_refind() {
  need_root
  [[ -d /sys/firmware/efi ]] || fail "el sistema no arrancó en modo UEFI"
  [[ -f $ESP/EFI/Linux/omarchy_linux.efi ]] || fail "no está la UKI $ESP/EFI/Linux/omarchy_linux.efi"
  snapshot "claude $STAMP: antes de rEFInd"
  pacman -S --needed --noconfirm refind efibootmgr

  if [[ -d $REFIND_DIR ]]; then
    mkdir -p "$BACKUPS"
    tar -C "$ESP/EFI" -czf "$BACKUPS/refind-esp.$STAMP.tar.gz" refind
    say "respaldo del rEFInd anterior: $BACKUPS/refind-esp.$STAMP.tar.gz"
  fi
  install -d "$REFIND_DIR/themes/omarchy"
  install -m 644 /usr/share/refind/refind_x64.efi "$REFIND_DIR/refind_x64.efi"
  rm -rf "$REFIND_DIR/icons"
  cp -r /usr/share/refind/icons "$REFIND_DIR/icons"
  install -m 644 "$HERE/refind/refind.conf" "$REFIND_DIR/refind.conf"
  install -m 644 "$HERE"/refind/themes/omarchy/*.png "$REFIND_DIR/themes/omarchy/"
  say "rEFInd copiado a $REFIND_DIR"

  local num disk part
  num=$(refind_bootnum)
  if [[ -z $num ]]; then
    read -r disk part < <(esp_disk_part)
    efibootmgr --create --disk "$disk" --part "$part" --label "rEFInd" --loader "$REFIND_LOADER" --unicode >/dev/null
    num=$(refind_bootnum)
    [[ -n $num ]] || fail "no se pudo crear la entrada EFI de rEFInd"
    say "entrada EFI Boot$num creada ($disk, partición $part)"
  fi

  install -D -m 755 "$HERE/guard/refind-bootorder" "$GUARD"
  install -D -m 644 "$HERE/guard/refind-bootorder.service" "$GUARD_UNIT"
  systemctl daemon-reload
  systemctl enable refind-bootorder.service
  "$GUARD"
  efibootmgr | sed -n '1,8p'
}

remove_refind() {
  need_root
  local num
  systemctl disable refind-bootorder.service 2>/dev/null || true
  rm -f "$GUARD_UNIT" "$GUARD"
  systemctl daemon-reload
  num=$(refind_bootnum)
  if [[ -n $num ]]; then
    efibootmgr --bootnum "$num" --delete-bootnum >/dev/null
    say "entrada EFI Boot$num borrada"
  fi
  if [[ -d $REFIND_DIR ]]; then
    mkdir -p "$BACKUPS"
    tar -C "$ESP/EFI" -czf "$BACKUPS/refind-esp.$STAMP.tar.gz" refind
    rm -rf "$REFIND_DIR"
    say "quitado $REFIND_DIR (respaldo en $BACKUPS/refind-esp.$STAMP.tar.gz)"
  fi
  efibootmgr | sed -n '1,6p'
}

# ------------------------------------------------------------------ TPM2
check_initramfs() {
  # $1 = imagen de initramfs; comprueba que trae lo necesario para el TPM2 y los snapshots
  local list missing=()
  list=$(lsinitcpio "$1")
  for f in usr/lib/systemd/systemd-cryptsetup libcryptsetup-token-systemd-tpm2.so libtss2-esys usr/lib/systemd/system/plymouth-start.service overlayfs-setup.service updates/dkms/nvidia.ko; do
    grep -q -- "$f" <<<"$list" || missing+=("$f")
  done
  if ((${#missing[@]})); then
    printf '  falta en el initramfs: %s\n' "${missing[@]}" >&2
    return 1
  fi
}

install_tpm2() {
  need_root
  systemd-cryptenroll --tpm2-device=list | grep -q /dev/tpm || fail "no hay TPM2 disponible"
  [[ -x $KERNEL_PARAM ]] || fail "no encuentro kernel-param.sh en $KERNEL_PARAM"
  local uuid kver test_img
  uuid=$(luks_uuid)
  kver=$(uname -r)
  snapshot "claude $STAMP: antes del desbloqueo con TPM2"

  say "1/4 hooks de systemd en el initramfs ($DROPIN)"
  install -m 644 "$HERE/tpm2/zz-claude-tpm2.conf" "$DROPIN"

  say "2/4 initramfs de prueba (no toca el arranque)"
  test_img=$(mktemp /tmp/claude-tpm2-test.XXXXXX.img)
  if ! /usr/bin/mkinitcpio -k "$kver" -g "$test_img" || ! check_initramfs "$test_img"; then
    rm -f "$DROPIN" "$test_img"
    fail "el initramfs de prueba falló: quité $DROPIN, nada más cambió"
  fi
  rm -f "$test_img"
  say "  initramfs de prueba correcto"

  say "3/4 línea del kernel + reconstrucción de la UKI (kernel-param.sh)"
  "$KERNEL_PARAM" add tpm2 "rd.luks.name=$uuid=root rd.luks.options=tpm2-device=auto" \
    "Desbloqueo de LUKS con TPM2 (initramfs systemd, sd-encrypt); cryptdevice= queda para snapshots viejos"
  local uki=$ESP/EFI/Linux/omarchy_linux.efi tmpd
  tmpd=$(mktemp -d)
  objcopy -O binary --only-section=.cmdline "$uki" "$tmpd/cmdline"
  objcopy -O binary --only-section=.initrd "$uki" "$tmpd/initrd"
  grep -q "rd.luks.name=$uuid=root" "$tmpd/cmdline" || fail "la UKI nueva no trae rd.luks.name: revisa antes de reiniciar"
  check_initramfs "$tmpd/initrd" || fail "el initramfs de la UKI nueva está incompleto: revisa antes de reiniciar"
  rm -rf "$tmpd"
  say "  UKI verificada: línea del kernel e initramfs con TPM2"

  say "4/4 clave en el TPM2 (PCR $TPM2_PCRS). Escribe la contraseña ACTUAL del disco:"
  if cryptsetup luksDump "$LUKS_DEV" | grep -q 'systemd-tpm2'; then
    systemd-cryptenroll --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs="$TPM2_PCRS" "$LUKS_DEV"
  else
    systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs="$TPM2_PCRS" "$LUKS_DEV"
  fi
  cryptsetup luksDump "$LUKS_DEV" | sed -n '/^Keyslots:/,/^Digests:/p' | grep -E '^\s+[0-9]+: |^Tokens|tpm2-pcrs|Keyslot:'
  say "Listo. La contraseña sigue valiendo como respaldo (si cambia la BIOS, el TPM no la soltará y la pedirá)."
}

remove_tpm2() {
  need_root
  snapshot "claude $STAMP: antes de quitar el desbloqueo con TPM2"
  if cryptsetup luksDump "$LUKS_DEV" | grep -q 'systemd-tpm2'; then
    systemd-cryptenroll --wipe-slot=tpm2 "$LUKS_DEV"
    say "clave del TPM2 borrada del disco (la contraseña sigue)"
  fi
  rm -f "$DROPIN"
  "$KERNEL_PARAM" remove tpm2 || limine-update
}

# ------------------------------------------------------------------ arranque sin negro
# La pantalla quedaba negra tras elegir Omarchy: (1) UKI de ~290 MB por el firmware de amdgpu que mete el
# hook kms, lenta de leer para rEFInd; (2) Plymouth 26 ignora simpledrm y no dibuja nada hasta que carga
# nvidia-drm (y, sin la contraseña del disco, nunca se veía).
install_splash() {
  need_root
  [[ -x $KERNEL_PARAM ]] || fail "no encuentro kernel-param.sh en $KERNEL_PARAM"
  local kver test_img uki=$ESP/EFI/Linux/omarchy_linux.efi before
  kver=$(uname -r)
  before=$(stat -c %s "$uki")
  snapshot "claude $STAMP: antes de quitar kms del initramfs y del logo con simpledrm"

  say "1/3 initramfs sin el hook kms ($NOKMS)"
  install -m 644 "$HERE/splash/zz-claude-nokms.conf" "$NOKMS"
  test_img=$(mktemp /tmp/claude-splash-test.XXXXXX.img)
  if ! /usr/bin/mkinitcpio -k "$kver" -g "$test_img" || ! check_initramfs "$test_img" \
    || ! lsinitcpio -l "$test_img" | grep -q 'nvidia-drm.ko'; then
    rm -f "$NOKMS" "$test_img"
    fail "el initramfs de prueba falló: quité $NOKMS, nada más cambió"
  fi
  lsinitcpio -l "$test_img" | grep -q 'amdgpu' && warn "amdgpu sigue en el initramfs (¿otro hook lo pide?)"
  say "  initramfs de prueba correcto: $(du -h "$test_img" | cut -f1) (con NVIDIA, sin amdgpu)"
  rm -f "$test_img"

  say "2/3 logo desde el primer segundo + registro de Plymouth por un arranque (kernel-param.sh)"
  "$KERNEL_PARAM" add plymouth-debug "plymouth.debug" \
    "Registro /var/log/plymouth-debug.log para revisar un arranque; quitar con install.sh splash-nodebug" >/dev/null
  "$KERNEL_PARAM" add plymouth-splash "plymouth.use-simpledrm" \
    "Plymouth 26 ignora simpledrm: sin esto no dibuja hasta que carga nvidia-drm (pantalla negra tras rEFInd)"

  say "3/3 verificar la UKI"
  local tmpd
  tmpd=$(mktemp -d)
  objcopy -O binary --only-section=.cmdline "$uki" "$tmpd/cmdline"
  objcopy -O binary --only-section=.initrd "$uki" "$tmpd/initrd"
  grep -q 'plymouth.use-simpledrm' "$tmpd/cmdline" || fail "la UKI no trae plymouth.use-simpledrm: revisa antes de reiniciar"
  grep -q 'rd.luks.name=' "$tmpd/cmdline" || fail "la UKI perdió rd.luks.name: revisa antes de reiniciar"
  check_initramfs "$tmpd/initrd" || fail "el initramfs de la UKI está incompleto: revisa antes de reiniciar"
  rm -rf "$tmpd"
  say "  UKI: $((before / 1048576)) MB → $(($(stat -c %s "$uki") / 1048576)) MB; línea del kernel y TPM2 correctos"
}

remove_splash() {
  need_root
  snapshot "claude $STAMP: antes de devolver kms y quitar el logo con simpledrm"
  rm -f "$NOKMS"
  "$KERNEL_PARAM" remove plymouth-debug >/dev/null 2>&1 || true
  "$KERNEL_PARAM" remove plymouth-splash || limine-update
}

check() {
  echo "== EFI"
  efibootmgr 2>/dev/null | sed -n '1,8p' || true
  echo "== rEFInd"
  systemctl is-enabled refind-bootorder.service 2>/dev/null || echo "guardia: no instalada"
  echo "== TPM2"
  [[ -f $DROPIN ]] && echo "hooks systemd: $DROPIN" || echo "hooks: los de Omarchy (busybox, encrypt)"
  [[ -f /etc/limine-entry-tool.d/claude-tpm2.conf ]] && cat /etc/limine-entry-tool.d/claude-tpm2.conf | grep -v '^#' || echo "sin rd.luks.name en la línea del kernel"
  tr ' ' '\n' </proc/cmdline | grep -E '^rd.luks|^cryptdevice' || true
}

case ${1:-} in
refind) install_refind ;;
tpm2) install_tpm2 ;;
remove-refind) remove_refind ;;
remove-tpm2) remove_tpm2 ;;
splash) install_splash ;;
remove-splash) remove_splash ;;
splash-nodebug) need_root; "$KERNEL_PARAM" remove plymouth-debug ;;
check) check ;;
*) sed -n '2,16p' "$0"; exit 3 ;;
esac
