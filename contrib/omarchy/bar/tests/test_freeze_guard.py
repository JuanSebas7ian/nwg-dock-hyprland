"""Tests for bin/omarchy-freeze-guard: the socket probe against a live and a
frozen fake Hyprland, how the previous boot ended, and the daemon's
report -> recover sequence (no real Hyprland, no signals sent).

Run: cd contrib/omarchy/bar && python3 -m unittest tests.test_freeze_guard
"""

import os
import socket
import tempfile
import threading
import types
import unittest
from importlib.machinery import SourceFileLoader
from pathlib import Path

BIN = Path(__file__).resolve().parent.parent / "bin" / "omarchy-freeze-guard"


def load(tmp):
  m = SourceFileLoader("omarchy_freeze_guard", str(BIN)).load_module()
  m.STATE = Path(tmp) / "state"
  m.SESSION_DIR = Path(tmp) / "session"
  m.RUN = Path(tmp) / "run"
  m.notify = lambda *a, **k: None
  return m


def fake_hyprland(m, answer):
  d = m.RUN / "hypr" / "sig"
  d.mkdir(parents=True)
  srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
  srv.bind(str(d / ".socket.sock"))
  srv.listen(4)

  def serve():
    while True:
      try:
        c, _ = srv.accept()
      except OSError:
        return
      c.recv(64)
      if answer:
        c.sendall(b'{"branch": "v0.56.2"}')
        c.close()
      # frozen: keep the connection open and never answer

  threading.Thread(target=serve, daemon=True).start()
  return srv


class Probe(unittest.TestCase):
  def setUp(self):
    self.tmp = tempfile.mkdtemp()
    self.m = load(self.tmp)
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "sig"

  def test_live(self):
    srv = fake_hyprland(self.m, True)
    self.assertIsNone(self.m.probe(1))
    srv.close()

  def test_frozen(self):
    srv = fake_hyprland(self.m, False)
    self.assertIn("no answer", self.m.probe(0.3))
    srv.close()

  def test_no_socket(self):
    self.assertIsNotNone(self.m.probe(0.3))


class PreviousBoot(unittest.TestCase):
  def setUp(self):
    self.m = load(tempfile.mkdtemp())

  def verdict(self, lines):
    self.m.run = lambda cmd, timeout=5: "\n".join(lines)
    return self.m.previous_boot()[0]

  def test_clean_shutdown(self):
    self.assertEqual(self.verdict(["x systemd[1]: home.mount: Deactivated successfully.",
                                   "x systemd[1]: Unmounted /home."]), "clean")

  def test_reset(self):
    # 2026-10-07 20:44: the journal just stops, the last lines are routine timers.
    self.assertEqual(self.verdict(["x systemd[1481]: Starting Google Calendar...",
                                   "x uwsm_hyprland.desktop[1]: > Warning: Unsupported maximum keycode 709, clipping.",
                                   "x omarchy-gcal[1]: {\"ok\": true}"]), "reset")

  def test_never_woke(self):
    # 2026-10-06 13:55
    self.assertEqual(self.verdict(["x systemd-logind[1]: The system will suspend now!",
                                   "x systemd-sleep[2]: Performing sleep operation 'suspend'...",
                                   "x kernel: PM: suspend entry (deep)"]), "suspend")

  def test_suspend_that_woke_then_reset(self):
    self.assertEqual(self.verdict(["x kernel: PM: suspend entry (deep)", "x kernel: PM: suspend exit",
                                   "x foo: bar"]), "reset")

  def test_journal_unreadable(self):
    self.assertEqual(self.verdict([]), "unknown")


class Daemon(unittest.TestCase):
  def run_daemon(self, answers, cfg):
    tmp = tempfile.mkdtemp()
    m = load(tmp)
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "sig"
    clock = [0.0]
    m.time = types.SimpleNamespace(
      sleep=lambda s: clock.__setitem__(0, clock[0] + s),
      monotonic=lambda: clock[0], clock_gettime=lambda c: clock[0], CLOCK_BOOTTIME=7)
    script = list(answers)
    m.probe = lambda t: script.pop(0) if script else None
    m.hypr_pid = lambda: 4242 if script or not cfg.get("exit_when_done") else None
    m.config = lambda: dict(m.DEFAULTS, **{k: v for k, v in cfg.items() if k in m.DEFAULTS})
    m.check_previous_boot = lambda: None
    m.mirror_log = lambda size: size
    reports, recovers = [], []

    def report(reason, pid=None, failures=()):
      p = m.STATE / f"freeze-{len(reports)}.txt"
      m.STATE.mkdir(parents=True, exist_ok=True)
      p.write_text(reason + "\n")
      reports.append((clock[0], p))
      return p

    m.report = report
    m.recover = lambda pid: recovers.append((clock[0], pid)) or "aborted"
    rc = m.daemon()
    return rc, reports, recovers

  def test_freeze_reports_then_recovers(self):
    rc, reports, recovers = self.run_daemon(["no answer in 2 s"] * 100, {"reportAfter": 10, "recoverAfter": 60})
    self.assertEqual(rc, 0)
    self.assertEqual(len(reports), 1)
    self.assertTrue(10 <= reports[0][0] <= 14, reports)
    self.assertEqual(len(recovers), 1)
    self.assertTrue(60 <= recovers[0][0] <= 64, recovers)
    self.assertIn("RECOVERY", reports[0][1].read_text())

  def test_short_hiccup_only_reports(self):
    rc, reports, recovers = self.run_daemon(["no answer in 2 s"] * 8 + [None],
                                            {"reportAfter": 10, "recoverAfter": 60, "exit_when_done": True})
    self.assertEqual(rc, 0)
    self.assertEqual(len(reports), 1)
    self.assertEqual(recovers, [])
    self.assertIn("RECOVERED by itself", reports[0][1].read_text())

  def test_recover_disabled(self):
    rc, reports, recovers = self.run_daemon(["no answer in 2 s"] * 100,
                                            {"reportAfter": 10, "recoverAfter": 0, "exit_when_done": True})
    self.assertEqual(len(reports), 1)
    self.assertEqual(recovers, [])


if __name__ == "__main__":
  unittest.main()
