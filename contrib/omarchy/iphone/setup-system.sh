#!/usr/bin/env bash
# System part of the iPhone integration (needs sudo; run it in a terminal, it asks for the password).
#   usbmuxd + ifuse   USB connection to the iPhone (pairing, info, photos, app files, backups; ipheth tethering)
#   kdeconnect        wireless companion with the official KDE Connect iOS app (clipboard, files, links)
#   uxplay (AUR)      AirPlay receiver: the iPhone mirrors its screen to this PC
#   ufw               opens KDE Connect and AirPlay ports to the home network only
# Snapshot before and after. Log: ~/.local/state/omarchy-maintenance/install-iphone.log
#   setup-system.sh --remove   undo (packages and firewall rules)
set -uo pipefail
LAN=${LAN:-$(ip -4 route show scope link | awk '!/docker|br-|virbr/ && $1 ~ /\// {print $1; exit}')}
log=$HOME/.local/state/omarchy-maintenance/install-iphone.log
mkdir -p "$(dirname "$log")"
exec > >(tee -a "$log") 2>&1
echo "== $(date -Is) iphone setup ${1:-install} (LAN=$LAN)"
[[ -n $LAN ]] || { echo "no home network found; set LAN=192.168.x.0/24"; echo "exit=2"; exit 2; }

rules=(
  "1714:1764/tcp KDE Connect" "1714:1764/udp KDE Connect"
  "7000,7001,7100/tcp AirPlay (uxplay)" "6000,6001,7011/udp AirPlay (uxplay)" "5353/udp mDNS"
)

if [[ ${1:-} == --remove ]]; then
  for r in "${rules[@]}"; do sudo ufw delete allow from "$LAN" to any port "${r%%/*}" proto "$(cut -d/ -f2 <<<"${r%% *}")"; done
  sudo pacman -Rns --noconfirm kdeconnect ifuse uxplay 2>/dev/null
  echo "usbmuxd is kept (libimobiledevice uses it); remove with: sudo pacman -Rns usbmuxd"
  echo "exit=0"; exit 0
fi

rc=0
pre=$(sudo snapper -c root create -t pre -p -d "claude: iphone (usbmuxd, kdeconnect, uxplay)" 2>/dev/null || true)
sudo pacman -S --needed --noconfirm usbmuxd ifuse kdeconnect || rc=1
if command -v yay >/dev/null; then yay -S --needed --noconfirm uxplay || rc=1; else echo "yay missing: uxplay not installed"; rc=1; fi
for r in "${rules[@]}"; do
  ports=${r%%/*}; proto=$(cut -d/ -f2 <<<"${r%% *}"); name=${r#* }
  sudo ufw allow from "$LAN" to any port "$ports" proto "$proto" comment "$name (LAN)" || rc=1
done
sudo udevadm control --reload
[[ -n $pre ]] && sudo snapper -c root create -t post --pre-number "$pre" -d "claude: iphone" >/dev/null 2>&1
pacman -Q usbmuxd ifuse kdeconnect uxplay 2>&1
sudo ufw status | grep -E 'KDE|AirPlay|mDNS'
echo "Connect the iPhone by cable, unlock it and tap Trust (then type the passcode)."
echo "exit=$rc"
