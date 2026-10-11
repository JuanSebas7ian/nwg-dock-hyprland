#!/usr/bin/env bash
# Hard-lockup protection: the machine reboots by itself instead of staying
# frozen without image, and the kernel's last words survive the reboot.
#   sudo install.sh            apply (snapshot first)
#   sudo install.sh --remove   undo
#   install.sh --check         show current state (no root)
set -u
LOG=${LOG:-/home/${SUDO_USER:-$USER}/.local/state/omarchy-maintenance/stability.log}
WD=/etc/systemd/system.conf.d/90-claude-watchdog.conf
SC=/etc/sysctl.d/90-claude-lockup.conf

check() {
  echo "systemd: $(systemctl show -p RuntimeWatchdogUSec --value) runtime watchdog"
  for k in softlockup_panic hardlockup_panic panic sysrq nmi_watchdog; do
    printf '%s=%s  ' "$k" "$(cat /proc/sys/kernel/$k)"; done; echo
  echo "systemd-pstore: $(systemctl is-enabled systemd-pstore 2>&1)"
  ls -1 /var/lib/systemd/pstore 2>/dev/null | sed 's/^/  pstore: /'
}

apply() {
  command -v snapper >/dev/null && snapper -c root create -c number -d "claude: lockup watchdog" || true
  install -d /etc/systemd/system.conf.d
  cat > "$WD" <<'X'
# Claude 2026-10-11: reboot if the kernel stops petting the SP5100 TCO
# watchdog for 30 s (hard lockups left the PC frozen for 33 h on 2026-10-10).
[Manager]
RuntimeWatchdogSec=30s
RebootWatchdogSec=2min
X
  cat > "$SC" <<'X'
# Claude 2026-10-11: a locked-up CPU panics, and a panic reboots after 15 s.
# The panic text is kept by systemd-pstore in /var/lib/systemd/pstore.
kernel.softlockup_panic = 1
kernel.hardlockup_panic = 1
kernel.panic = 15
kernel.panic_on_oops = 0
# Magic SysRq: sync, remount-ro, reboot, signal processes (Alt+PrtSc+R/E/I/S/U/B)
kernel.sysrq = 244
X
  sysctl --load "$SC"
  systemctl enable systemd-pstore.service
  systemctl daemon-reexec
  check
}

remove() {
  rm -f "$WD" "$SC"
  systemctl disable systemd-pstore.service
  sysctl -w kernel.softlockup_panic=0 kernel.hardlockup_panic=0 kernel.panic=0 kernel.sysrq=16
  systemctl daemon-reexec
  check
}

case "${1:-}" in
  --check) check ;;
  --remove) { remove; echo "exit=$?"; } 2>&1 | tee -a "$LOG" ;;
  "") { apply; echo "exit=$?"; } 2>&1 | tee -a "$LOG" ;;
  *) echo "usage: $0 [--check|--remove]"; exit 3 ;;
esac
