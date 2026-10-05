"""Tests for plugins/juansebas7ian.icloud/icloud.py without iCloud or a mount:
sync state from the VFS cache, path confinement, the 30-day sign-in count and
auth-error detection.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_icloud
"""

import io
import json
import os
import tempfile
import time
import unittest
from contextlib import redirect_stdout
from importlib.machinery import SourceFileLoader
from pathlib import Path
from unittest import mock

HELPER = Path(__file__).resolve().parent.parent / "plugins" / "juansebas7ian.icloud" / "icloud.py"


def load(tmp):
  env = {"ICLOUD_MOUNT": str(tmp / "mnt"), "XDG_STATE_HOME": str(tmp / "state"),
         "XDG_CACHE_HOME": str(tmp / "cache"), "ICLOUD_SOCK": str(tmp / "none.sock")}
  with mock.patch.dict(os.environ, env):
    return SourceFileLoader("icloud_helper", str(HELPER)).load_module()


class Helper(unittest.TestCase):
  def setUp(self):
    self.tmp = tempfile.TemporaryDirectory()
    self.root = Path(self.tmp.name)
    (self.root / "mnt").mkdir()
    self.m = load(self.root)

  def tearDown(self):
    self.tmp.cleanup()

  def test_local_state_from_sparse_cache(self):
    cache = self.root / "vfs"
    cache.mkdir()
    (cache / "full.txt").write_bytes(b"x" * 10000)
    with open(cache / "sparse.bin", "wb") as f:   # 1 MB file with only its first 4 KB present
      f.write(b"y" * 4096)
      f.truncate(1 << 20)
    with open(cache / "hole.bin", "wb") as f:     # nothing downloaded yet
      f.truncate(1 << 20)
    self.assertEqual(self.m.local_state(cache, "full.txt", 10000), "local")
    self.assertEqual(self.m.local_state(cache, "sparse.bin", 1 << 20), "partial")
    self.assertEqual(self.m.local_state(cache, "hole.bin", 1 << 20), "cloud")
    self.assertEqual(self.m.local_state(cache, "missing.txt", 5), "cloud")
    self.assertEqual(self.m.local_state(None, "full.txt", 5), "cloud")

  def test_paths_cannot_leave_the_mount(self):
    (self.root / "mnt" / "Docs").mkdir()
    self.assertEqual(self.m.safe_rel("/Docs/")[0], "Docs")
    self.assertEqual(self.m.safe_rel("")[1], self.root / "mnt")
    for bad in ("..", "Docs/../../etc", "../mnt2"):
      with self.assertRaises(ValueError):
        self.m.safe_rel(bad)

  def test_keep_refuses_everything_and_unmounted(self):
    with self.assertRaises(ValueError):
      self.m.keep("")
    with self.assertRaises(ValueError):    # tmp/mnt is a plain folder, not a mount
      self.m.keep("Docs")

  def test_sign_in_countdown(self):
    self.assertEqual(self.m.auth_info(), {"issued": None, "daysLeft": None})
    stamp = self.root / "state" / "omarchy-icloud" / "authenticated"
    stamp.parent.mkdir(parents=True)
    stamp.write_text(str(int(time.time() - 27 * 86400)))
    self.assertAlmostEqual(self.m.auth_info()["daysLeft"], 3.0, places=1)
    stamp.write_text(str(int(time.time() - 31 * 86400)))
    self.assertLess(self.m.auth_info()["daysLeft"], 0)

  def test_auth_errors_are_recognised(self):
    for line in ("HTTP error 421 (Misdirected Request)", "Missing PCS cookies from the request",
                 "trust token expired", "authentication failed"):
      self.assertTrue(self.m.AUTH_ERROR.search(line), line)
    self.assertFalse(self.m.AUTH_ERROR.search("couldn't upload: disk full"))

  def test_status_without_remote_does_not_touch_the_network(self):
    with mock.patch.object(self.m, "configured", return_value=False), \
         mock.patch.object(self.m, "quota", side_effect=AssertionError("network")), \
         mock.patch.object(self.m, "service_state", return_value="inactive"):
      out = io.StringIO()
      with redirect_stdout(out):
        self.m.status()
    data = json.loads(out.getvalue())
    self.assertFalse(data["configured"])
    self.assertFalse(data["mounted"])

  def test_ls_unmounted(self):
    out = io.StringIO()
    with redirect_stdout(out):
      self.m.ls("")
    self.assertEqual(json.loads(out.getvalue())["error"], "not mounted")


if __name__ == "__main__":
  unittest.main()
