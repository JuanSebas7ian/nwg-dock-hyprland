"""Peripherals: Bluetooth adapter and devices (BlueZ over D-Bus, with battery),
reconnect-at-boot settings shared with bt-autopower, wireless batteries
(power_supply and Logitech receivers through solaar), input devices and game
controllers (pad-keepalive), the USB tree with recent kernel errors per port,
cameras, and the default audio output and input.

Actions (connect, disconnect, auto-connect, visibility) go through
bluetoothctl and ~/.config/bt-autopower.conf. Read-only otherwise, no root."""

import json
import re
import shlex
import subprocess
import time

from .util import CACHE, CONFIG_DIR, cached, listdir, read, run

BT_CONF = CONFIG_DIR / "bt-autopower.conf"
DEFAULT_RECONNECT = "0 20 60 180 600"


# ------------------------------------------------------------------ Bluetooth

def _v(props, key, default=None):
  item = props.get(key)
  return item.get("data", default) if isinstance(item, dict) else default


def parse_bluez(objects):
  """GetManagedObjects (busctl --json) → adapter + devices."""
  adapter, devices = None, []
  for path, ifaces in sorted(objects.items()):
    if "org.bluez.Adapter1" in ifaces:
      a = ifaces["org.bluez.Adapter1"]
      adapter = adapter or {"path": path, "address": _v(a, "Address", ""), "name": _v(a, "Alias", ""),
                            "powered": bool(_v(a, "Powered", False)), "discoverable": bool(_v(a, "Discoverable", False)),
                            "pairable": bool(_v(a, "Pairable", False)), "discovering": bool(_v(a, "Discovering", False))}
    if "org.bluez.Device1" in ifaces:
      d = ifaces["org.bluez.Device1"]
      if not _v(d, "Paired", False) and not _v(d, "Connected", False):
        continue  # only devices this PC knows, not every scan result
      battery = ifaces.get("org.bluez.Battery1")
      devices.append({
        "mac": _v(d, "Address", ""), "name": _v(d, "Alias", "") or _v(d, "Name", ""),
        "icon": _v(d, "Icon", ""), "paired": bool(_v(d, "Paired", False)), "trusted": bool(_v(d, "Trusted", False)),
        "connected": bool(_v(d, "Connected", False)), "blocked": bool(_v(d, "Blocked", False)),
        "battery": _v(battery, "Percentage") if battery else None,
      })
  devices.sort(key=lambda d: (not d["connected"], d["name"].lower()))
  return adapter, devices


def read_bt_conf(path=None):
  path = path or BT_CONF
  conf = {"DISCOVERABLE": "yes", "PAIRABLE": "yes", "AUTOCONNECT": "yes", "AUTOCONNECT_EXCLUDE": "",
          "RECONNECT_AFTER": DEFAULT_RECONNECT}
  try:
    for line in path.read_text().splitlines():
      m = re.match(r"\s*([A-Z_]+)=(.*)$", line)
      if m:
        value = shlex.split(m.group(2))
        conf[m.group(1)] = value[0] if value else ""
  except (OSError, ValueError):
    pass
  return conf


def write_bt_conf(updates, path=None):
  """Set KEY=value lines in bt-autopower.conf, keeping comments and other keys."""
  path = path or BT_CONF
  try:
    lines = path.read_text().splitlines()
  except OSError:
    lines = ["# bt-autopower settings (also edited from the Hardware widget, Devices tab)"]
  seen = set()
  for i, line in enumerate(lines):
    m = re.match(r"\s*([A-Z_]+)=", line)
    if m and m.group(1) in updates:
      lines[i] = f"{m.group(1)}={shlex.quote(updates[m.group(1)])}"
      seen.add(m.group(1))
  for key, value in updates.items():
    if key not in seen:
      lines.append(f"{key}={shlex.quote(value)}")
  path.parent.mkdir(parents=True, exist_ok=True)
  tmp = path.with_suffix(".tmp")
  tmp.write_text("\n".join(lines) + "\n")
  tmp.replace(path)


def bluetooth():
  raw = run(["busctl", "--system", "--json=short", "call", "org.bluez", "/",
             "org.freedesktop.DBus.ObjectManager", "GetManagedObjects"], timeout=5)
  try:
    objects = json.loads(raw)["data"][0]
  except (ValueError, KeyError, IndexError):
    objects = {}
  adapter, devices = parse_bluez(objects)
  conf = read_bt_conf()
  excluded = set(conf["AUTOCONNECT_EXCLUDE"].split())
  for d in devices:
    d["autoconnect"] = conf["AUTOCONNECT"] == "yes" and d["trusted"] and d["mac"] not in excluded
  rf = run(["rfkill", "list", "bluetooth"], timeout=5)
  return {
    "adapter": adapter, "devices": devices, "blocked": "Soft blocked: yes" in rf,
    "service": run(["systemctl", "--user", "is-active", "bt-autopower.service"], timeout=5).strip(),
    "settings": {"discoverable": conf["DISCOVERABLE"] == "yes", "pairable": conf["PAIRABLE"] == "yes",
                 "autoconnect": conf["AUTOCONNECT"] == "yes", "reconnectAfter": conf["RECONNECT_AFTER"]},
  }


def bt_action(action, mac=None, on=None):
  """connect | disconnect | autoconnect (per device) | visible (adapter)."""
  if mac is not None and not re.fullmatch(r"[0-9A-F]{2}(:[0-9A-F]{2}){5}", mac):
    return {"ok": False, "error": "bad address"}
  if action in ("connect", "disconnect"):
    try:
      p = subprocess.run(["bluetoothctl", action, mac], capture_output=True, text=True, timeout=20)
    except subprocess.TimeoutExpired:
      return {"ok": False, "error": "timed out (is the device on and in range?)"}
    out = p.stdout + p.stderr
    ok = "successful" in out
    err = re.search(r"Failed to \w+: (\S+)", out)
    return {"ok": ok, "error": "" if ok else (err.group(1) if err else out.strip()[-120:])}
  if action == "autoconnect":
    conf = read_bt_conf()
    excluded = [m for m in conf["AUTOCONNECT_EXCLUDE"].split() if m != mac]
    if on:
      run(["bluetoothctl", "trust", mac], timeout=10)  # bt-autopower only reconnects trusted devices
    else:
      excluded.append(mac)
    write_bt_conf({"AUTOCONNECT_EXCLUDE": " ".join(excluded)})
    return {"ok": True}
  if action == "visible":
    value = "yes" if on else "no"
    write_bt_conf({"DISCOVERABLE": value, "PAIRABLE": value})
    if on:
      run(["bluetoothctl", "discoverable-timeout", "0"], timeout=10)
    run(["bluetoothctl", "discoverable", "on" if on else "off"], timeout=10)
    run(["bluetoothctl", "pairable", "on" if on else "off"], timeout=10)
    return {"ok": True}
  return {"ok": False, "error": f"unknown action {action}"}


# ------------------------------------------------------------------ batteries

def power_supplies():
  out = []
  for name in listdir("/sys/class/power_supply"):
    base = f"/sys/class/power_supply/{name}"
    if read(f"{base}/scope") != "Device":
      continue  # the PC's own supplies, not peripherals
    cap = read(f"{base}/capacity")
    out.append({"name": read(f"{base}/model_name") or name, "source": "kernel",
                "percent": int(cap) if cap.isdigit() else None, "level": read(f"{base}/capacity_level"),
                "status": read(f"{base}/status")})
  return out


def parse_solaar(text):
  """Devices and batteries from `solaar show` (Logitech Unifying/Bolt receivers)."""
  out, current = [], None
  for line in text.splitlines():
    m = re.match(r"^\s{2}\d+: (.+?)\s*$", line)
    if m:
      current = {"name": m.group(1), "source": "solaar", "percent": None, "status": ""}
      out.append(current)
      continue
    m = re.search(r"Battery: (\d+)%(?:, (?:BatteryStatus\.)?(\w+))?", line)
    if m and current and current["percent"] is None:
      current["percent"] = int(m.group(1))
      current["status"] = (m.group(2) or "").lower()
  return out


def solaar(refresh=True):
  """Logitech devices from solaar (6 s): refreshed at most every 5 min; with
  refresh=False only the last answer is returned, never a new run."""
  if not refresh:
    try:
      return json.loads((CACHE / "solaar.json").read_text())
    except (OSError, ValueError):
      return {"devices": []}

  def produce():
    text = run(["solaar", "show"], timeout=30)
    return {"devices": parse_solaar(text), "checkedAt": int(time.time())} if text else {"devices": [], "error": "solaar not available"}
  return cached("solaar", 300, produce)


# ------------------------------------------------------------------ input, USB, video, audio

def parse_input_devices(text):
  """Keyboards, pointers and game controllers from /proc/bus/input/devices."""
  out, dev = [], {}
  for line in text.splitlines() + [""]:
    if not line.strip():
      if dev.get("name"):
        h = dev.get("handlers", "")
        kind = "gamepad" if re.search(r"\bjs\d", h) else "pointer" if "mouse" in h else "keyboard" if "kbd" in h and dev.get("ev") == "120013" else ""
        if kind:
          out.append({"name": dev["name"], "kind": kind, "phys": dev.get("phys", ""),
                      "virtual": dev.get("phys", "").startswith("pad-keepalive") or "/virtual/" in dev.get("sysfs", "")})
      dev = {}
      continue
    tag, _, value = line.partition(": ")
    if tag == "N":
      dev["name"] = value.split("=", 1)[1].strip('"')
    elif tag == "P":
      dev["phys"] = value.split("=", 1)[1]
    elif tag == "S":
      dev["sysfs"] = value.split("=", 1)[1]
    elif tag == "H":
      dev["handlers"] = value.split("=", 1)[1]
    elif tag == "B" and value.startswith("EV="):
      dev["ev"] = value[3:]
  # One row per physical device: a receiver or keyboard exposes several input
  # nodes ("... Mouse", "... Consumer Control"); merge them and list the kinds.
  rank = {"gamepad": 0, "pointer": 1, "keyboard": 2}
  merged = {}
  for d in out:
    base = re.sub(r" (Mouse|Consumer Control|System Control)$", "", d["name"])
    key = (base, d["virtual"])
    if key in merged:
      if d["kind"] not in merged[key]["kinds"]:
        merged[key]["kinds"].append(d["kind"])
    else:
      merged[key] = dict(d, name=base, kinds=[d["kind"]])
  for d in merged.values():
    d["kinds"].sort(key=rank.get)
    d["kind"] = d["kinds"][0]
  return list(merged.values())


USB_ERR = re.compile(r"usb (\d+-[\d.]+):.*?(error -?\d+|failed.*|disabled by hub.*|device not accepting address.*|"
                     r"unable to enumerate.*|reset .* USB device.*|USB disconnect.*)", re.I)


def usb_errors():
  """{port: {"count", "last", "at"}} from this boot's kernel log."""
  out = {}
  for line in run(["journalctl", "-k", "-b", "--no-pager", "-o", "short-unix", "-g", r"usb [0-9]+-[0-9.]+:"], timeout=10).splitlines():
    m = USB_ERR.search(line)
    if not m:
      continue
    port, msg = m.group(1), m.group(2)
    if msg.lower().startswith(("usb disconnect", "reset")):
      continue
    e = out.setdefault(port, {"count": 0, "last": "", "at": 0})
    e["count"] += 1
    e["last"] = msg[:90]
    try:
      e["at"] = int(float(line.split()[0]))
    except ValueError:
      pass
  return out


SPEEDS = {"1.5": "USB 1 low", "12": "USB 1", "480": "USB 2", "5000": "USB 3", "10000": "USB 3.1", "20000": "USB 3.2"}


def usb_tree():
  errors = usb_errors()
  out = []
  for name in listdir("/sys/bus/usb/devices"):
    if ":" in name or name.startswith("usb"):
      continue
    base = f"/sys/bus/usb/devices/{name}"
    cls = read(f"{base}/bDeviceClass")
    drivers = sorted({read(f"/sys/bus/usb/devices/{i}/uevent").partition("DRIVER=")[2].split("\n")[0]
                      for i in listdir("/sys/bus/usb/devices") if i.startswith(name + ":")} - {""})
    out.append({"port": name, "id": f"{read(f'{base}/idVendor')}:{read(f'{base}/idProduct')}",
                "name": read(f"{base}/product") or "Unknown device", "vendor": read(f"{base}/manufacturer"),
                "speed": SPEEDS.get(read(f"{base}/speed"), read(f"{base}/speed")), "hub": cls == "09",
                "depth": name.count(".") + 1, "drivers": drivers, "errors": errors.get(name)})
  out.sort(key=lambda d: [int(x) for x in re.split(r"[-.]", d["port"])])
  return out


def cameras():
  names = []
  for v in listdir("/sys/class/video4linux"):
    n = read(f"/sys/class/video4linux/{v}/name").split(":")[0]
    if n and n not in names:
      names.append(n)
  return names


def audio_node(which):
  for line in run(["wpctl", "inspect", f"@DEFAULT_AUDIO_{which}@"], timeout=3).splitlines():
    m = re.search(r'node\.description = "(.*)"', line)
    if m:
      return m.group(1)
  return ""


def disabled_audio():
  """Audio devices switched off by a WirePlumber rule (e.g. the NexiGo webcam mic)."""
  out = []
  d = CONFIG_DIR / "wireplumber" / "wireplumber.conf.d"
  try:
    for f in sorted(d.glob("*.conf")):
      text = f.read_text(errors="replace")
      if "device.disabled" in text and "true" in text:
        out.append(f.name)
  except OSError:
    pass
  return out


def collect(with_solaar=True):
  batteries = power_supplies() + solaar(refresh=with_solaar).get("devices", [])
  return {
    "bluetooth": bluetooth(),
    "batteries": batteries,
    "input": parse_input_devices(read("/proc/bus/input/devices")),
    "padKeepalive": run(["systemctl", "--user", "is-active", "pad-keepalive.service"], timeout=5).strip(),
    "usb": usb_tree(),
    "cameras": cameras(),
    "audio": {"output": audio_node("SINK"), "input": audio_node("SOURCE"), "disabled": disabled_audio()},
    "checkedAt": int(time.time()),
  }
