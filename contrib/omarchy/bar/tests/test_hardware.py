"""Tests for the juansebas7ian.hardware backend against a fake /sys and /proc:
sensors (per-thread load, fan roles, temperatures), alerts and notification
de-duplication, update classification, BlueZ / solaar / input parsing,
bt-autopower.conf editing, and the daemon's stdin/stdout protocol.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_hardware
"""

import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

BAR = Path(__file__).resolve().parent.parent
BACKEND = BAR / "plugins" / "juansebas7ian.hardware" / "backend"
sys.path.insert(0, str(BACKEND))

from hw import alerts, drivers, peripherals, sensors, util  # noqa: E402


def write(root, rel, text):
  p = Path(root) / rel.lstrip("/")
  p.parent.mkdir(parents=True, exist_ok=True)
  p.write_text(text)


def fake_system(root, idle=(100, 100), fan_pump=5500):
  """Two threads, k10temp, an nct6799 with a pump and a CPU fan, one DDR5 module."""
  write(root, "/proc/stat", f"cpu  100 0 100 {sum(idle)} 0 0 0 0 0 0\ncpu0 50 0 50 {idle[0]} 0 0 0 0 0 0\n"
        f"cpu1 50 0 50 {idle[1]} 0 0 0 0 0 0\nintr 1\n")
  write(root, "/proc/cpuinfo", "model name\t: AMD Ryzen 5 9600X 6-Core Processor\n")
  write(root, "/proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\nCached: 100 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
  write(root, "/proc/diskstats", "259 0 nvme0n1 1 0 10 0 1 0 20 0 0 0 0\n")
  write(root, "/proc/net/dev", "h1\nh2\n  lo: 5 0 0 0 0 0 0 0 5 0\neth0: 1000 0 0 0 0 0 0 0 2000 0\n")
  write(root, "/sys/class/hwmon/hwmon0/name", "k10temp")
  write(root, "/sys/class/hwmon/hwmon0/temp1_label", "Tctl")
  write(root, "/sys/class/hwmon/hwmon0/temp1_input", "61250")
  write(root, "/sys/class/hwmon/hwmon0/temp3_label", "Tccd1")
  write(root, "/sys/class/hwmon/hwmon0/temp3_input", "50000")
  write(root, "/sys/class/hwmon/hwmon1/name", "nct6799")
  write(root, "/sys/class/hwmon/hwmon1/fan2_input", "900")
  write(root, "/sys/class/hwmon/hwmon1/fan3_input", "0")
  write(root, "/sys/class/hwmon/hwmon1/fan7_input", str(fan_pump))
  write(root, "/sys/class/hwmon/hwmon1/temp1_label", "SYSTIN")
  write(root, "/sys/class/hwmon/hwmon1/temp1_input", "31000")
  write(root, "/sys/class/hwmon/hwmon2/name", "spd5118")
  write(root, "/sys/class/hwmon/hwmon2/temp1_input", "40500")
  write(root, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq", "5200000")
  write(root, "/sys/devices/system/cpu/cpu1/cpufreq/scaling_cur_freq", "600000")
  write(root, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor", "performance")


CFG = dict(util.DEFAULTS)


class Base(unittest.TestCase):
  def setUp(self):
    self.tmp = tempfile.TemporaryDirectory()
    self.root = Path(self.tmp.name)
    self.patches = [mock.patch.object(util, "ROOT", str(self.root / "fs")),
                    mock.patch.object(util, "CONFIG_DIR", self.root / "config"),
                    mock.patch.object(sensors, "CONFIG_DIR", self.root / "config"),
                    mock.patch.object(peripherals, "BT_CONF", self.root / "config" / "bt-autopower.conf")]
    for p in self.patches:
      p.start()
    fake_system(self.root / "fs")
    write(self.root / "config", "omarchy-sysmon/fans.json",
          json.dumps({"7": "AIO pump", "2": "CPU fan", "_roles": {"pump": ["7"], "cpu": ["2"]}}))

  def tearDown(self):
    for p in self.patches:
      p.stop()
    self.tmp.cleanup()


class Sensors(Base):
  def test_per_thread_load_temperatures_and_fan_roles(self):
    s = sensors.Sensors(use_nvml=False)
    fake_system(self.root / "fs", idle=(150, 100))  # cpu0 idle +50 of +50, cpu1 busy
    write(self.root / "fs", "/proc/stat", "cpu  100 0 150 250 0 0 0 0 0 0\ncpu0 50 0 50 150 0 0 0 0 0 0\n"
          "cpu1 50 0 100 100 0 0 0 0 0 0\n")
    snap = s.sample(gpu=False)
    self.assertEqual(snap["cpu"]["threads"], [0.0, 100.0])
    self.assertEqual(snap["cpu"]["load"], 50.0)
    self.assertEqual(snap["cpu"]["tctl"], 61.25)
    self.assertEqual(snap["cpu"]["ccd"], [50.0])
    self.assertEqual(snap["cpu"]["freqs"], [5200, 600])
    fans = {f["id"]: f for f in snap["fans"]}
    self.assertEqual(fans["7"]["role"], "pump")
    self.assertEqual(fans["2"]["role"], "cpu")
    self.assertNotIn("3", fans)  # a stopped case fan is hidden
    self.assertEqual(snap["board"], [{"name": "Motherboard", "temp": 31.0}])
    self.assertEqual(snap["ram"], [40.5])
    self.assertEqual(snap["mem"]["used"], 600 * 1024)
    self.assertNotIn("gpus", snap)

  def test_stopped_pump_stays_listed(self):
    fake_system(self.root / "fs", fan_pump=0)
    snap = sensors.Sensors(use_nvml=False).sample(gpu=False)
    self.assertEqual([f["rpm"] for f in snap["fans"] if f["role"] == "pump"], [0])


class Alerts(unittest.TestCase):
  def live(self, tctl=50, pump=5500, used=0.5, gpu=40):
    return {"cpu": {"tctl": tctl}, "fans": [{"id": "7", "name": "AIO pump", "rpm": pump, "role": "pump"}],
            "mem": {"total": 100, "used": used * 100}, "gpus": [{"index": 0, "temp": gpu}]}

  def ids(self, **kw):
    return [a["id"] for a in alerts.evaluate(CFG, **kw)]

  def test_quiet_when_everything_is_fine(self):
    self.assertEqual(self.ids(live=self.live()), [])

  def test_thresholds(self):
    self.assertEqual(self.ids(live=self.live(tctl=86)), ["cpu-warm"])
    self.assertEqual(self.ids(live=self.live(tctl=91)), ["cpu-hot"])
    self.assertEqual(self.ids(live=self.live(pump=120)), ["pump"])
    self.assertEqual(self.ids(live=self.live(used=0.95)), ["ram"])
    self.assertEqual(self.ids(live=self.live(gpu=85)), ["gpu-hot-0"])

  def test_critical_sorted_first_and_tabs(self):
    out = alerts.evaluate(CFG, live=self.live(tctl=86, pump=0))
    self.assertEqual([(a["id"], a["level"], a["tab"]) for a in out], [("pump", "crit", "cpu"), ("cpu-warm", "warn", "cpu")])

  def test_storage_drivers_and_devices(self):
    storage = {"root": {"size": 100, "free": 5}, "disks": [{"name": "nvme0n1", "model": "X", "health": {"mediaErrors": 2}}]}
    drv = {"nvidia": {"issues": ["DKMS module not built for kernel 7.3"]}, "cuda": {"works": True},
           "health": {"issues": [{"id": "CF07", "level": "fail", "title": "t", "details": []}]}}
    per = {"bluetooth": {"adapter": {}, "devices": [{"mac": "AA", "name": "JBL", "connected": True, "battery": 9}]},
           "batteries": [{"name": "M575S", "percent": 50}]}
    ids = self.ids(storage=storage, drivers=drv, peripherals=per)
    self.assertIn("disk-free", ids)
    self.assertIn("smart-nvme0n1", ids)
    self.assertIn("hwcheck-CF07", ids)
    self.assertIn("battery-AA", ids)
    self.assertTrue(any(i.startswith("nvidia-") for i in ids))

  def test_notifier_sends_once_and_rearms(self):
    n = alerts.Notifier(rearm=60)
    crit = alerts.evaluate(CFG, live=self.live(pump=0))
    with mock.patch.object(alerts, "notify") as notify:
      self.assertEqual(len(n.update(crit, now=1000)), 1)
      self.assertEqual(n.update(crit, now=1010), [])          # still active: no repeat
      n.update([], now=1100)                                  # clear for > rearm
      self.assertEqual(len(n.update(crit, now=1101)), 1)      # back again: notify again
      self.assertEqual(notify.call_count, 2)
      warn = alerts.evaluate(CFG, live=self.live(tctl=86))
      self.assertEqual(n.update(warn, now=1102), [])          # warnings never notify


class Drivers(unittest.TestCase):
  def test_checkupdates_is_split_once(self):
    text = "linux 7.2.3.arch1-3 -> 7.2.4.arch1-1\nnvidia-utils 610.57.04-1 -> 610.60.01-1\nfirefox 140-1 -> 141-1\ncuda 13.3.1-1 -> 13.3.2-1\n"
    split = drivers.split_updates({"packages": drivers.parse_checkupdates(text), "checkedAt": 1})
    self.assertEqual(split["total"], 4)
    self.assertEqual([p["name"] for p in split["drivers"]], ["linux", "nvidia-utils"])
    self.assertEqual([p["name"] for p in split["gpu"]], ["linux", "nvidia-utils", "cuda"])

  def test_bios_newer(self):
    self.assertTrue(drivers.bios_newer("1804", "1402"))
    self.assertFalse(drivers.bios_newer("1402", "1402"))
    self.assertFalse(drivers.bios_newer("", "1402"))


class Peripherals(Base):
  def test_bluez_objects(self):
    v = lambda x: {"type": "x", "data": x}  # noqa: E731
    objects = {
      "/org/bluez/hci0": {"org.bluez.Adapter1": {"Address": v("58:02"), "Alias": v("pc"), "Powered": v(True),
                                                  "Discoverable": v(False), "Pairable": v(True)}},
      "/org/bluez/hci0/dev_A": {"org.bluez.Device1": {"Address": v("AA"), "Alias": v("JBL Grip"), "Paired": v(True),
                                                       "Trusted": v(True), "Connected": v(False), "Icon": v("audio-card")}},
      "/org/bluez/hci0/dev_B": {"org.bluez.Device1": {"Address": v("BB"), "Alias": v("Keys"), "Paired": v(True),
                                                       "Trusted": v(True), "Connected": v(True)},
                                "org.bluez.Battery1": {"Percentage": v(42)}},
      "/org/bluez/hci0/dev_C": {"org.bluez.Device1": {"Address": v("CC"), "Alias": v("Stranger"), "Paired": v(False),
                                                       "Connected": v(False)}},
    }
    adapter, devices = peripherals.parse_bluez(objects)
    self.assertTrue(adapter["powered"])
    self.assertFalse(adapter["discoverable"])
    self.assertEqual([d["name"] for d in devices], ["Keys", "JBL Grip"])  # connected first, scan results hidden
    self.assertEqual(devices[0]["battery"], 42)
    self.assertIsNone(devices[1]["battery"])

  def test_solaar_output(self):
    text = ("Bolt Receiver\n  Device path  : /dev/hidraw6\n\n  1: ERGO M575S Trackball\n     Codename : x\n"
            "         8: UNIFIED BATTERY        {1004} V2\n            Battery: 80%, BatteryStatus.DISCHARGING.\n"
            "  2: ERGO M575S\n     Codename : y\n")
    self.assertEqual(peripherals.parse_solaar(text), [
      {"name": "ERGO M575S Trackball", "source": "solaar", "percent": 80, "status": "discharging"},
      {"name": "ERGO M575S", "source": "solaar", "percent": None, "status": ""}])

  def test_input_devices_merged_per_device(self):
    text = ('N: Name="Logitech USB Receiver"\nP: Phys=usb-0:9/input0\nH: Handlers=sysrq kbd event5\nB: EV=120013\n\n'
            'N: Name="Logitech USB Receiver Mouse"\nP: Phys=usb-0:9/input1\nH: Handlers=mouse0 event6\nB: EV=17\n\n'
            'N: Name="USB Keyboard"\nP: Phys=usb-0:2.3/input0\nH: Handlers=sysrq kbd event3\nB: EV=120013\n\n'
            'N: Name="Microsoft X-Box 360 pad"\nP: Phys=usb-0:1/input0\nH: Handlers=event22 js0\nB: EV=20000b\n\n'
            'N: Name="Microsoft X-Box 360 pad"\nP: Phys=pad-keepalive/input0\nS: Sysfs=/devices/virtual/input/input99\n'
            'H: Handlers=event23 js1\nB: EV=20000b\n\n'
            'N: Name="Power Button"\nP: Phys=LNXPWRBN/button/input0\nH: Handlers=kbd event1\nB: EV=3\n')
    rows = [(d["name"], d["kinds"], d["virtual"]) for d in peripherals.parse_input_devices(text)]
    self.assertEqual(rows, [("Logitech USB Receiver", ["pointer", "keyboard"], False),
                            ("USB Keyboard", ["keyboard"], False),
                            ("Microsoft X-Box 360 pad", ["gamepad"], False),
                            ("Microsoft X-Box 360 pad", ["gamepad"], True)])

  def test_bt_conf_edit_keeps_other_lines(self):
    conf = peripherals.BT_CONF
    conf.parent.mkdir(parents=True, exist_ok=True)
    conf.write_text("# mine\nINTERVAL=5\nDISCOVERABLE=yes\n")
    peripherals.write_bt_conf({"DISCOVERABLE": "no", "AUTOCONNECT_EXCLUDE": "AA:BB:CC:DD:EE:FF 11:22:33:44:55:66"})
    text = conf.read_text()
    self.assertIn("# mine\nINTERVAL=5\nDISCOVERABLE=no\n", text)
    c = peripherals.read_bt_conf()
    self.assertEqual(c["AUTOCONNECT_EXCLUDE"].split(), ["AA:BB:CC:DD:EE:FF", "11:22:33:44:55:66"])
    self.assertEqual(c["RECONNECT_AFTER"], peripherals.DEFAULT_RECONNECT)
    # bt-autopower sources this file with bash: it must stay valid shell
    out = subprocess.run(["bash", "-c", f'. "{conf}"; echo "$DISCOVERABLE|$AUTOCONNECT_EXCLUDE"'], capture_output=True, text=True)
    self.assertEqual(out.stdout.strip(), "no|AA:BB:CC:DD:EE:FF 11:22:33:44:55:66")

  def test_autoconnect_toggle(self):
    mac = "AA:BB:CC:DD:EE:FF"
    with mock.patch.object(peripherals, "run") as run:
      self.assertTrue(peripherals.bt_action("autoconnect", mac, False)["ok"])
      self.assertEqual(peripherals.read_bt_conf()["AUTOCONNECT_EXCLUDE"], mac)
      self.assertTrue(peripherals.bt_action("autoconnect", mac, True)["ok"])
      self.assertEqual(peripherals.read_bt_conf()["AUTOCONNECT_EXCLUDE"], "")
      run.assert_called_with(["bluetoothctl", "trust", mac], timeout=10)

  def test_rejects_bad_addresses(self):
    with mock.patch.object(peripherals, "run") as run, mock.patch("subprocess.run") as sp:
      self.assertFalse(peripherals.bt_action("connect", "AA; rm -rf ~")["ok"])
      run.assert_not_called()
      sp.assert_not_called()


class Protocol(Base):
  def test_view_command_streams_history_and_live(self):
    env = dict(os.environ, HW_SYSROOT=str(self.root / "fs"), HW_NO_NVML="1", XDG_CONFIG_HOME=str(self.root / "config"),
               XDG_CACHE_HOME=str(self.root / "cache"), PATH="/nonexistent")  # no busctl, checkupdates, … : collectors fail soft
    script = '{"cmd":"view","open":true,"tab":"cpu"}\n'
    p = subprocess.Popen([sys.executable, "-B", str(BACKEND / "hardwared.py")], stdin=subprocess.PIPE,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env)
    # stdin stays open like the shell keeps it; closing it makes the daemon exit.
    p.stdin.write(script)
    p.stdin.flush()
    time.sleep(2.5)
    out, err = p.communicate(timeout=5)
    types = [json.loads(line)["type"] for line in out.splitlines()]
    self.assertIn("history", types, err)
    self.assertIn("live", types, err)
    live = next(json.loads(line)["data"] for line in out.splitlines() if json.loads(line)["type"] == "live")
    self.assertEqual(live["cpu"]["tctl"], 61.25)


class SharedUi(unittest.TestCase):
  def test_plugin_copies_match_shared_ui(self):
    shared = BAR / "shared" / "ui"
    for marker in BAR.glob("plugins/*/.uses-shared-ui"):
      copy = marker.parent / "ui"
      names = sorted(p.name for p in shared.iterdir())
      self.assertEqual(sorted(p.name for p in copy.iterdir()), names, f"{copy}: run sync-ui.sh")
      for n in names:
        self.assertEqual((copy / n).read_text(), (shared / n).read_text(), f"{copy / n}: run sync-ui.sh")


if __name__ == "__main__":
  unittest.main()
