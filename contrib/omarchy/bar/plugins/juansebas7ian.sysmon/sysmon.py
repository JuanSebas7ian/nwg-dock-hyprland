#!/usr/bin/python3
"""System monitor backend for the bar: prints one JSON snapshot per interval.

  sysmon.py [interval_seconds]     stream forever (default 2 s)
  sysmon.py --once                 one snapshot (rates measured over 0.5 s)

CPU, RAM, temperatures (k10temp, NVMe, DDR5 SPD, amdgpu), disk and network
throughput from /proc and /sys; NVIDIA through NVML (libnvidia-ml) via ctypes:
utilization, memory-controller load, VRAM, temperature, power, fan, clocks,
PCIe link and RX/TX throughput, encoder/decoder, and per-process GPU memory
and SM share. Read-only, no root.
"""

import ctypes
import json
import os
import sys
import time

HWMON = "/sys/class/hwmon"


def read(path, default=""):
  try:
    with open(path) as f:
      return f.read().strip()
  except OSError:
    return default


def num(path, scale=1.0):
  try:
    return float(read(path)) / scale
  except ValueError:
    return None


# ------------------------------------------------------------------ NVML

class Nvml:
  OK = 0

  class Util(ctypes.Structure):
    _fields_ = [("gpu", ctypes.c_uint), ("memory", ctypes.c_uint)]

  class Mem(ctypes.Structure):
    _fields_ = [("total", ctypes.c_ulonglong), ("free", ctypes.c_ulonglong), ("used", ctypes.c_ulonglong)]

  class Proc(ctypes.Structure):  # nvmlProcessInfo_v2/v3 share this layout
    _fields_ = [("pid", ctypes.c_uint), ("usedGpuMemory", ctypes.c_ulonglong),
                ("gpuInstanceId", ctypes.c_uint), ("computeInstanceId", ctypes.c_uint)]

  class ProcUtil(ctypes.Structure):
    _fields_ = [("pid", ctypes.c_uint), ("timeStamp", ctypes.c_ulonglong), ("smUtil", ctypes.c_uint),
                ("memUtil", ctypes.c_uint), ("encUtil", ctypes.c_uint), ("decUtil", ctypes.c_uint)]

  def __init__(self):
    self.lib = ctypes.CDLL("libnvidia-ml.so.1")
    if self.lib.nvmlInit_v2() != self.OK:
      raise OSError("nvmlInit failed")
    count = ctypes.c_uint()
    self.lib.nvmlDeviceGetCount_v2(ctypes.byref(count))
    self.handles = []
    for i in range(count.value):
      h = ctypes.c_void_p()
      if self.lib.nvmlDeviceGetHandleByIndex_v2(i, ctypes.byref(h)) == self.OK:
        self.handles.append(h)
    self.last_sample = {}

  def _uint(self, fn, h, *args):
    v = ctypes.c_uint()
    return v.value if getattr(self.lib, fn)(h, *args, ctypes.byref(v)) == self.OK else None

  def _name(self, h):
    buf = ctypes.create_string_buffer(96)
    self.lib.nvmlDeviceGetName(h, buf, 96)
    return buf.value.decode(errors="replace")

  def processes(self, h):
    procs = {}
    for fn in ("nvmlDeviceGetComputeRunningProcesses_v3", "nvmlDeviceGetGraphicsRunningProcesses_v3"):
      if not hasattr(self.lib, fn):
        continue
      n = ctypes.c_uint(64)
      arr = (self.Proc * 64)()
      if getattr(self.lib, fn)(h, ctypes.byref(n), arr) != self.OK:
        continue
      for p in arr[:n.value]:
        mem = p.usedGpuMemory if p.usedGpuMemory < (1 << 62) else 0
        entry = procs.setdefault(p.pid, {"pid": p.pid, "mem": 0, "type": ""})
        entry["mem"] = max(entry["mem"], mem)
        entry["type"] += "C" if "Compute" in fn else "G"
    # Per-process SM / encoder / decoder share since the previous call.
    n = ctypes.c_uint(64)
    arr = (self.ProcUtil * 64)()
    since = self.last_sample.get(id(h), 0)
    if self.lib.nvmlDeviceGetProcessUtilization(h, arr, ctypes.byref(n), ctypes.c_ulonglong(since)) == self.OK:
      for u in arr[:n.value]:
        if u.pid in procs:
          procs[u.pid].update(sm=u.smUtil, enc=u.encUtil, dec=u.decUtil)
        self.last_sample[id(h)] = max(self.last_sample.get(id(h), 0), u.timeStamp)
    out = []
    for p in procs.values():
      p["name"] = os.path.basename(read(f"/proc/{p['pid']}/comm") or str(p["pid"]))
      out.append(p)
    out.sort(key=lambda p: (-p.get("sm", 0), -p["mem"]))
    return out

  def snapshot(self, with_procs):
    gpus = []
    for i, h in enumerate(self.handles):
      util = self.Util()
      mem = self.Mem()
      self.lib.nvmlDeviceGetUtilizationRates(h, ctypes.byref(util))
      self.lib.nvmlDeviceGetMemoryInfo(h, ctypes.byref(mem))
      power = self._uint("nvmlDeviceGetPowerUsage", h)
      limit = self._uint("nvmlDeviceGetEnforcedPowerLimit", h)
      enc, dec = ctypes.c_uint(), ctypes.c_uint()
      period = ctypes.c_uint()
      self.lib.nvmlDeviceGetEncoderUtilization(h, ctypes.byref(enc), ctypes.byref(period))
      self.lib.nvmlDeviceGetDecoderUtilization(h, ctypes.byref(dec), ctypes.byref(period))
      gpu = {
        "index": i, "name": self._name(h).replace("NVIDIA GeForce ", ""),
        "util": util.gpu, "memUtil": util.memory,
        "vramUsed": mem.used, "vramTotal": mem.total,
        "temp": self._uint("nvmlDeviceGetTemperature", h, 0),
        "power": power / 1000 if power is not None else None,
        "powerLimit": limit / 1000 if limit is not None else None,
        "fan": self._uint("nvmlDeviceGetFanSpeed", h),
        "clockCore": self._uint("nvmlDeviceGetClockInfo", h, 0),
        "clockMem": self._uint("nvmlDeviceGetClockInfo", h, 2),
        "pcieGen": self._uint("nvmlDeviceGetCurrPcieLinkGeneration", h),
        "pcieGenMax": self._uint("nvmlDeviceGetMaxPcieLinkGeneration", h),
        "pcieWidth": self._uint("nvmlDeviceGetCurrPcieLinkWidth", h),
        # KB/s over the last 20 ms window
        "pcieRx": self._uint("nvmlDeviceGetPcieThroughput", h, 1),
        "pcieTx": self._uint("nvmlDeviceGetPcieThroughput", h, 0),
        "enc": enc.value, "dec": dec.value,
        "pstate": self._uint("nvmlDeviceGetPerformanceState", h),
      }
      if with_procs:
        gpu["processes"] = self.processes(h)
      gpus.append(gpu)
    return gpus


# ------------------------------------------------------------------ /proc

def cpu_times():
  with open("/proc/stat") as f:
    parts = f.readline().split()[1:]
  vals = list(map(int, parts))
  idle = vals[3] + vals[4]
  return sum(vals), idle


def disk_bytes():
  total_r = total_w = 0
  with open("/proc/diskstats") as f:
    for line in f:
      p = line.split()
      name = p[2]
      # whole NVMe/SATA devices only, not partitions or dm/loop
      if (name.startswith("nvme") and "p" not in name[4:]) or (name.startswith("sd") and name[-1].isalpha()):
        total_r += int(p[5]) * 512
        total_w += int(p[9]) * 512
  return total_r, total_w


def net_bytes():
  rx = tx = 0
  with open("/proc/net/dev") as f:
    for line in f.readlines()[2:]:
      iface, data = line.split(":", 1)
      if iface.strip() == "lo":
        continue
      d = data.split()
      rx += int(d[0])
      tx += int(d[8])
  return rx, tx


def memory():
  info = {}
  with open("/proc/meminfo") as f:
    for line in f:
      k, v = line.split(":", 1)
      info[k] = int(v.split()[0]) * 1024
  total = info.get("MemTotal", 0)
  avail = info.get("MemAvailable", 0)
  return {"total": total, "used": total - avail, "cached": info.get("Cached", 0),
          "swapTotal": info.get("SwapTotal", 0), "swapUsed": info.get("SwapTotal", 0) - info.get("SwapFree", 0)}


def temps():
  out = {"cpu": None, "nvme": [], "ram": [], "igpu": None, "fans": [], "board": []}
  for name in sorted(os.listdir(HWMON)):
    base = os.path.join(HWMON, name)
    kind = read(os.path.join(base, "name"))
    if kind == "k10temp":
      out["cpu"] = num(os.path.join(base, "temp1_input"), 1000)  # Tctl
    elif kind == "nvme":
      t = num(os.path.join(base, "temp1_input"), 1000)
      model = read(os.path.join(base, "device", "model")) or "NVMe"
      if t is not None:
        out["nvme"].append({"name": model.replace("WD_BLACK ", "").replace("Samsung SSD ", "")[:18], "temp": t})
    elif kind == "spd5118":
      t = num(os.path.join(base, "temp1_input"), 1000)
      if t is not None:
        out["ram"].append(t)
    elif kind == "amdgpu":
      out["igpu"] = num(os.path.join(base, "temp1_input"), 1000)
    elif kind.startswith("nct6"):
      # Motherboard Super I/O (nct6775 driver): fans and board temperatures.
      names = FAN_NAMES
      for i in range(1, 8):
        rpm = num(os.path.join(base, f"fan{i}_input"))
        if rpm:
          out["fans"].append({"name": names.get(str(i), f"Fan {i}"), "rpm": int(rpm)})
      for i in range(1, 14):
        label = read(os.path.join(base, f"temp{i}_label"))
        if label in ("SYSTIN", "CPUTIN"):
          t = num(os.path.join(base, f"temp{i}_input"), 1000)
          if t:
            out["board"].append({"name": {"SYSTIN": "Motherboard", "CPUTIN": "CPU socket"}[label], "temp": t})
  return out


def cpu_freq():
  freqs = []
  base = "/sys/devices/system/cpu"
  for d in os.listdir(base):
    if d.startswith("cpu") and d[3:].isdigit():
      f = num(os.path.join(base, d, "cpufreq", "scaling_cur_freq"), 1000)
      if f:
        freqs.append(f)
  return (sum(freqs) / len(freqs), max(freqs)) if freqs else (None, None)


def igpu():
  for card in sorted(os.listdir("/sys/class/drm")):
    dev = f"/sys/class/drm/{card}/device"
    if "-" in card or read(f"{dev}/vendor") != "0x1002":
      continue
    busy = num(f"{dev}/gpu_busy_percent")
    return {"util": busy, "vramUsed": num(f"{dev}/mem_info_vram_used"),
            "vramTotal": num(f"{dev}/mem_info_vram_total")}
  return None


# Optional fan names: ~/.config/omarchy-sysmon/fans.json, e.g. {"1": "CPU", "7": "AIO pump"}
try:
  FAN_NAMES = json.loads(open(os.path.expanduser("~/.config/omarchy-sysmon/fans.json")).read())
except (OSError, ValueError):
  FAN_NAMES = {}

CPU_MODEL = next((l.split(":", 1)[1].strip() for l in read("/proc/cpuinfo").splitlines()
                  if l.startswith("model name")), "CPU")


def main():
  once = "--once" in sys.argv
  args = [a for a in sys.argv[1:] if not a.startswith("--")]
  interval = 0.5 if once else float(args[0]) if args else 2.0
  try:
    nvml = Nvml()
  except Exception:
    nvml = None
  prev = (cpu_times(), disk_bytes(), net_bytes(), time.monotonic())
  if nvml:
    nvml.snapshot(True)  # prime per-process sampling
  procs_every = 0
  while True:
    time.sleep(interval)
    now = (cpu_times(), disk_bytes(), net_bytes(), time.monotonic())
    dt = max(0.001, now[3] - prev[3])
    total_d = now[0][0] - prev[0][0]
    idle_d = now[0][1] - prev[0][1]
    avg_f, max_f = cpu_freq()
    snap = {
      "cpu": {"model": CPU_MODEL, "usage": round(100 * (1 - idle_d / total_d), 1) if total_d else 0,
              "cores": os.cpu_count(), "freqAvg": avg_f, "freqMax": max_f,
              "load": os.getloadavg()},
      "mem": memory(),
      "temps": temps(),
      "disk": {"read": (now[1][0] - prev[1][0]) / dt, "write": (now[1][1] - prev[1][1]) / dt},
      "net": {"rx": (now[2][0] - prev[2][0]) / dt, "tx": (now[2][1] - prev[2][1]) / dt},
      "igpu": igpu(),
      "gpus": nvml.snapshot(procs_every == 0) if nvml else [],
    }
    procs_every = (procs_every + 1) % 2
    print(json.dumps(snap, separators=(",", ":")), flush=True)
    prev = now
    if once:
      return


if __name__ == "__main__":
  try:
    main()
  except (KeyboardInterrupt, BrokenPipeError):
    pass
