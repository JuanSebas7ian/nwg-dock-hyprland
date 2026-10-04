#!/usr/bin/python3
"""CPU and cooling monitor backend: one JSON line per interval (default 1 s).

  cooling.py [interval]      stream
  cooling.py --once          one sample

CPU: total and per-thread load and clock, Tctl/Tccd (k10temp), load average,
governor and energy preference. Cooling: every spinning fan and the pump from
the motherboard's Super I/O (nct6775), board and socket temperatures, DDR5,
and the NVIDIA GPU's temperature and fan (NVML). Names and roles of the fans
come from ~/.config/omarchy-sysmon/fans.json ({"2": "CPU cooler fan 1",
"_roles": {"pump": ["7"], "cpu": ["2", "5"]}}). Read-only, no root.
"""

import ctypes
import json
import os
import sys
import time

HWMON = "/sys/class/hwmon"
CPU = "/sys/devices/system/cpu"


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


def hwmon(name_prefix):
  for d in sorted(os.listdir(HWMON)):
    if read(os.path.join(HWMON, d, "name")).startswith(name_prefix):
      return os.path.join(HWMON, d)
  return None


def fan_config():
  try:
    with open(os.path.expanduser("~/.config/omarchy-sysmon/fans.json")) as f:
      cfg = json.load(f)
  except (OSError, ValueError):
    cfg = {}
  roles = cfg.get("_roles") or {}
  return {k: v for k, v in cfg.items() if not k.startswith("_")}, set(roles.get("pump", [])), set(roles.get("cpu", []))


def cpu_times():
  per = []
  with open("/proc/stat") as f:
    for line in f:
      if not line.startswith("cpu"):
        break
      p = line.split()
      vals = list(map(int, p[1:]))
      per.append((sum(vals), vals[3] + vals[4]))
  return per  # [total, cpu0, cpu1, ...]


class Gpu:
  def __init__(self):
    try:
      self.lib = ctypes.CDLL("libnvidia-ml.so.1")
      self.ok = self.lib.nvmlInit_v2() == 0
      self.h = ctypes.c_void_p()
      self.ok = self.ok and self.lib.nvmlDeviceGetHandleByIndex_v2(0, ctypes.byref(self.h)) == 0
    except OSError:
      self.ok = False

  def sample(self):
    if not self.ok:
      return None
    t, fan, util = ctypes.c_uint(), ctypes.c_uint(), (ctypes.c_uint * 2)()
    self.lib.nvmlDeviceGetTemperature(self.h, 0, ctypes.byref(t))
    has_fan = self.lib.nvmlDeviceGetFanSpeed(self.h, ctypes.byref(fan)) == 0
    self.lib.nvmlDeviceGetUtilizationRates(self.h, util)
    return {"temp": t.value, "fan": fan.value if has_fan else None, "util": util[0]}


def main():
  once = "--once" in sys.argv
  args = [a for a in sys.argv[1:] if not a.startswith("--")]
  interval = 0.5 if once else float(args[0]) if args else 1.0
  k10 = hwmon("k10temp")
  sio = hwmon("nct6")
  spd = [os.path.join(HWMON, d) for d in sorted(os.listdir(HWMON)) if read(os.path.join(HWMON, d, "name")) == "spd5118"]
  model = next((l.split(":", 1)[1].strip() for l in read("/proc/cpuinfo").splitlines() if l.startswith("model name")), "CPU")
  threads = os.cpu_count() or 1
  gpu = Gpu()
  prev = cpu_times()
  names, pump_ids, cpu_ids = fan_config()
  tick = 0
  while True:
    time.sleep(interval)
    now = cpu_times()
    loads = []
    for (t1, i1), (t0, i0) in zip(now, prev):
      dt = t1 - t0
      loads.append(round(100 * (1 - (i1 - i0) / dt), 1) if dt > 0 else 0.0)
    prev = now
    if tick % 10 == 0:
      names, pump_ids, cpu_ids = fan_config()  # pick up renames without a restart
    tick += 1
    freqs = [num(f"{CPU}/cpu{i}/cpufreq/scaling_cur_freq", 1000) for i in range(threads)]
    temps = {}
    if k10:
      for i in range(1, 12):
        label = read(f"{k10}/temp{i}_label")
        if label:
          temps[label] = num(f"{k10}/temp{i}_input", 1000)
    fans, board = [], []
    if sio:
      for i in range(1, 8):
        rpm = num(f"{sio}/fan{i}_input")
        if rpm is None:
          continue
        key = str(i)
        role = "pump" if key in pump_ids else "cpu" if key in cpu_ids else "case"
        if rpm > 0 or key in pump_ids or key in cpu_ids:
          fans.append({"id": key, "name": names.get(key, f"Fan {i}"), "rpm": int(rpm), "role": role})
      for i in range(1, 14):
        label = read(f"{sio}/temp{i}_label")
        if label in ("SYSTIN", "CPUTIN"):
          t = num(f"{sio}/temp{i}_input", 1000)
          if t:
            board.append({"name": {"SYSTIN": "Motherboard", "CPUTIN": "CPU socket"}[label], "temp": round(t, 1)})
    ram = [round(num(f"{d}/temp1_input", 1000) or 0, 1) for d in spd]
    snap = {
      "cpu": {
        "model": model, "load": loads[0], "threads": loads[1:],
        "freqs": [round(f) if f else 0 for f in freqs], "freqMax": max((f or 0) for f in freqs),
        "tctl": temps.get("Tctl"), "ccd": [v for k, v in sorted(temps.items()) if k.startswith("Tccd")],
        "loadavg": [round(x, 2) for x in os.getloadavg()],
        "governor": read(f"{CPU}/cpu0/cpufreq/scaling_governor"),
        "epp": read(f"{CPU}/cpu0/cpufreq/energy_performance_preference"),
      },
      "fans": fans,
      "board": board,
      "ram": ram,
      "gpu": gpu.sample(),
      "t": time.time(),
    }
    print(json.dumps(snap, separators=(",", ":")), flush=True)
    if once:
      return


if __name__ == "__main__":
  try:
    main()
  except (KeyboardInterrupt, BrokenPipeError):
    pass
