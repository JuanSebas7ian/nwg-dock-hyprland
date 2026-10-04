#!/usr/bin/python3
"""Storage backend for the bar: disks, health, what uses the space and what is free.

  storage.py overview        disks, partitions, SMART (UDisks2), filesystems, btrfs (fast)
  storage.py breakdown       what uses the system disk: home folders, system areas, swap, the rest (du, ~5 s)
  storage.py dir <path>      one level of a folder, biggest first (drill down)
  storage.py cleanup         what can be reclaimed: pacman cache, orphans, caches, trash, journal

Read-only and without root. du cannot read every system folder and counts
btrfs data before zstd compression, so "Other" (snapshots, metadata, folders
it cannot read) is the filesystem's used space minus what du measured.
"""

import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

HOME = Path.home()
CACHE = Path(os.environ.get("XDG_CACHE_HOME") or HOME / ".cache") / "omarchy-storage-widget"
SYSTEM_AREAS = [
  ("Programs (/usr)", "/usr"),
  ("Apps in /opt", "/opt"),
  ("Ollama models", "/var/lib/ollama"),
  ("Flatpak (system)", "/var/lib/flatpak"),
  ("Docker", "/var/lib/docker"),
  ("Package cache", "/var/cache/pacman/pkg"),
  ("Logs", "/var/log"),
  ("Swap file", "/swap"),
]


def run(cmd, timeout=60):
  try:
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout).stdout
  except Exception:
    return ""


def du(path, depth=0):
  """{path: bytes} for path (depth 0) or its children (depth 1), on one filesystem."""
  args = ["du", "-xb", "--max-depth", str(depth), path] if depth else ["du", "-xsb", path]
  out = run(["nice", "-n", "15", "ionice", "-c3", *args], timeout=300)
  sizes = {}
  for line in out.splitlines():
    size, _, p = line.partition("\t")
    try:
      sizes[p] = int(size)
    except ValueError:
      pass
  return sizes


# ------------------------------------------------------------------ overview

def smart():
  """Per-drive NVMe health through UDisks2 (no root needed)."""
  drives = {}
  tree = run(["busctl", "--system", "tree", "org.freedesktop.UDisks2", "--list"])
  for obj in tree.split():
    if "/drives/" not in obj:
      continue
    raw = run(["busctl", "--system", "--json=short", "call", "org.freedesktop.UDisks2", obj,
               "org.freedesktop.UDisks2.NVMe.Controller", "SmartGetAttributes", "a{sv}", "0"], timeout=10)
    props = run(["busctl", "--system", "--json=short", "get-property", "org.freedesktop.UDisks2", obj,
                 "org.freedesktop.UDisks2.NVMe.Controller", "SmartPowerOnHours", "SmartTemperature",
                 "SmartCriticalWarning"], timeout=10)
    serial = run(["busctl", "--system", "--json=short", "get-property", "org.freedesktop.UDisks2", obj,
                  "org.freedesktop.UDisks2.Drive", "Serial"], timeout=10)
    try:
      attrs = {k: v["data"] for k, v in json.loads(raw)["data"][0].items()}
    except (ValueError, KeyError, IndexError):
      continue
    vals = [json.loads(line)["data"] for line in props.splitlines() if line.strip()]
    try:
      ser = json.loads(serial)["data"]
    except ValueError:
      ser = ""
    drives[ser] = {
      "percentUsed": attrs.get("percent_used"),
      "spare": attrs.get("avail_spare"),
      "spareThreshold": attrs.get("spare_thresh"),
      "written": attrs.get("total_data_written"),
      "read": attrs.get("total_data_read"),
      "unsafeShutdowns": attrs.get("unsafe_shutdowns"),
      "mediaErrors": attrs.get("media_errors"),
      "powerOnHours": vals[0] if len(vals) > 0 else None,
      "temp": round(vals[1] - 273.15) if len(vals) > 1 and vals[1] else None,
      "criticalWarning": vals[2] if len(vals) > 2 else [],
    }
  return drives


def overview():
  lsblk = json.loads(run(["lsblk", "-J", "-b", "-e7", "-o",
                          "NAME,SIZE,TYPE,FSTYPE,LABEL,MOUNTPOINTS,FSAVAIL,FSUSED,FSSIZE,MODEL,SERIAL,ROTA"]) or "{}")
  health = smart()
  disks = []
  for dev in lsblk.get("blockdevices", []):
    if dev.get("type") != "disk" or dev["name"].startswith("zram"):
      continue
    parts = []

    def walk(node, depth):
      for child in node.get("children") or []:
        mounts = [m for m in (child.get("mountpoints") or []) if m]
        parts.append({"name": child["name"], "size": child.get("size") or 0, "type": child.get("type"),
                      "fstype": child.get("fstype") or "", "label": child.get("label") or "",
                      "mounts": mounts, "used": child.get("fsused"), "avail": child.get("fsavail"),
                      "fssize": child.get("fssize"), "depth": depth})
        walk(child, depth + 1)
    walk(dev, 0)
    fstypes = {p["fstype"] for p in parts}
    role = "Omarchy" if any("/" in p["mounts"] for p in parts) else ("Windows" if "ntfs" in fstypes else "")
    disks.append({"name": dev["name"], "model": (dev.get("model") or "").strip(), "size": dev.get("size") or 0,
                  "serial": dev.get("serial") or "", "role": role, "partitions": parts,
                  "health": health.get(dev.get("serial") or "", None)})
  disks.sort(key=lambda d: (d["role"] != "Omarchy", d["name"]))

  st = os.statvfs("/")
  root = {"size": st.f_blocks * st.f_frsize, "free": st.f_bavail * st.f_frsize,
          "used": (st.f_blocks - st.f_bfree) * st.f_frsize}
  btrfs = {}
  for line in run(["btrfs", "filesystem", "usage", "-b", "/"]).splitlines():
    m = re.match(r"\s*(Device size|Device allocated|Device unallocated|Used|Free \(estimated\)):\s+(\d+)(?:\s+\(min: (\d+)\))?", line)
    if m:
      key = {"Device size": "size", "Device allocated": "allocated", "Device unallocated": "unallocated",
             "Used": "used", "Free (estimated)": "freeEstimated"}[m.group(1)]
      btrfs[key] = int(m.group(2))
      if m.group(3):
        btrfs["freeMin"] = int(m.group(3))
  swaps = []
  for line in Path("/proc/swaps").read_text().splitlines()[1:]:
    p = line.split()
    swaps.append({"name": p[0], "type": p[1], "size": int(p[2]) * 1024, "used": int(p[3]) * 1024})
  extra = []
  gd = HOME / "GoogleDrive"
  if os.path.ismount(gd):
    try:
      q = json.loads((HOME / ".cache/omarchy-gdrive-widget/quota.json").read_text())
      extra.append({"name": "Google Drive", "path": str(gd), "size": q.get("total"),
                    "used": (q.get("used") or 0) + (q.get("other") or 0), "free": q.get("free")})
    except (OSError, ValueError):
      pass
  print(json.dumps({"disks": disks, "root": root, "btrfs": btrfs, "swaps": swaps, "extra": extra,
                    "updatedAt": int(time.time())}, separators=(",", ":")))


# ------------------------------------------------------------------ breakdown

def breakdown():
  st = os.statvfs("/")
  fs_used = (st.f_blocks - st.f_bfree) * st.f_frsize
  home_children = du(str(HOME), 1)
  home_total = home_children.pop(str(HOME), sum(home_children.values()))
  home = sorted(({"name": Path(p).name, "path": p, "size": s} for p, s in home_children.items()),
                key=lambda e: -e["size"])
  system = []
  for label, path in SYSTEM_AREAS:
    if os.path.exists(path):
      size = sum(du(path).values())
      if size > 0:
        system.append({"name": label, "path": path, "size": size})
  # /var/lib already contains Ollama, Flatpak and Docker; count the rest of it separately.
  var_lib = sum(du("/var/lib").values())
  inside = sum(e["size"] for e in system if e["path"].startswith("/var/lib/"))
  if var_lib - inside > 0:
    system.append({"name": "Other system data (/var/lib)", "path": "/var/lib", "size": var_lib - inside})
  system.sort(key=lambda e: -e["size"])
  measured = home_total + sum(e["size"] for e in system)
  result = {
    "fsUsed": fs_used, "home": {"total": home_total, "items": home[:14]},
    "system": {"total": sum(e["size"] for e in system), "items": system},
    "other": max(0, fs_used - measured),
    "overMeasured": max(0, measured - fs_used),  # zstd compression makes du add up to more than is used
    "updatedAt": int(time.time()),
  }
  CACHE.mkdir(parents=True, exist_ok=True)
  (CACHE / "breakdown.json").write_text(json.dumps(result))
  print(json.dumps(result, separators=(",", ":")))


def directory(path):
  path = os.path.realpath(os.path.expanduser(path))
  children = du(path, 1)
  total = children.pop(path, sum(children.values()))
  items = sorted(({"name": Path(p).name, "path": p, "size": s,
                   "isDir": os.path.isdir(p)} for p, s in children.items()), key=lambda e: -e["size"])
  # Files directly inside (du -max-depth 1 lists folders only)
  files = []
  try:
    for e in os.scandir(path):
      if e.is_file(follow_symlinks=False):
        try:
          files.append({"name": e.name, "path": e.path, "size": e.stat(follow_symlinks=False).st_blocks * 512,
                        "isDir": False})
        except OSError:
          pass
  except OSError:
    pass
  items = sorted(items + files, key=lambda e: -e["size"])[:25]
  print(json.dumps({"path": path, "total": total, "items": items}, separators=(",", ":")))


# ------------------------------------------------------------------ cleanup

def cleanup():
  items = []
  out = run(["paccache", "-dk1"], timeout=60)
  m = re.search(r"(\d+) candidate packages? .*?\(disk space saved: ([\d.]+) ([KMGT]i?B)\)", out)
  pkg_cache = sum(du("/var/cache/pacman/pkg").values())
  if m:
    mult = {"KiB": 2**10, "MiB": 2**20, "GiB": 2**30, "TiB": 2**40}.get(m.group(3), 1)
    items.append({"id": "paccache", "name": "Old package versions", "size": int(float(m.group(2)) * mult),
                  "detail": f"{m.group(1)} packages · keeps the installed version", "command": "sudo paccache -rk1",
                  "needsSudo": True})
  uninstalled = run(["paccache", "-dk0", "-u"], timeout=60)
  m = re.search(r"(\d+) candidate packages? .*?\(disk space saved: ([\d.]+) ([KMGT]i?B)\)", uninstalled)
  if m:
    mult = {"KiB": 2**10, "MiB": 2**20, "GiB": 2**30, "TiB": 2**40}.get(m.group(3), 1)
    items.append({"id": "paccache-u", "name": "Cached packages no longer installed", "size": int(float(m.group(2)) * mult),
                  "detail": f"{m.group(1)} packages", "command": "sudo paccache -ruk0", "needsSudo": True})
  if not any(i["id"].startswith("paccache") for i in items) and pkg_cache > 0:
    items.append({"id": "paccache-all", "name": "Package cache (installed versions)", "size": pkg_cache,
                  "detail": "only current versions; emptying it means no offline reinstall or downgrade",
                  "command": "sudo paccache -rk0", "needsSudo": True})
  orphans = run(["pacman", "-Qdtq"]).split()
  if orphans:
    size = 0
    for line in run(["pacman", "-Qi", *orphans]).splitlines():
      m = re.match(r"Installed Size\s*:\s*([\d.]+) ([KMG]iB)", line)
      if m:
        size += int(float(m.group(1)) * {"KiB": 2**10, "MiB": 2**20, "GiB": 2**30}[m.group(2)])
    items.append({"id": "orphans", "name": "Orphan packages", "size": size, "detail": ", ".join(orphans[:6]),
                  "command": "sudo pacman -Rns $(pacman -Qdtq)", "needsSudo": True})
  user_cache = sum(du(str(HOME / ".cache")).values())
  items.append({"id": "cache", "name": "Your app caches (~/.cache)", "size": user_cache,
                "detail": "Apps rebuild them; review before deleting", "open": str(HOME / ".cache")})
  trash = sum(du(str(HOME / ".local/share/Trash")).values()) if (HOME / ".local/share/Trash").exists() else 0
  if trash:
    items.append({"id": "trash", "name": "Trash", "size": trash, "detail": "", "command": "gio trash --empty"})
  m = re.search(r"take up ([\d.]+)([KMGT])", run(["journalctl", "--disk-usage"]))
  if m:
    items.append({"id": "journal", "name": "System journal", "size": int(float(m.group(1)) * {"K": 2**10, "M": 2**20, "G": 2**30, "T": 2**40}[m.group(2)]),
                  "detail": "keep 2 weeks", "command": "sudo journalctl --vacuum-time=2weeks", "needsSudo": True})
  items.sort(key=lambda e: -e["size"])
  print(json.dumps({"items": items, "packageCache": pkg_cache}, separators=(",", ":")))


def main(argv):
  cmd = argv[1] if len(argv) > 1 else "overview"
  if cmd == "overview":
    overview()
  elif cmd == "breakdown":
    breakdown()
  elif cmd == "dir" and len(argv) > 2:
    directory(argv[2])
  elif cmd == "cleanup":
    cleanup()
  else:
    print(json.dumps({"error": __doc__}))
    return 2
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
