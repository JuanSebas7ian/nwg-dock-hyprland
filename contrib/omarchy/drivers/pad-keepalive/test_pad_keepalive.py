"""Tests for pad-keepalive without hardware: a fake sysfs tree and fake evdev devices.

Run: cd contrib/omarchy/drivers/pad-keepalive && PYTHONDONTWRITEBYTECODE=1 /usr/bin/python3 -m unittest test_pad_keepalive
"""
import importlib.machinery
import importlib.util
import os
import tempfile
import unittest
from unittest import mock

HERE = os.path.dirname(os.path.abspath(__file__))


def load(sysfs):
    os.environ["PAD_KEEPALIVE_SYSFS"] = sysfs
    os.environ["PAD_KEEPALIVE_DEV"] = "/dev/input"
    loader = importlib.machinery.SourceFileLoader("pad_keepalive", os.path.join(HERE, "pad-keepalive"))
    spec = importlib.util.spec_from_loader("pad_keepalive", loader)
    mod = importlib.util.module_from_spec(spec)
    loader.exec_module(mod)
    return mod


def add_node(root, name, vendor, product, phys):
    dev = os.path.join(root, name, "device")
    os.makedirs(os.path.join(dev, "id"))
    for rel, val in (("id/vendor", vendor), ("id/product", product), ("phys", phys)):
        with open(os.path.join(dev, rel), "w") as f:
            f.write(val + "\n")


class FakeUInput:
    def __init__(self, *a, **kw):
        self.fd = os.pipe()[0]
        self.device = mock.Mock(path="/dev/input/event99")
        self.written = []

    def write(self, t, c, v):
        self.written.append((t, c, v))

    def syn(self):
        self.written.append(("syn",))


class FakePad:
    def __init__(self, grab_error=None, keys=(), axes=None):
        self.path, self.phys = "/dev/input/event22", "usb-0000:0b:00.4-2.2/input0"
        self.fd = os.pipe()[0]
        self.grab_error, self.keys, self.axes = grab_error, list(keys), axes or {}
        self.closed = False

    def grab(self):
        if self.grab_error:
            raise OSError(16, self.grab_error)

    def close(self):
        self.closed = True

    def active_keys(self):
        return self.keys

    def absinfo(self, code):
        return mock.Mock(value=self.axes.get(code, 0))


class FindPhysical(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name

    def tearDown(self):
        self.tmp.cleanup()

    def test_finds_the_usb_pad(self):
        add_node(self.root, "event3", "046d", "c548", "usb-0000:09:00.0-9/input0")
        add_node(self.root, "event22", "045e", "028e", "usb-0000:0b:00.4-2.2/input0")
        self.assertEqual(load(self.root).find_physical_path(), "/dev/input/event22")

    def test_ignores_its_own_virtual_pad(self):
        add_node(self.root, "event23", "045e", "028e", "pad-keepalive/input0")
        self.assertIsNone(load(self.root).find_physical_path())

    def test_ignores_other_microsoft_devices(self):
        add_node(self.root, "event5", "045e", "0b12", "usb-0000:09:00.0-3/input0")
        self.assertIsNone(load(self.root).find_physical_path())

    def test_missing_sysfs_or_attributes(self):
        os.makedirs(os.path.join(self.root, "event7", "device"))
        self.assertIsNone(load(self.root).find_physical_path())
        self.assertIsNone(load(os.path.join(self.root, "nope")).find_physical_path())

    def test_nothing_is_opened_while_unplugged(self):
        add_node(self.root, "event3", "046d", "c548", "usb-0000:09:00.0-9/input0")
        mod = load(self.root)
        with mock.patch.object(mod.evdev, "InputDevice") as dev:
            self.assertIsNone(mod.find_physical())
            dev.assert_not_called()


class ProxyBehaviour(unittest.TestCase):
    def setUp(self):
        self.mod = load("/nonexistent")
        p = mock.patch.object(self.mod.evdev, "UInput", FakeUInput)
        p.start()
        self.addCleanup(p.stop)
        self.log = mock.patch.object(self.mod, "log").start()
        self.addCleanup(mock.patch.stopall)
        self.proxy = self.mod.Proxy()

    def test_grab_failure_is_logged_once_and_backs_off(self):
        self.assertFalse(self.proxy.attach(FakePad(grab_error="Device or resource busy")))
        self.assertFalse(self.proxy.attach(FakePad(grab_error="Device or resource busy")))
        grab_logs = [c for c in self.log.call_args_list if "cannot grab" in c.args[0]]
        self.assertEqual(len(grab_logs), 1)
        self.assertIsNone(self.proxy.phys)

    def test_attach_syncs_held_buttons_and_sticks(self):
        e = self.mod.e
        self.assertTrue(self.proxy.attach(FakePad(keys=[e.BTN_A], axes={e.ABS_X: 12000})))
        w = self.proxy.ui.written
        self.assertIn((e.EV_KEY, e.BTN_A, 1), w)
        self.assertIn((e.EV_KEY, e.BTN_B, 0), w)
        self.assertIn((e.EV_ABS, e.ABS_X, 12000), w)
        self.assertEqual(w[-1], ("syn",))

    def test_detach_releases_everything_and_keeps_virtual(self):
        e = self.mod.e
        pad = FakePad(keys=[e.BTN_A])
        self.proxy.attach(pad)
        self.proxy.ui.written.clear()
        self.proxy.detach("No such device")
        self.assertTrue(pad.closed)
        self.assertIsNone(self.proxy.phys)
        self.assertIn((e.EV_KEY, e.BTN_A, 0), self.proxy.ui.written)
        self.assertIn((e.EV_ABS, e.ABS_X, 0), self.proxy.ui.written)
        self.assertEqual(self.proxy.ui.written[-1], ("syn",))

    def test_rumble_is_reuploaded_to_a_new_physical_pad(self):
        self.proxy.effects[0] = self.mod.evdev.ff.Effect()
        pad = FakePad()
        pad.upload_effect = mock.Mock(return_value=3)
        self.proxy.attach(pad)
        self.assertEqual(self.proxy.phys_effect(0), 3)
        self.proxy.detach("gone")
        self.assertEqual(self.proxy.mapped, {})
        pad2 = FakePad()
        pad2.upload_effect = mock.Mock(return_value=5)
        self.proxy.attach(pad2)
        self.assertEqual(self.proxy.phys_effect(0), 5)
        self.assertEqual(pad2.upload_effect.call_args.args[0].id, -1)


if __name__ == "__main__":
    unittest.main()
