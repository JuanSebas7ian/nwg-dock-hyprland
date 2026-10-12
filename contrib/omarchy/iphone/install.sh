#!/usr/bin/env bash
# User part of the iPhone integration (no root): the Go backend, the watch service, the UxPlay unit and the
# bar widget. The system part (usbmuxd, ifuse, kdeconnect, uxplay, firewall) is setup-system.sh (sudo).
#   install.sh            build, test and install; enable omarchy-iphone.service; place the widget after Bluetooth
#   install.sh --remove   stop and remove all of it (imported photos and backups stay)
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/.." && pwd)
BIN=$HOME/.local/bin
UNITS=$HOME/.config/systemd/user
PLUG=$HOME/.config/omarchy/plugins/juansebas7ian.iphone
say() { echo "iphone: $*"; }

if [[ ${1:-} == --remove ]]; then
  systemctl --user disable --now omarchy-iphone.service uxplay.service 2>/dev/null || true
  "$BIN/omarchy-iphone" eject >/dev/null 2>&1 || true
  rm -f "$UNITS/omarchy-iphone.service" "$UNITS/uxplay.service" "$BIN/omarchy-iphone"
  omarchy plugin disable juansebas7ian.iphone >/dev/null 2>&1 || true
  rm -rf "$PLUG"
  systemctl --user daemon-reload
  omarchy restart shell >/dev/null 2>&1 || true
  say "removed (photos in ~/Pictures/iPhone and backups in ~/iPhoneBackup stay)"; exit 0
fi

for t in idevice_id ideviceinfo idevicepair idevicebackup2 ifuse fusermount3; do
  command -v "$t" >/dev/null || { echo "iphone: missing $t: run $here/setup-system.sh first" >&2; exit 1; }
done
(cd "$repo/go" && go vet ./internal/iphone ./cmd/omarchy-iphone && go test ./internal/iphone >/dev/null) || { echo "iphone: tests failed" >&2; exit 1; }
built=$("$repo/go/tool.sh" --build omarchy-iphone)
install -Dm755 "$built" "$BIN/omarchy-iphone"
install -Dm644 "$here/systemd/omarchy-iphone.service" "$UNITS/omarchy-iphone.service"
install -Dm644 "$here/systemd/uxplay.service" "$UNITS/uxplay.service"
systemctl --user daemon-reload
systemctl --user enable --now omarchy-iphone.service >/dev/null
systemctl --user restart omarchy-iphone.service

src="$repo/bar/plugins/juansebas7ian.iphone"
omarchy plugin validate "$src" >/dev/null
mkdir -p "$PLUG"
cp -a "$src/." "$PLUG/"
# the shell registers a new plugin a moment after the rescan
for _ in 1 2 3 4 5 6 7 8 9 10; do
  omarchy-shell shell rescanPlugins >/dev/null 2>&1 || true
  omarchy plugin enable juansebas7ian.iphone >/dev/null 2>&1 && break
  sleep 1
done
if [[ ! -e $PLUG/.placed ]]; then
  omarchy bar move juansebas7ian.iphone --section right --after omarchy.bluetooth >/dev/null 2>&1 || omarchy bar move juansebas7ian.iphone --section right >/dev/null 2>&1 || true
  touch "$PLUG/.placed"
fi
omarchy restart shell >/dev/null 2>&1 || true
say "done: plug the iPhone in, unlock it and tap Trust"
