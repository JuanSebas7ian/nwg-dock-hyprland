"""Alerts from every domain, with thresholds from ~/.config/omarchy-hardware/config.json.

evaluate() turns the latest data into a list of
  {"id", "level": "crit"|"warn", "title", "detail", "tab"}
and Notifier sends a desktop notification when a critical alert (or a low
battery) first appears; it re-arms only after the alert has been clear for
`rearm` seconds, so a value hovering around a threshold does not spam."""

import time

from .util import notify


def evaluate(cfg, live=None, drivers=None, storage=None, peripherals=None):
  out = []

  def add(id_, level, title, detail, tab):
    out.append({"id": id_, "level": level, "title": title, "detail": detail, "tab": tab})

  if live:
    cpu = live.get("cpu") or {}
    t = cpu.get("tctl")
    if t is not None and t >= cfg["cpuCrit"]:
      add("cpu-hot", "crit", f"CPU at {round(t)} °C", f"Critical threshold {cfg['cpuCrit']} °C.", "cpu")
    elif t is not None and t >= cfg["cpuWarn"]:
      add("cpu-warm", "warn", f"CPU at {round(t)} °C", f"Warning threshold {cfg['cpuWarn']} °C.", "cpu")
    for f in live.get("fans") or []:
      if f.get("role") == "pump" and f.get("rpm", 0) < cfg["pumpMinRpm"]:
        add("pump", "crit", "AIO pump stopped",
            f"{f['name']} reads {f['rpm']} RPM. Check the pump cable (AIO_PUMP) before using the PC hard.", "cpu")
    for g in live.get("gpus") or []:
      if g.get("temp") is not None and g["temp"] >= cfg["gpuWarn"]:
        add(f"gpu-hot-{g['index']}", "warn", f"GPU at {g['temp']} °C", f"Warning threshold {cfg['gpuWarn']} °C.", "gpu")
    mem = live.get("mem") or {}
    if mem.get("total") and mem["used"] / mem["total"] >= cfg["ramWarn"]:
      add("ram", "warn", f"RAM {round(100 * mem['used'] / mem['total'])}% used", "Close something or check for a leak.", "cpu")

  if storage:
    root = storage.get("root") or {}
    if root.get("size") and root["free"] / root["size"] < cfg["diskFreeWarn"]:
      add("disk-free", "warn", "System disk almost full", f"{round(root['free'] / 1e9)} GB free.", "storage")
    for d in storage.get("disks") or []:
      h = d.get("health") or {}
      if not h:
        continue
      if h.get("criticalWarning") or (h.get("mediaErrors") or 0) > 0:
        add(f"smart-{d['name']}", "crit", f"{d['model']}: SMART warning",
            "Back up now and check the Storage tab.", "storage")
      elif (h.get("percentUsed") or 0) >= 80 or (h.get("spare") is not None and h.get("spare") <= (h.get("spareThreshold") or 0)):
        add(f"wear-{d['name']}", "warn", f"{d['model']}: {h.get('percentUsed')}% worn", "Plan a replacement.", "storage")

  if drivers:
    for issue in (drivers.get("nvidia") or {}).get("issues") or []:
      add("nvidia-" + str(abs(hash(issue)) % 10**6), "crit", "NVIDIA driver", issue, "drivers")
    cuda = drivers.get("cuda") or {}
    if cuda and not cuda.get("works"):
      add("cuda", "warn", "CUDA not working", cuda.get("error", ""), "gpu")
    for i in (drivers.get("health") or {}).get("issues") or []:
      level = "crit" if i["level"] in ("fail", "regression") else "warn"
      add("hwcheck-" + str(i["id"]), level, f"{i['id']} {i['title']}", "; ".join(i.get("details") or [])[:160], "drivers")

  if peripherals:
    bt = peripherals.get("bluetooth") or {}
    if bt.get("adapter") is None and not bt.get("blocked"):
      add("bt-missing", "warn", "No Bluetooth adapter", "bt-guardian tries to recover it; after Windows, cut the power.", "devices")
    for d in bt.get("devices") or []:
      if d.get("connected") and d.get("battery") is not None and d["battery"] <= cfg["batteryWarn"]:
        add("battery-" + d["mac"], "warn", f"{d['name']} battery {d['battery']}%", "Charge it soon.", "devices")
    for b in peripherals.get("batteries") or []:
      if b.get("percent") is not None and b["percent"] <= cfg["batteryWarn"]:
        add("battery-" + b["name"], "warn", f"{b['name']} battery {b['percent']}%", "Charge it soon.", "devices")
  order = {"crit": 0, "warn": 1}
  out.sort(key=lambda a: order[a["level"]])
  return out


class Notifier:
  def __init__(self, rearm=600):
    self.active = {}   # id -> time it was last seen
    self.rearm = rearm

  def update(self, alerts, now=None):
    """Returns the alerts that were notified now."""
    now = now or time.time()
    sent = []
    for a in alerts:
      notifiable = a["level"] == "crit" or a["id"].startswith("battery-")
      if notifiable and a["id"] not in self.active:
        notify(a["title"], a["detail"], "critical" if a["level"] == "crit" else "normal")
        sent.append(a)
      self.active[a["id"]] = now
    for id_, seen in list(self.active.items()):
      if now - seen > self.rearm:
        del self.active[id_]
    return sent
