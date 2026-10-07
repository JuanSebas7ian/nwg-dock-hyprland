"""Shared helpers: sysfs reads, subprocesses, JSON caches, config and notifications."""

import json
import os
import subprocess
import time
from pathlib import Path

# Tests point this at a fake tree containing sys/ and proc/.
ROOT = os.environ.get("HW_SYSROOT", "")
HOME = Path.home()
CACHE = Path(os.environ.get("XDG_CACHE_HOME") or HOME / ".cache") / "omarchy-hardware"
CONFIG_DIR = Path(os.environ.get("XDG_CONFIG_HOME") or HOME / ".config")

DEFAULTS = {
  "cpuWarn": 85,          # °C: icon turns red
  "cpuCrit": 90,          # °C: critical alert + notification
  "gpuWarn": 83,
  "pumpMinRpm": 500,      # below this the AIO pump counts as stopped
  "ramWarn": 0.90,        # fraction used
  "diskFreeWarn": 0.10,   # fraction free on /
  "batteryWarn": 15,      # % for Bluetooth / wireless devices
  "notify": True,
}


def sys_path(path):
  return ROOT + path if ROOT and path.startswith(("/sys/", "/proc/")) else path


def read(path, default=""):
  try:
    with open(sys_path(path)) as f:
      return f.read().strip()
  except OSError:
    return default


def num(path, scale=1.0):
  try:
    return float(read(path)) / scale
  except ValueError:
    return None


def listdir(path):
  try:
    return sorted(os.listdir(sys_path(path)))
  except OSError:
    return []


def run(cmd, timeout=60, **kw):
  """stdout of cmd, or "" if it fails, is missing or times out."""
  try:
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, **kw).stdout
  except (OSError, subprocess.SubprocessError):
    return ""


def config():
  cfg = dict(DEFAULTS)
  try:
    cfg.update(json.loads((CONFIG_DIR / "omarchy-hardware" / "config.json").read_text()))
  except (OSError, ValueError):
    pass
  return cfg


def write_json(path, data):
  path.parent.mkdir(parents=True, exist_ok=True)
  tmp = path.with_suffix(".tmp")
  tmp.write_text(json.dumps(data))
  tmp.replace(path)


def cached(name, max_age, producer, force=False):
  """producer() result cached in CACHE/<name>.json for max_age seconds.
  If a refresh fails (result has "error") the last good answer is returned
  with "stale" set, so an offline check never blanks the panel."""
  path = CACHE / f"{name}.json"
  if not force:
    try:
      if time.time() - path.stat().st_mtime < max_age:
        return json.loads(path.read_text())
    except (OSError, ValueError):
      pass
  data = producer()
  if isinstance(data, dict) and data.get("error") and path.exists():
    try:
      old = json.loads(path.read_text())
      old["stale"] = data["error"]
      return old
    except ValueError:
      pass
  write_json(path, data)
  return data


def notify(title, body, urgency="normal", app="Hardware"):
  if not config().get("notify", True):
    return
  run(["notify-send", "-u", urgency, "-a", app, title, body], timeout=10)
