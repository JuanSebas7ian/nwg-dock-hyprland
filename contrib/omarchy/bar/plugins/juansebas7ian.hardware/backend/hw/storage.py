"""Storage: disks, partitions and NVMe health (UDisks2, no root), what uses
the system disk with drill-down, Steam per game, disk firmware against the
vendor's latest ("Samsung Magician" for Linux), what is available and what can
be reclaimed. du runs with nice/ionice; the slow parts run only when the
Storage tab is open."""

import json
import os
import re
import subprocess
import time
from pathlib import Path

from .util import CACHE, HOME, run

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
  return {"disks": disks, "root": root, "btrfs": btrfs, "swaps": swaps, "extra": extra,
          "updatedAt": int(time.time())}


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
  return result


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
  return {"path": path, "total": total, "items": items}


# ------------------------------------------------------------------ firmware ("Magician")

# Rated endurance (TBW) by model, from the manufacturers' spec sheets.
TBW = {"Samsung SSD 980 500GB": 300, "Samsung SSD 980 1TB": 600, "Samsung SSD 980 PRO 500GB": 300,
       "Samsung SSD 980 PRO 1TB": 600, "Samsung SSD 990 PRO 1TB": 600, "Samsung SSD 990 PRO 2TB": 1200,
       "WD_BLACK SN770 500GB": 300, "WD_BLACK SN770 1TB": 600, "WD_BLACK SN850X 1TB": 600}
SAMSUNG_TOOLS = "https://semiconductor.samsung.com/consumer-storage/support/tools/"


def samsung_latest():
  """{model family: {version, iso}} scraped from Samsung's tools page, cached a day."""
  path = CACHE / "samsung-firmware.json"
  try:
    if time.time() - path.stat().st_mtime < 86400:
      return json.loads(path.read_text())
  except (OSError, ValueError):
    pass
  import urllib.request
  try:
    req = urllib.request.Request(SAMSUNG_TOOLS, headers={"User-Agent": "Mozilla/5.0"})
    page = urllib.request.urlopen(req, timeout=20).read().decode("utf-8", "replace")
  except Exception:
    try:
      return json.loads(path.read_text())
    except (OSError, ValueError):
      return {}
  found = {}
  for url in set(re.findall(r'https://[^"\s]+/Samsung_SSD_([0-9A-Za-z_]+?)_([0-9A-Z]{8})\.iso', page)):
    family, version = url
    found[family.replace("_", " ")] = {"version": version}
  for m in re.finditer(r'(https://[^"\s]+/Samsung_SSD_([0-9A-Za-z_]+?)_([0-9A-Z]{8})\.iso)', page):
    found.setdefault(m.group(2).replace("_", " "), {})["iso"] = m.group(1)
  CACHE.mkdir(parents=True, exist_ok=True)
  path.write_text(json.dumps(found))
  return found


def fwupd_updates():
  try:
    data = json.loads(run(["fwupdmgr", "get-updates", "--json"], timeout=60) or "{}")
  except ValueError:
    return {}
  return {d.get("Serial") or d.get("Name"): [r.get("Version") for r in d.get("Releases", [])] for d in data.get("Devices", [])}


def selftest(obj):
  out = run(["busctl", "--system", "--json=short", "get-property", "org.freedesktop.UDisks2", obj,
             "org.freedesktop.UDisks2.NVMe.Controller", "SmartSelftestStatus", "SmartSelftestPercentRemaining"])
  vals = [json.loads(l)["data"] for l in out.splitlines() if l.strip()]
  return {"status": vals[0] if vals else "", "remaining": vals[1] if len(vals) > 1 else -1}


def firmware():
  health = smart()
  samsung = samsung_latest()
  fw_updates = fwupd_updates()
  objs = {}
  for obj in run(["busctl", "--system", "tree", "org.freedesktop.UDisks2", "--list"]).split():
    if "/drives/" in obj:
      ser = run(["busctl", "--system", "--json=short", "get-property", "org.freedesktop.UDisks2", obj,
                 "org.freedesktop.UDisks2.Drive", "Serial"])
      try:
        objs[json.loads(ser)["data"]] = obj
      except ValueError:
        pass
  drives = []
  for ctrl in sorted(Path("/sys/class/nvme").iterdir()):
    model = (ctrl / "model").read_text().strip()
    serial = (ctrl / "serial").read_text().strip()
    fw = (ctrl / "firmware_rev").read_text().strip()
    h = health.get(serial) or {}
    entry = {"model": model, "serial": serial, "firmware": fw, "vendor": model.split()[0].replace("_", " "),
             "tbw": TBW.get(model), "written": h.get("written"), "health": h, "udisks": objs.get(serial, ""),
             "selftest": selftest(objs[serial]) if serial in objs else {}}
    if model.startswith("Samsung"):
      family = re.sub(r"^Samsung SSD |\s*\d+(GB|TB)$", "", model).strip()
      info = samsung.get(family) or {}
      entry.update(latest=info.get("version", ""), source="Samsung", download=info.get("iso", SAMSUNG_TOOLS),
                   note="Samsung Magician has no Linux version for consumer SSDs; firmware ships as a bootable ISO.")
    else:
      versions = fw_updates.get(serial) or []
      entry.update(latest=versions[0] if versions else fw, source="LVFS (fwupd)",
                   download="fwupdmgr update" if versions else "")
    entry["upToDate"] = not entry.get("latest") or entry["latest"] == fw
    drives.append(entry)
  return {"drives": drives}


def start_selftest(obj, kind):
  # UDisks asks the polkit agent (Omarchy's shell has one) for permission.
  out = subprocess.run(["busctl", "--system", "call", "org.freedesktop.UDisks2", obj,
                        "org.freedesktop.UDisks2.NVMe.Controller", "SmartSelftestStart", "sa{sv}", kind, "0"],
                       capture_output=True, text=True, timeout=120)
  return {"ok": out.returncode == 0, "error": out.stderr.strip()}


# ------------------------------------------------------------------ steam

TOOLS = re.compile(r"(?i)^(proton|steam linux runtime|steamworks common|steamvr)")


def vdf_values(text, key):
  return re.findall(r'"' + re.escape(key) + r'"\s+"([^"]*)"', text)


def steam():
  """Installed Steam games per library, with Proton prefix and shader cache."""
  roots = [HOME / ".local/share/Steam", HOME / ".steam/steam",
           HOME / ".var/app/com.valvesoftware.Steam/.local/share/Steam"]
  libraries = []
  for r in roots:
    vdf = r / "steamapps" / "libraryfolders.vdf"
    if vdf.exists():
      for path in vdf_values(vdf.read_text(errors="replace"), "path"):
        if path not in libraries:
          libraries.append(path)
  games, tools = [], []
  for lib in libraries:
    apps = Path(lib) / "steamapps"
    for acf in sorted(apps.glob("appmanifest_*.acf")):
      text = acf.read_text(errors="replace")
      appid = (vdf_values(text, "appid") or [""])[0]
      name = (vdf_values(text, "name") or [appid])[0]
      installdir = (vdf_values(text, "installdir") or [""])[0]
      size = int((vdf_values(text, "SizeOnDisk") or ["0"])[0] or 0)
      last = int((vdf_values(text, "LastPlayed") or ["0"])[0] or 0)
      game_dir = apps / "common" / installdir
      prefix = sum(du(str(apps / "compatdata" / appid)).values()) if (apps / "compatdata" / appid).exists() else 0
      shaders = sum(du(str(apps / "shadercache" / appid)).values()) if (apps / "shadercache" / appid).exists() else 0
      entry = {"appid": appid, "name": name, "size": size, "prefix": prefix, "shaders": shaders,
               "total": size + prefix + shaders, "lastPlayed": last, "path": str(game_dir), "library": lib}
      (tools if TOOLS.match(name) else games).append(entry)
  games.sort(key=lambda g: -g["total"])
  tools.sort(key=lambda g: -g["total"])
  steam_dir = next((str(r) for r in roots if r.exists()), "")
  total = sum(du(steam_dir).values()) if steam_dir else 0
  listed = sum(g["total"] for g in games + tools)
  return {"libraries": libraries, "games": games, "tools": tools, "total": total,
          "client": max(0, total - listed), "path": steam_dir}


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
  return {"items": items, "packageCache": pkg_cache}

