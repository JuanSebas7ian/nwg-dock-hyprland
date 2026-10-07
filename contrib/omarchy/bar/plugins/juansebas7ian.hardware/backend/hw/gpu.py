"""NVIDIA GPU through NVML (libnvidia-ml) via ctypes: utilization, memory
bandwidth, VRAM, temperature, power, fan, clocks, PCIe link and traffic,
encoder/decoder, and the processes using it."""

import ctypes
import os

from .util import read


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

  @classmethod
  def open(cls):
    try:
      return cls()
    except OSError:
      return None

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

  def snapshot(self, with_procs=False):
    gpus = []
    for i, h in enumerate(self.handles):
      util, mem = self.Util(), self.Mem()
      self.lib.nvmlDeviceGetUtilizationRates(h, ctypes.byref(util))
      self.lib.nvmlDeviceGetMemoryInfo(h, ctypes.byref(mem))
      power = self._uint("nvmlDeviceGetPowerUsage", h)
      limit = self._uint("nvmlDeviceGetEnforcedPowerLimit", h)
      enc, dec, period = ctypes.c_uint(), ctypes.c_uint(), ctypes.c_uint()
      self.lib.nvmlDeviceGetEncoderUtilization(h, ctypes.byref(enc), ctypes.byref(period))
      self.lib.nvmlDeviceGetDecoderUtilization(h, ctypes.byref(dec), ctypes.byref(period))
      gpu = {
        "index": i, "name": self._name(h).replace("NVIDIA GeForce ", ""),
        "util": util.gpu, "memUtil": util.memory, "vramUsed": mem.used, "vramTotal": mem.total,
        "temp": self._uint("nvmlDeviceGetTemperature", h, 0),
        "power": power / 1000 if power is not None else None,
        "powerLimit": limit / 1000 if limit is not None else None,
        "fan": self._uint("nvmlDeviceGetFanSpeed", h),
        "clockCore": self._uint("nvmlDeviceGetClockInfo", h, 0),
        "clockMem": self._uint("nvmlDeviceGetClockInfo", h, 2),
        "pcieGen": self._uint("nvmlDeviceGetCurrPcieLinkGeneration", h),
        "pcieGenMax": self._uint("nvmlDeviceGetMaxPcieLinkGeneration", h),
        "pcieWidth": self._uint("nvmlDeviceGetCurrPcieLinkWidth", h),
        "pcieRx": self._uint("nvmlDeviceGetPcieThroughput", h, 1),  # KB/s, 20 ms window
        "pcieTx": self._uint("nvmlDeviceGetPcieThroughput", h, 0),
        "enc": enc.value, "dec": dec.value,
        "pstate": self._uint("nvmlDeviceGetPerformanceState", h),
      }
      if with_procs:
        gpu["processes"] = self.processes(h)
      gpus.append(gpu)
    return gpus
