#!/usr/bin/python3
"""Backend of the juansebas7ian.hardware bar widget: one long-running process
for every hardware tab (CPU & cooling, GPU, storage, drivers, peripherals).

Writes one JSON object per line on stdout: {"type": <topic>, "data": ...}
  live         sensors (1 s while the panel is open on a live tab, 5 s otherwise)
  history      the last 120 one-second samples, sent when the panel opens
  drivers      health, NVIDIA/CUDA, updates, BIOS        (startup + every 30 min)
  storage.*    overview (10 min) · breakdown, steam, cleanup, firmware (Storage tab, cached 10 min)
  peripherals  Bluetooth, batteries, input, USB, audio   (every 3 s on the Devices tab, 60 s otherwise)
  alerts       every active alert across domains
  result       answer to an action

Reads commands on stdin, one JSON object per line:
  {"cmd": "view", "open": true, "tab": "cpu"}
  {"cmd": "refresh", "what": "drivers"|"storage"|"peripherals", "force": true}
  {"cmd": "dir", "path": "~/Downloads"}           storage drill-down
  {"cmd": "selftest", "obj": "/org/.../drives/X"}  SMART short self-test (polkit)
  {"cmd": "bt", "action": "connect"|"disconnect"|"autoconnect"|"visible", "mac": "...", "on": true}

  hardwared.py --once [topic]   print one topic and exit (live, drivers, storage, peripherals)
"""

import json
import os
import queue
import sys
import threading
import time
from collections import deque
from concurrent.futures import ThreadPoolExecutor

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from hw import alerts, drivers, peripherals, storage  # noqa: E402
from hw.sensors import Sensors  # noqa: E402
from hw.util import config  # noqa: E402

LIVE_TABS = {"summary", "cpu", "gpu"}
HISTORY = 120


class Daemon:
  def __init__(self, out=sys.stdout):
    self.out = out
    self.lock = threading.Lock()
    self.commands = queue.Queue()
    self.pool = ThreadPoolExecutor(max_workers=3)
    self.busy = set()
    self.cfg = config()
    self.sensors = Sensors()
    self.history = deque(maxlen=HISTORY)
    self.opened = False
    self.tab = "summary"
    self.latest = {"live": None, "drivers": None, "storage": None, "peripherals": None}
    self.notifier = alerts.Notifier()
    self.last_alerts = None
    self.due = {"drivers": time.monotonic() + 20, "storage": time.monotonic() + 5, "peripherals": time.monotonic() + 8}

  # ---------------------------------------------------------------- output
  def emit(self, topic, data):
    line = json.dumps({"type": topic, "data": data}, separators=(",", ":"))
    with self.lock:
      try:
        self.out.write(line + "\n")
        self.out.flush()
      except BrokenPipeError:
        os._exit(0)

  def publish_alerts(self):
    active = alerts.evaluate(self.cfg, **self.latest)
    self.notifier.update(active)
    if active != self.last_alerts:
      self.last_alerts = active
      self.emit("alerts", active)

  # ---------------------------------------------------------------- background jobs
  def job(self, name, fn, then=None):
    """Run fn() on the pool unless a job with this name is already running."""
    if name in self.busy:
      return
    self.busy.add(name)

    def done(fut):
      self.busy.discard(name)
      try:
        result = fut.result()
      except Exception as exc:  # a failing collector must not kill the stream
        self.emit("error", {"job": name, "error": str(exc)[:300]})
        return
      if then:
        then(result)
    self.pool.submit(fn).add_done_callback(done)

  def refresh_drivers(self, force=False):
    def then(d):
      self.latest["drivers"] = d
      self.emit("drivers", d)
      self.publish_alerts()
    self.job("drivers", lambda: drivers.collect(force), then)

  def refresh_storage(self, full=False):
    def then_overview(d):
      self.latest["storage"] = d
      self.emit("storage.overview", d)
      self.publish_alerts()
    self.job("storage.overview", storage.overview, then_overview)
    if full:
      for name, fn in (("breakdown", storage.breakdown), ("cleanup", storage.cleanup),
                       ("steam", storage.steam), ("firmware", storage.firmware)):
        self.job("storage." + name, fn, lambda d, n=name: self.emit("storage." + n, d))

  def refresh_peripherals(self, slow=False):
    def then(d):
      self.latest["peripherals"] = d
      self.emit("peripherals", d)
      self.publish_alerts()
    self.job("peripherals", lambda: peripherals.collect(with_solaar=slow), then)

  # ---------------------------------------------------------------- commands
  def handle(self, cmd):
    kind = cmd.get("cmd")
    now = time.monotonic()
    if kind == "view":
      was_open, old_tab = self.opened, self.tab
      self.opened = bool(cmd.get("open"))
      self.tab = cmd.get("tab") or self.tab
      if self.opened and not was_open:
        self.emit("history", list(self.history))
      if self.opened and (not was_open or self.tab != old_tab):
        if self.tab == "storage":
          self.refresh_storage(full=True)
        elif self.tab == "devices":
          self.refresh_peripherals(slow=True)
        elif self.tab == "drivers" and self.latest["drivers"] is None:
          self.refresh_drivers()
    elif kind == "refresh":
      what = cmd.get("what")
      if what == "drivers":
        self.refresh_drivers(force=bool(cmd.get("force")))
      elif what == "storage":
        self.refresh_storage(full=True)
      elif what == "peripherals":
        self.refresh_peripherals(slow=True)
      self.cfg = config()
    elif kind == "dir":
      path = str(cmd.get("path") or "~")
      self.job("storage.dir", lambda: storage.directory(path), lambda d: self.emit("storage.dir", d))
    elif kind == "selftest":
      obj = str(cmd.get("obj") or "")
      if obj.startswith("/org/freedesktop/UDisks2/drives/"):
        self.job("selftest", lambda: storage.start_selftest(obj, "short"),
                 lambda d: self.emit("result", dict(d, action="selftest")))
    elif kind == "bt":
      action, mac, on = cmd.get("action"), cmd.get("mac"), cmd.get("on")

      def then(d):
        self.emit("result", dict(d, action="bt-" + str(action), mac=mac))
        self.refresh_peripherals()
      self.job("bt-" + str(mac), lambda: peripherals.bt_action(action, mac, on), then)
      self.due["peripherals"] = now + 3

  def read_stdin(self):
    for line in sys.stdin:
      try:
        self.commands.put(json.loads(line))
      except ValueError:
        pass
    os._exit(0)  # the shell went away

  # ---------------------------------------------------------------- main loop
  def run(self):
    threading.Thread(target=self.read_stdin, daemon=True).start()
    next_sample = time.monotonic()
    tick = 0
    while True:
      while True:
        try:
          self.handle(self.commands.get_nowait())
        except queue.Empty:
          break
      now = time.monotonic()
      if now >= next_sample:
        tick += 1
        live_view = self.opened and self.tab in LIVE_TABS
        snap = self.sensors.sample(gpu=live_view or tick % 5 == 0,
                                   gpu_procs=self.opened and self.tab == "gpu" and tick % 2 == 0)
        if "gpus" not in snap and self.latest["live"] and "gpus" in self.latest["live"]:
          snap["gpus"] = self.latest["live"]["gpus"]
        self.history.append({"t": round(snap["t"]), "tctl": snap["cpu"]["tctl"], "load": snap["cpu"]["load"],
                             "cooler": max([f["rpm"] for f in snap["fans"] if f["role"] == "cpu"] or [0]),
                             "gpu": (snap.get("gpus") or [{}])[0].get("util")})
        self.latest["live"] = snap
        if live_view or tick % 5 == 0:
          self.emit("live", snap)
          self.publish_alerts()
        next_sample += 1.0
        if next_sample < now:
          next_sample = now + 1.0
      for what, every in (("drivers", 1800), ("storage", 600),
                          ("peripherals", 3 if self.opened and self.tab == "devices" else 60)):
        if now >= self.due[what]:
          self.due[what] = now + every
          {"drivers": self.refresh_drivers, "storage": self.refresh_storage,
           "peripherals": self.refresh_peripherals}[what]()
      time.sleep(0.1)


def once(topic):
  if topic == "live":
    s = Sensors()
    time.sleep(0.5)
    data = s.sample(gpu=True, gpu_procs=True)
  elif topic == "drivers":
    data = drivers.collect()
  elif topic == "storage":
    data = storage.overview()
  elif topic == "peripherals":
    data = peripherals.collect()
  else:
    print(__doc__, file=sys.stderr)
    return 2
  print(json.dumps(data, indent=1))
  return 0


if __name__ == "__main__":
  try:
    if "--once" in sys.argv:
      args = [a for a in sys.argv[1:] if a != "--once"]
      sys.exit(once(args[0] if args else "live"))
    Daemon().run()
  except KeyboardInterrupt:
    pass
