#!/usr/bin/python3
"""Drivers backend for the bar: board, driver health and pending updates as JSON.

  drivers.py [--force]

- Health: `omarchy-hwcheck --json` (fast, local, deterministic).
- Driver packages with updates: `checkupdates` (pacman-contrib, no root),
  filtered to kernel, firmware, GPU, microcode, Bluetooth and DKMS packages.
  Network, so cached for an hour.
- BIOS: installed version from DMI against the newest one ASUS publishes for
  this board (support API), cached for a day.
- Loaded driver versions for the main devices.
"""

import json
import os
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

CACHE = Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache") / "omarchy-drivers-widget"
DRIVER_PKGS = re.compile(
  r"^(linux(-lts|-zen|-hardened)?(-headers)?|linux-firmware.*|amd-ucode|intel-ucode|"
  r"nvidia.*|lib32-nvidia.*|opencl-nvidia|egl-wayland.*|mesa.*|lib32-mesa.*|vulkan-.*|lib32-vulkan-.*|"
  r"libva.*|bluez.*|sof-firmware|alsa-firmware|alsa-ucm-conf|wireless-regdb|"
  r"dkms|.*-dkms|hid-xpadneo.*|fwupd|systemd|limine.*)$")


def dmi(name):
  try:
    return Path(f"/sys/class/dmi/id/{name}").read_text().strip()
  except OSError:
    return ""


def cached(name, max_age, producer, force):
  path = CACHE / f"{name}.json"
  if not force:
    try:
      if time.time() - path.stat().st_mtime < max_age:
        return json.loads(path.read_text())
    except (OSError, ValueError):
      pass
  data = producer()
  if data.get("error") and path.exists():
    # Keep showing the last good answer when offline, but say so.
    try:
      old = json.loads(path.read_text())
      old["stale"] = data["error"]
      return old
    except ValueError:
      pass
  CACHE.mkdir(parents=True, exist_ok=True)
  tmp = path.with_suffix(".tmp")
  tmp.write_text(json.dumps(data))
  tmp.replace(path)
  return data


def health():
  try:
    out = subprocess.run(["omarchy-hwcheck", "--json"], capture_output=True, text=True, timeout=60).stdout
    data = json.loads(out)
  except Exception as exc:
    return {"error": str(exc), "counts": {}, "issues": []}
  counts = {}
  issues = []
  for f in data.get("findings", []):
    level = f.get("level", "ok")
    counts[level] = counts.get(level, 0) + 1
    if level not in ("ok", "info"):
      issues.append({"id": f.get("id"), "level": level, "title": f.get("title", ""),
                     "details": f.get("details", [])[:3], "hint": f.get("hint", "")})
  return {"counts": counts, "issues": issues}


def driver_updates():
  try:
    proc = subprocess.run(["checkupdates"], capture_output=True, text=True, timeout=120)
  except Exception as exc:
    return {"error": str(exc), "drivers": [], "total": 0}
  # checkupdates: 0 = updates, 2 = none, 1 = error
  if proc.returncode == 1:
    return {"error": (proc.stderr.strip() or "checkupdates failed")[:200], "drivers": [], "total": 0}
  drivers, total = [], 0
  for line in proc.stdout.splitlines():
    parts = line.split()
    if len(parts) >= 4:
      total += 1
      if DRIVER_PKGS.match(parts[0]):
        drivers.append({"name": parts[0], "from": parts[1], "to": parts[3]})
  return {"drivers": drivers, "total": total, "checkedAt": int(time.time())}


def latest_bios():
  board = dmi("board_name")
  vendor = dmi("board_vendor")
  if "ASUS" not in vendor.upper():
    return {"error": "only ASUS boards are supported", "board": board}
  url = ("https://www.asus.com/support/api/product.asmx/GetPDBIOS?"
         + urllib.parse.urlencode({"website": "us", "model": board}))
  try:
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0 omarchy-drivers-widget"})
    with urllib.request.urlopen(req, timeout=15) as r:
      data = json.loads(r.read())
  except Exception as exc:
    return {"error": str(exc), "board": board}
  for group in ((data.get("Result") or {}).get("Obj") or []):
    if group.get("Name") != "BIOS":
      continue
    files = group.get("Files") or []
    if files:
      f = files[0]
      notes = re.sub(r"<br\s*/?>", "\n", str(f.get("Description") or "")).strip('"').strip()
      return {"board": board, "version": str(f.get("Version", "")),
              "date": str(f.get("ReleaseDate", "")).replace("/", "-"),
              "notes": notes, "size": f.get("FileSize", ""),
              "beta": str(f.get("IsRelease", "1")) == "0",
              "download": ((f.get("DownloadUrl") or {}).get("Global") or ""),
              "page": "https://rog.asus.com/motherboards/rog-strix/"
                      + board.lower().replace(" ", "-") + "/helpdesk_bios/"}
  return {"error": "no BIOS listed", "board": board}


def loaded_drivers():
  def mod_version(m):
    try:
      return Path(f"/sys/module/{m}/version").read_text().strip()
    except OSError:
      return "loaded" if Path(f"/sys/module/{m}").exists() else ""
  kernel = os.uname().release
  rows = [("Kernel", kernel)]
  for label, module in (("NVIDIA", "nvidia"), ("Radeon (amdgpu)", "amdgpu"), ("Wi-Fi (mt7925e)", "mt7925e"),
                        ("Bluetooth (btusb)", "btusb"), ("Ethernet (igc)", "igc"), ("NVMe", "nvme")):
    v = mod_version(module)
    if v:
      rows.append((label, kernel if v == "loaded" else v))
  return [{"label": l, "version": v} for l, v in rows]


def main():
  force = "--force" in sys.argv
  bios_installed = dmi("bios_version")
  bios = cached("bios", 86400, latest_bios, force)
  newer = False
  try:
    newer = int(re.sub(r"\D", "", bios.get("version", "0")) or 0) > int(re.sub(r"\D", "", bios_installed) or 0)
  except ValueError:
    pass
  result = {
    "board": {"vendor": dmi("board_vendor"), "name": dmi("board_name"),
              "bios": bios_installed, "biosDate": dmi("bios_date")},
    "bios": dict(bios, newer=newer),
    "updates": cached("updates", 3600, driver_updates, force),
    "health": health(),
    "drivers": loaded_drivers(),
  }
  print(json.dumps(result, separators=(",", ":")))


if __name__ == "__main__":
  main()
