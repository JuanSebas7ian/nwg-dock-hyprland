#!/usr/bin/python3
"""Live CUDA check through libcuda: prints JSON. Runs in its own short-lived
process so the long-running backend never keeps a CUDA driver context."""

import ctypes
import json

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
print(json.dumps(info))
