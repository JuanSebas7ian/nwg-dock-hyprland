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

    def test_power_failure_is_not_fatal(self):
        open(os.path.join(self.state, "power_fails"), "w").close()
        r = self.run_once()
        self.assertEqual(r.returncode, 0)
        self.assertIn("retrying", r.stdout)


if __name__ == "__main__":
    unittest.main()
