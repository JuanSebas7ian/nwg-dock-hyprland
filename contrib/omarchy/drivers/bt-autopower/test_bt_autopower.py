"""bt-autopower against fake bluetoothctl/rfkill (no hardware touched)."""
import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))

FAKE_BTCTL = r"""#!/bin/bash
S=$FAKE_STATE
echo "$*" >> "$S/calls"
case "$1" in
  list) [ -e "$S/present" ] && echo "Controller 58:02:05:BC:99:6B host [default]" ;;
  show) echo "Controller 58:02:05:BC:99:6B (public)"
        echo "	Powered: $(cat "$S/powered")"
        echo "	Discoverable: $(cat "$S/disc")"
        echo "	Pairable: $(cat "$S/pair")" ;;
  power) [ -e "$S/power_fails" ] && exit 1; echo yes > "$S/powered" ;;
  discoverable) echo yes > "$S/disc" ;;
  pairable) echo yes > "$S/pair" ;;
  devices) [ -e "$S/dev_$2" ] && cat "$S/dev_$2" ;;
  info) echo "Device $2 (public)"; echo "	Alias: Speaker $2" ;;
  connect) if grep -qx "$2" "$S/reachable" 2>/dev/null; then echo "Connection successful"; else echo "Failed to connect"; exit 1; fi ;;
esac
"""
FAKE_RFKILL = r"""#!/bin/bash
echo "0: hci0: Bluetooth"
echo "	Soft blocked: $(cat "$FAKE_STATE/blocked")"
echo "	Hard blocked: no"
"""


class BtAutopower(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        d = self.tmp.name
        self.state = os.path.join(d, "state")
        os.mkdir(self.state)
        bindir = os.path.join(d, "bin")
        os.mkdir(bindir)
        for name, body in (("bluetoothctl", FAKE_BTCTL), ("rfkill", FAKE_RFKILL)):
            p = os.path.join(bindir, name)
            with open(p, "w") as f:
                f.write(body)
            os.chmod(p, 0o755)
        self.set(present=True, powered="no", disc="no", pair="no", blocked="no")
        self.env = dict(os.environ, PATH=bindir + ":/usr/bin:/bin", FAKE_STATE=self.state,
                        BT_AUTOPOWER_ONCE="1", BT_AUTOPOWER_CONF=os.path.join(d, "none.conf"))

    def tearDown(self):
        self.tmp.cleanup()

    def set(self, present=None, **kv):
        if present is True:
            open(os.path.join(self.state, "present"), "w").close()
        elif present is False and os.path.exists(os.path.join(self.state, "present")):
            os.remove(os.path.join(self.state, "present"))
        for k, v in kv.items():
            with open(os.path.join(self.state, k), "w") as f:
                f.write(v + "\n")

    def get(self, k):
        with open(os.path.join(self.state, k)) as f:
            return f.read().strip()

    def calls(self):
        p = os.path.join(self.state, "calls")
        return open(p).read().split("\n") if os.path.exists(p) else []

    def run_once(self):
        return subprocess.run([os.path.join(HERE, "bt-autopower")], env=self.env,
                              capture_output=True, text=True, timeout=30)

    def test_powers_on_unpowered_adapter(self):
        r = self.run_once()
        self.assertEqual(self.get("powered"), "yes")
        self.assertIn("adapter powered on", r.stdout)

    def test_makes_powered_adapter_discoverable_and_pairable(self):
        self.set(powered="yes")
        self.run_once()
        self.assertEqual(self.get("disc"), "yes")
        self.assertEqual(self.get("pair"), "yes")
        self.assertIn("discoverable-timeout 0", self.calls())

    def test_respects_rfkill_soft_block(self):
        self.set(blocked="yes")
        self.run_once()
        self.assertEqual(self.get("powered"), "no")
        self.assertNotIn("power on", self.calls())

    def test_no_adapter_does_nothing(self):
        self.set(present=False)
        self.run_once()
        self.assertEqual(self.calls(), ["list", ""])

    def test_discoverable_can_be_disabled(self):
        self.set(powered="yes")
        conf = os.path.join(self.tmp.name, "c.conf")
        with open(conf, "w") as f:
            f.write("DISCOVERABLE=no\n")
        self.env["BT_AUTOPOWER_CONF"] = conf
        self.run_once()
        self.assertEqual(self.get("disc"), "no")
        self.assertEqual(self.get("pair"), "yes")

    def devices(self, kind, *macs):
        self.set(**{"dev_" + kind: "\n".join("Device %s X" % m for m in macs)})

    def test_reconnects_paired_trusted_devices(self):
        self.set(powered="yes", reachable="AA:00:00:00:00:01")
        self.devices("Paired", "AA:00:00:00:00:01", "AA:00:00:00:00:02", "AA:00:00:00:00:03")
        self.devices("Trusted", "AA:00:00:00:00:01", "AA:00:00:00:00:03")
        self.devices("Connected", "AA:00:00:00:00:03")
        r = self.run_once()
        calls = self.calls()
        self.assertIn("connect AA:00:00:00:00:01", calls)
        self.assertNotIn("connect AA:00:00:00:00:02", calls)  # not trusted
        self.assertNotIn("connect AA:00:00:00:00:03", calls)  # already connected
        self.assertIn("connected Speaker AA:00:00:00:00:01", r.stdout)

    def test_unreachable_device_is_not_fatal(self):
        self.set(powered="yes")
        self.devices("Paired", "AA:00:00:00:00:01")
        self.devices("Trusted", "AA:00:00:00:00:01")
        r = self.run_once()
        self.assertEqual(r.returncode, 0)
        self.assertIn("connect AA:00:00:00:00:01", self.calls())
        self.assertNotIn("connected", r.stdout)

    def test_autoconnect_can_be_disabled_or_excluded(self):
        self.set(powered="yes")
        self.devices("Paired", "AA:00:00:00:00:01", "AA:00:00:00:00:02")
        self.devices("Trusted", "AA:00:00:00:00:01", "AA:00:00:00:00:02")
        conf = os.path.join(self.tmp.name, "c.conf")
        self.env["BT_AUTOPOWER_CONF"] = conf
        with open(conf, "w") as f:
            f.write('AUTOCONNECT_EXCLUDE="AA:00:00:00:00:02"\n')
        self.run_once()
        self.assertIn("connect AA:00:00:00:00:01", self.calls())
        self.assertNotIn("connect AA:00:00:00:00:02", self.calls())
        os.remove(os.path.join(self.state, "calls"))
        with open(conf, "w") as f:
            f.write("AUTOCONNECT=no\n")
        self.run_once()
        self.assertFalse([c for c in self.calls() if c.startswith("connect")])

    def test_no_reconnect_while_blocked(self):
        self.set(blocked="yes")
        self.devices("Paired", "AA:00:00:00:00:01")
        self.devices("Trusted", "AA:00:00:00:00:01")
        self.run_once()
        self.assertFalse([c for c in self.calls() if c.startswith("connect")])

    def test_power_failure_is_not_fatal(self):
        open(os.path.join(self.state, "power_fails"), "w").close()
        r = self.run_once()
        self.assertEqual(r.returncode, 0)
        self.assertIn("retrying", r.stdout)


if __name__ == "__main__":
    unittest.main()
