"""Drivers, firmware and updates: omarchy-hwcheck health, NVIDIA driver and
DKMS per kernel, CUDA, pending updates (one checkupdates call, cached an hour),
and the installed BIOS against the newest one ASUS publishes (cached a day).
New NVIDIA/CUDA/kernel updates raise one notification per version."""

import json
import os
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

from .util import CACHE, cached, notify, read, run, write_json

DRIVER_PKGS = re.compile(
  r"^(linux(-lts|-zen|-hardened)?(-headers)?|linux-firmware.*|amd-ucode|intel-ucode|"
  r"nvidia.*|lib32-nvidia.*|opencl-nvidia|egl-wayland.*|mesa.*|lib32-mesa.*|vulkan-.*|lib32-vulkan-.*|"
  r"libva.*|bluez.*|sof-firmware|alsa-firmware|alsa-ucm-conf|wireless-regdb|"
  r"dkms|.*-dkms|hid-xpadneo.*|fwupd|systemd|limine.*)$")
GPU_PKGS = re.compile(r"^(nvidia.*|lib32-nvidia.*|opencl-nvidia|libva-nvidia-driver|egl-wayland.*|cuda|cuda-tools|"
                      r"cudnn|nccl|python-pytorch-cuda|ollama-cuda|linux|linux-headers|linux-lts|linux-lts-headers|dkms)$")
PROBE = Path(__file__).resolve().parent.parent / "cuda_probe.py"


def dmi(name):
  return read(f"/sys/class/dmi/id/{name}")


# ------------------------------------------------------------------ packages

def pkg_versions(pattern):
  out = {}
  for line in run(["pacman", "-Q"]).splitlines():
    parts = line.split()
    if len(parts) == 2 and re.match(pattern, parts[0]):
      out[parts[0]] = parts[1]
  return out


def parse_checkupdates(text):
  pkgs = []
  for line in text.splitlines():
    parts = line.split()
    if len(parts) >= 4 and parts[2] == "->":
      pkgs.append({"name": parts[0], "from": parts[1], "to": parts[3]})
  return pkgs


def all_updates():
  try:
    proc = subprocess.run(["checkupdates"], capture_output=True, text=True, timeout=120)
  except (OSError, subprocess.SubprocessError) as exc:
    return {"error": str(exc), "packages": []}
  # checkupdates: 0 = updates, 2 = none, 1 = error
  if proc.returncode == 1:
    return {"error": (proc.stderr.strip() or "checkupdates failed")[:200], "packages": []}
  return {"packages": parse_checkupdates(proc.stdout), "checkedAt": int(time.time())}


def split_updates(data):
  pkgs = data.get("packages") or []
  return {
    "total": len(pkgs),
    "drivers": [p for p in pkgs if DRIVER_PKGS.match(p["name"])],
    "gpu": [p for p in pkgs if GPU_PKGS.match(p["name"])],
    "checkedAt": data.get("checkedAt"), "error": data.get("error"), "stale": data.get("stale"),
  }


def notify_new_gpu(gpu_pkgs):
  path = CACHE / "notified-gpu.json"
  try:
    seen = set(json.loads(path.read_text()))
  except (OSError, ValueError):
    seen = set()
  keys = {f"{p['name']}={p['to']}" for p in gpu_pkgs}
  new = sorted(keys - seen)
  if new:
    body = "\n".join(k.replace("=", " → ") for k in new[:8])
    body += "\nUpdate with `omarchy update`; omarchy-hwcheck checks the NVIDIA build before you reboot."
    notify("NVIDIA / CUDA / kernel updates", body, app="Hardware")
  write_json(path, sorted(keys))


# ------------------------------------------------------------------ health

def health():
  try:
    data = json.loads(run(["omarchy-hwcheck", "--json"], timeout=60) or "{}")
  except ValueError as exc:
    return {"error": str(exc), "counts": {}, "issues": []}
  if not data:
    return {"error": "omarchy-hwcheck not available", "counts": {}, "issues": []}
  counts, issues = {}, []
  for f in data.get("findings", []):
    level = f.get("level", "ok")
    counts[level] = counts.get(level, 0) + 1
    if level not in ("ok", "info"):
      issues.append({"id": f.get("id"), "level": level, "title": f.get("title", ""),
                     "details": f.get("details", [])[:3], "hint": f.get("hint", "")})
  return {"counts": counts, "issues": issues}


# ------------------------------------------------------------------ NVIDIA

def nvidia():
  ver_text = read("/proc/driver/nvidia/version")
  loaded = read("/sys/module/nvidia/version")
  flavor = "open" if "Open Kernel Module" in ver_text else ("proprietary" if ver_text else "")
  pkgs = pkg_versions(r"^(nvidia.*|lib32-nvidia-utils|opencl-nvidia|libva-nvidia-driver)$")
  utils = pkgs.get("nvidia-utils", "").split("-")[0]
  module_pkg = next((k for k in pkgs if "dkms" in k or k in ("nvidia", "nvidia-open", "nvidia-lts", "nvidia-open-lts")), "")
  try:
    kernels = sorted(p.name for p in Path("/usr/lib/modules").iterdir() if (p / "vmlinuz").exists())
  except OSError:
    kernels = []
  dkms_out = run(["dkms", "status", "nvidia"])
  built = {}
  for k in kernels:
    line = next((l for l in dkms_out.splitlines() if f", {k}," in l), "")
    built[k] = "installed" in line
  gsp = sorted(p.name for p in Path(f"/usr/lib/firmware/nvidia/{loaded}").glob("gsp_*.bin*")) if loaded else []
  modeset = read("/sys/module/nvidia_drm/parameters/modeset")
  if not modeset:
    conf = run(["bash", "-c", "cat /etc/modprobe.d/*.conf /usr/lib/modprobe.d/*.conf /proc/cmdline 2>/dev/null"])
    modeset = "Y (configured)" if re.search(r"nvidia[-_]drm[ .]modeset=1", conf) else "unknown"
  issues = []
  if not loaded:
    issues.append("The nvidia module is not loaded")
  if loaded and utils and loaded != utils:
    issues.append(f"Loaded driver {loaded} differs from nvidia-utils {utils}: reboot pending")
  for k, ok in built.items():
    if not ok and module_pkg.endswith("dkms"):
      issues.append(f"DKMS module not built for kernel {k}: do not reboot into it")
  if loaded and not gsp:
    issues.append(f"No GSP firmware for {loaded}")
  q = "name,vbios_version,pcie.link.gen.max,pcie.link.width.max,persistence_mode"
  smi = run(["nvidia-smi", f"--query-gpu={q}", "--format=csv,noheader,nounits"], timeout=10).strip()
  card = None
  if smi:
    p = [x.strip() for x in smi.splitlines()[0].split(",")]
    if len(p) >= 5:
      card = {"name": p[0].replace("NVIDIA GeForce ", ""), "vbios": p[1], "pcie": f"Gen{p[2]} x{p[3]}", "persistence": p[4]}
  return {"loaded": loaded, "flavor": flavor, "utils": utils, "modulePackage": module_pkg, "card": card,
          "kernels": [{"kernel": k, "built": v, "running": k == os.uname().release} for k, v in built.items()],
          "gsp": len(gsp), "modeset": modeset, "issues": issues}


def cuda():
  try:
    info = json.loads(run([sys.executable, "-B", str(PROBE)], timeout=30) or "{}")
  except ValueError:
    info = {}
  if not info:
    info = {"works": False, "error": "CUDA check did not answer"}
  pk = pkg_versions(r"^(cuda|cuda-tools|cudnn|nccl|python-pytorch-cuda|ollama-cuda)$")
  info["toolkit"] = pk.get("cuda", "").split("-")[0]
  info["cudnn"] = pk.get("cudnn", "").split("-")[0]
  info["extras"] = {k: v.split("-")[0] for k, v in pk.items() if k not in ("cuda", "cudnn")}
  if info.get("toolkit") and info.get("driverCuda"):
    try:
      tk = tuple(int(x) for x in info["toolkit"].split(".")[:2])
      dr = tuple(int(x) for x in info["driverCuda"].split(".")[:2])
      info["compatible"] = tk <= dr
    except ValueError:
      pass
  return info


# ------------------------------------------------------------------ BIOS

def latest_bios():
  board, vendor = dmi("board_name"), dmi("board_vendor")
  if "ASUS" not in vendor.upper():
    return {"error": "only ASUS boards are supported", "board": board}
  url = ("https://www.asus.com/support/api/product.asmx/GetPDBIOS?"
         + urllib.parse.urlencode({"website": "us", "model": board}))
  try:
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0 omarchy-hardware-widget"})
    with urllib.request.urlopen(req, timeout=15) as r:
      data = json.loads(r.read())
  except Exception as exc:  # network, HTTP, JSON: all mean "could not check"
    return {"error": str(exc)[:200], "board": board}
  for group in ((data.get("Result") or {}).get("Obj") or []):
    if group.get("Name") != "BIOS":
      continue
    files = group.get("Files") or []
    if files:
      f = files[0]
      notes = re.sub(r"<br\s*/?>", "\n", str(f.get("Description") or "")).strip('"').strip()
      return {"board": board, "version": str(f.get("Version", "")),
              "date": str(f.get("ReleaseDate", "")).replace("/", "-"), "notes": notes,
              "beta": str(f.get("IsRelease", "1")) == "0",
              "page": "https://rog.asus.com/motherboards/rog-strix/" + board.lower().replace(" ", "-") + "/helpdesk_bios/"}
  return {"error": "no BIOS listed", "board": board}


def bios_newer(latest, installed):
  try:
    return int(re.sub(r"\D", "", latest or "0") or 0) > int(re.sub(r"\D", "", installed or "0") or 0)
  except ValueError:
    return False


def loaded_drivers():
  kernel = os.uname().release
  rows = [{"label": "Kernel", "version": kernel}]
  for label, module in (("NVIDIA", "nvidia"), ("Radeon (amdgpu)", "amdgpu"), ("Wi-Fi (mt7925e)", "mt7925e"),
                        ("Bluetooth (btusb)", "btusb"), ("Ethernet (igc)", "igc"), ("NVMe", "nvme"),
                        ("Xbox pad (xpad)", "xpad"), ("Board sensors (nct6775)", "nct6775")):
    v = read(f"/sys/module/{module}/version")
    if v or os.path.exists(f"/sys/module/{module}"):
      rows.append({"label": label, "version": v or "built-in / " + kernel})
  return rows


def collect(force=False):
  updates = cached("updates", 3600, all_updates, force)
  split = split_updates(updates)
  if not updates.get("error") and not updates.get("stale"):
    notify_new_gpu(split["gpu"])
  bios = cached("bios", 86400, latest_bios, force)
  installed = dmi("bios_version")
  return {
    "board": {"vendor": dmi("board_vendor"), "name": dmi("board_name"), "bios": installed, "biosDate": dmi("bios_date")},
    "bios": dict(bios, newer=bios_newer(bios.get("version"), installed)),
    "updates": split,
    "health": health(),
    "nvidia": nvidia(),
    "cuda": cuda(),
    "loaded": loaded_drivers(),
    "checkedAt": int(time.time()),
  }
