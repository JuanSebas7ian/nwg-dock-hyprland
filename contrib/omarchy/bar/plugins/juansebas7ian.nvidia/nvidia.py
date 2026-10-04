#!/usr/bin/python3
"""NVIDIA backend for the bar: driver, kernel module, CUDA and updates as JSON.

  nvidia.py [--force]

- Driver: loaded module (open or proprietary) against the installed packages,
  DKMS build for every installed kernel, GSP firmware, nvidia_drm modeset.
- CUDA: driver CUDA version and a live check through libcuda (cuInit, device,
  compute capability, free memory), the CUDA toolkit and cuDNN packages.
- Updates: NVIDIA/CUDA/kernel packages pending in the Arch repos (checkupdates,
  no root, cached for an hour). New ones raise a desktop notification once.
"""

import ctypes
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

CACHE = Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache") / "omarchy-nvidia-widget"
WATCH = re.compile(r"^(nvidia.*|lib32-nvidia.*|opencl-nvidia|libva-nvidia-driver|egl-wayland.*|cuda|cuda-tools|"
                   r"cudnn|nccl|python-pytorch-cuda|ollama-cuda|linux|linux-headers|linux-lts|linux-lts-headers|dkms)$")


def read(path):
  try:
    return Path(path).read_text().strip()
  except OSError:
    return ""


def pkg_versions(names):
  out = subprocess.run(["pacman", "-Q", *names], capture_output=True, text=True).stdout
  return dict(line.split() for line in out.splitlines() if len(line.split()) == 2)


def installed_matching(pattern):
  out = subprocess.run(["pacman", "-Qq"], capture_output=True, text=True).stdout.split()
  return [p for p in out if re.match(pattern, p)]


def driver():
  ver_text = read("/proc/driver/nvidia/version")
  loaded = read("/sys/module/nvidia/version")
  flavor = "open" if "Open Kernel Module" in ver_text else ("proprietary" if ver_text else "")
  pkgs = pkg_versions(installed_matching(r"^(nvidia.*|lib32-nvidia-utils|opencl-nvidia|libva-nvidia-driver)$"))
  utils = pkgs.get("nvidia-utils", "").split("-")[0]
  module_pkg = next((k for k in pkgs if k.startswith("nvidia") and ("dkms" in k or k in ("nvidia", "nvidia-open", "nvidia-lts", "nvidia-open-lts"))), "")
  kernels = sorted(p.name for p in Path("/usr/lib/modules").iterdir() if (p / "build").exists() or (p / "vmlinuz").exists())
  dkms_out = subprocess.run(["dkms", "status", "nvidia"], capture_output=True, text=True).stdout
  built = {k: (f", {k}," in dkms_out and "installed" in next((l for l in dkms_out.splitlines() if f", {k}," in l), ""))
           for k in kernels}
  gsp = sorted(p.name for p in Path(f"/usr/lib/firmware/nvidia/{loaded}").glob("gsp_*.bin")) if loaded else []
  modeset = read("/sys/module/nvidia_drm/parameters/modeset")
  if not modeset:
    conf = subprocess.run(["bash", "-c", "cat /etc/modprobe.d/*.conf /usr/lib/modprobe.d/*.conf /proc/cmdline 2>/dev/null"],
                          capture_output=True, text=True).stdout
    modeset = "Y (configured)" if re.search(r"nvidia[-_]drm[ .]modeset=1", conf) else "unknown"
  issues = []
  if not loaded:
    issues.append("The nvidia module is not loaded")
  if loaded and utils and loaded != utils:
    issues.append(f"Loaded driver {loaded} differs from nvidia-utils {utils}: reboot pending")
  for k, ok in built.items():
    if not ok:
      issues.append(f"DKMS module not built for kernel {k}: do not reboot into it")
  if loaded and not gsp:
    issues.append(f"No GSP firmware for {loaded}")
  return {"loaded": loaded, "flavor": flavor, "utils": utils, "modulePackage": module_pkg,
          "packages": pkgs, "kernels": [{"kernel": k, "built": v, "running": k == os.uname().release} for k, v in built.items()],
          "gsp": gsp, "modeset": modeset, "issues": issues}


def cuda():
  info = {"works": False}
  try:
    cu = ctypes.CDLL("libcuda.so.1")
    rc = cu.cuInit(0)
    if rc != 0:
      info["error"] = f"cuInit failed ({rc})"
    else:
      v = ctypes.c_int()
      cu.cuDriverGetVersion(ctypes.byref(v))
      info["driverCuda"] = f"{v.value // 1000}.{(v.value % 1000) // 10}"
      n = ctypes.c_int()
      cu.cuDeviceGetCount(ctypes.byref(n))
      devices = []
      for i in range(n.value):
        dev = ctypes.c_int()
        cu.cuDeviceGet(ctypes.byref(dev), i)
        name = ctypes.create_string_buffer(100)
        cu.cuDeviceGetName(name, 100, dev)
        ma, mi = ctypes.c_int(), ctypes.c_int()
        cu.cuDeviceGetAttribute(ctypes.byref(ma), 75, dev)
        cu.cuDeviceGetAttribute(ctypes.byref(mi), 76, dev)
        devices.append({"name": name.value.decode(), "cc": f"{ma.value}.{mi.value}"})
      info["devices"] = devices
      info["works"] = n.value > 0
  except OSError as exc:
    info["error"] = f"libcuda not available ({exc})"
  pk = pkg_versions(installed_matching(r"^(cuda|cuda-tools|cudnn|nccl|python-pytorch-cuda|ollama-cuda)$"))
  info["toolkit"] = pk.get("cuda", "").split("-")[0]
  info["cudnn"] = pk.get("cudnn", "").split("-")[0]
  info["extras"] = {k: v.split("-")[0] for k, v in pk.items() if k not in ("cuda", "cudnn")}
  nvcc = Path("/opt/cuda/bin/nvcc")
  if nvcc.exists():
    m = re.search(r"release ([\d.]+)", subprocess.run([str(nvcc), "--version"], capture_output=True, text=True).stdout)
    info["nvcc"] = m.group(1) if m else ""
  if info.get("toolkit") and info.get("driverCuda"):
    tk = tuple(int(x) for x in info["toolkit"].split(".")[:2])
    dr = tuple(int(x) for x in info["driverCuda"].split(".")[:2])
    info["compatible"] = tk <= dr
  return info


def gpu():
  q = "name,vbios_version,pcie.link.gen.max,pcie.link.width.max,temperature.gpu,power.draw,memory.used,memory.total,persistence_mode"
  out = subprocess.run(["nvidia-smi", f"--query-gpu={q}", "--format=csv,noheader,nounits"],
                       capture_output=True, text=True, timeout=10).stdout.strip()
  if not out:
    return None
  p = [x.strip() for x in out.splitlines()[0].split(",")]
  return {"name": p[0].replace("NVIDIA GeForce ", ""), "vbios": p[1], "pcie": f"Gen{p[2]} x{p[3]}",
          "temp": p[4], "power": p[5], "vram": f"{float(p[6]) / 1024:.1f} / {float(p[7]) / 1024:.0f} GB",
          "persistence": p[8]}


def updates(force):
  path = CACHE / "updates.json"
  if not force:
    try:
      if time.time() - path.stat().st_mtime < 3600:
        return json.loads(path.read_text())
    except (OSError, ValueError):
      pass
  proc = subprocess.run(["checkupdates"], capture_output=True, text=True, timeout=120)
  if proc.returncode == 1:
    try:
      return dict(json.loads(path.read_text()), stale=True)
    except (OSError, ValueError):
      return {"error": proc.stderr.strip()[:200] or "checkupdates failed", "packages": []}
  pkgs = []
  for line in proc.stdout.splitlines():
    parts = line.split()
    if len(parts) >= 4 and WATCH.match(parts[0]):
      pkgs.append({"name": parts[0], "from": parts[1], "to": parts[3]})
  data = {"packages": pkgs, "checkedAt": int(time.time())}
  CACHE.mkdir(parents=True, exist_ok=True)
  path.write_text(json.dumps(data))
  notify_new(pkgs)
  return data


def notify_new(pkgs):
  seen_path = CACHE / "notified.json"
  try:
    seen = set(json.loads(seen_path.read_text()))
  except (OSError, ValueError):
    seen = set()
  keys = {f"{p['name']}={p['to']}" for p in pkgs}
  new = sorted(keys - seen)
  if new:
    body = "\n".join(k.replace("=", " → ") for k in new[:8])
    if any(k.startswith(("linux=", "nvidia")) for k in new):
      body += "\nUpdate with `omarchy update`; omarchy-hwcheck checks the NVIDIA build before you reboot."
    subprocess.run(["notify-send", "-a", "NVIDIA", "NVIDIA / CUDA updates available", body],
                   capture_output=True)
  seen_path.write_text(json.dumps(sorted(keys)))


def main():
  force = "--force" in sys.argv
  print(json.dumps({"driver": driver(), "cuda": cuda(), "gpu": gpu(), "updates": updates(force)},
                   separators=(",", ":")))


if __name__ == "__main__":
  main()
