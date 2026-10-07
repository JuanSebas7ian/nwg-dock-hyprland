"""Live sensors, sampled every second: CPU (per thread), memory, temperatures,
fans and pump (nct6775 Super I/O), DDR5, NVMe, disk and network throughput,
the Radeon iGPU and the NVIDIA GPU (NVML). Read-only, no root.

Fan names and roles: ~/.config/omarchy-sysmon/fans.json
  {"2": "CPU cooler fan 1", "_roles": {"pump": ["7"], "cpu": ["2", "5"]}}
"""

import json
import os
import time

from . import gpu as gpu_mod
from .util import CONFIG_DIR, listdir, num, read

HWMON = "/sys/class/hwmon"
CPU = "/sys/devices/system/cpu"
BOARD_TEMPS = {"SYSTIN": "Motherboard", "CPUTIN": "CPU socket"}


def hwmons():
  """{driver name: [hwmon dirs]}"""
  out = {}
  for d in listdir(HWMON):
    base = f"{HWMON}/{d}"
    out.setdefault(read(f"{base}/name"), []).append(base)
  return out


def fan_config():
  try:
    cfg = json.loads((CONFIG_DIR / "omarchy-sysmon" / "fans.json").read_text())
  except (OSError, ValueError):
    cfg = {}
  roles = cfg.get("_roles") or {}
  names = {k: v for k, v in cfg.items() if not k.startswith("_")}
  return names, set(roles.get("pump", [])), set(roles.get("cpu", []))


def cpu_times():
  per = []
  for line in read("/proc/stat").splitlines():
    if not line.startswith("cpu"):
      break
    vals = list(map(int, line.split()[1:]))
    per.append((sum(vals), vals[3] + vals[4]))
  return per  # [total, cpu0, cpu1, ...]


def disk_bytes():
  r = w = 0
  for line in read("/proc/diskstats").splitlines():
    p = line.split()
    name = p[2]
    # whole NVMe/SATA devices only, not partitions, dm or loop
    if (name.startswith("nvme") and "p" not in name[4:]) or (name.startswith("sd") and name[-1].isalpha()):
      r += int(p[5]) * 512
      w += int(p[9]) * 512
  return r, w


def net_bytes():
  rx = tx = 0
  for line in read("/proc/net/dev").splitlines()[2:]:
    iface, _, data = line.partition(":")
    if iface.strip() == "lo" or not data:
      continue
    d = data.split()
    rx += int(d[0])
    tx += int(d[8])
  return rx, tx


def memory():
  info = {}
  for line in read("/proc/meminfo").splitlines():
    k, _, v = line.partition(":")
    try:
      info[k] = int(v.split()[0]) * 1024
    except (ValueError, IndexError):
      pass
  total = info.get("MemTotal", 0)
  return {"total": total, "used": total - info.get("MemAvailable", 0), "cached": info.get("Cached", 0),
          "swapTotal": info.get("SwapTotal", 0), "swapUsed": info.get("SwapTotal", 0) - info.get("SwapFree", 0)}


def igpu():
  for card in listdir("/sys/class/drm"):
    dev = f"/sys/class/drm/{card}/device"
    if "-" in card or read(f"{dev}/vendor") != "0x1002":
      continue
    return {"util": num(f"{dev}/gpu_busy_percent"), "vramUsed": num(f"{dev}/mem_info_vram_used"),
            "vramTotal": num(f"{dev}/mem_info_vram_total")}
  return None


class Sensors:
  def __init__(self, use_nvml=os.environ.get("HW_NO_NVML") != "1"):
    mons = hwmons()
    self.k10 = (mons.get("k10temp") or [None])[0]
    self.sio = next((v[0] for k, v in mons.items() if k.startswith("nct6")), None)
    self.spd = mons.get("spd5118", [])
    self.nvme = mons.get("nvme", [])
    self.amdgpu = (mons.get("amdgpu") or [None])[0]
    self.model = next((l.split(":", 1)[1].strip() for l in read("/proc/cpuinfo").splitlines()
                       if l.startswith("model name")), "CPU")
    self.threads = len(cpu_times()) - 1 or os.cpu_count() or 1
    self.fans = fan_config()
    self.gpu = gpu_mod.Nvml.open() if use_nvml else None
    self.prev = (cpu_times(), disk_bytes(), net_bytes(), time.monotonic())
    self.ticks = 0

  def cpu(self, now_times):
    loads = []
    for (t1, i1), (t0, i0) in zip(now_times, self.prev[0]):
      dt = t1 - t0
      loads.append(round(100 * (1 - (i1 - i0) / dt), 1) if dt > 0 else 0.0)
    freqs = [num(f"{CPU}/cpu{i}/cpufreq/scaling_cur_freq", 1000) for i in range(self.threads)]
    temps = {}
    if self.k10:
      for i in range(1, 12):
        label = read(f"{self.k10}/temp{i}_label")
        if label:
          temps[label] = num(f"{self.k10}/temp{i}_input", 1000)
    valid = [f for f in freqs if f]
    return {
      "model": self.model, "load": loads[0] if loads else 0, "threads": loads[1:],
      "freqs": [round(f) if f else 0 for f in freqs],
      "freqAvg": round(sum(valid) / len(valid)) if valid else None, "freqMax": max(valid) if valid else None,
      "tctl": temps.get("Tctl"), "ccd": [v for k, v in sorted(temps.items()) if k.startswith("Tccd")],
      "loadavg": [round(x, 2) for x in os.getloadavg()],
      "governor": read(f"{CPU}/cpu0/cpufreq/scaling_governor"),
      "epp": read(f"{CPU}/cpu0/cpufreq/energy_performance_preference"),
    }

  def cooling(self):
    names, pump_ids, cpu_ids = self.fans
    fans, board = [], []
    if self.sio:
      for i in range(1, 8):
        rpm = num(f"{self.sio}/fan{i}_input")
        if rpm is None:
          continue
        key = str(i)
        role = "pump" if key in pump_ids else "cpu" if key in cpu_ids else "case"
        if rpm > 0 or role != "case":
          fans.append({"id": key, "name": names.get(key, f"Fan {i}"), "rpm": int(rpm), "role": role})
      for i in range(1, 14):
        label = read(f"{self.sio}/temp{i}_label")
        if label in BOARD_TEMPS:
          t = num(f"{self.sio}/temp{i}_input", 1000)
          if t:
            board.append({"name": BOARD_TEMPS[label], "temp": round(t, 1)})
    return fans, board

  def sample(self, gpu=True, gpu_procs=False):
    """One snapshot. gpu=False skips NVML (the panel is closed and it is not the 5 s tick)."""
    self.ticks += 1
    if self.ticks % 10 == 0:
      self.fans = fan_config()  # pick up renames without a restart
    now = (cpu_times(), disk_bytes(), net_bytes(), time.monotonic())
    dt = max(0.001, now[3] - self.prev[3])
    cpu = self.cpu(now[0])
    fans, board = self.cooling()
    nvme = []
    for base in self.nvme:
      t = num(f"{base}/temp1_input", 1000)
      model = read(f"{base}/device/model") or "NVMe"
      if t is not None:
        nvme.append({"name": model.replace("WD_BLACK ", "").replace("Samsung SSD ", "")[:18], "temp": round(t, 1)})
    snap = {
      "t": time.time(),
      "cpu": cpu,
      "fans": fans,
      "board": board,
      "ram": [round(num(f"{d}/temp1_input", 1000) or 0, 1) for d in self.spd],
      "mem": memory(),
      "nvme": nvme,
      "disk": {"read": (now[1][0] - self.prev[1][0]) / dt, "write": (now[1][1] - self.prev[1][1]) / dt},
      "net": {"rx": (now[2][0] - self.prev[2][0]) / dt, "tx": (now[2][1] - self.prev[2][1]) / dt},
      "igpu": dict(igpu() or {}, temp=num(f"{self.amdgpu}/temp1_input", 1000)) if self.amdgpu else igpu(),
    }
    if gpu and self.gpu:
      snap["gpus"] = self.gpu.snapshot(gpu_procs)
    self.prev = now
    return snap
