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


class Ladder(unittest.TestCase):
  """Drives Guard.tick with a fake clock; nothing is signalled for real."""

  def setUp(self):
    self.m = load(tempfile.mkdtemp())
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "sig"
    m = self.m
    self.reports, self.recovers, self.x_restarts, self.shell_restarts = [], [], [], []
    self.state = {"hypr": "S", "x_answers": True, "shell": [], "stopped": []}

    def report(reason, pid=None, failures=()):
      m.STATE.mkdir(parents=True, exist_ok=True)
      p = m.STATE / f"freeze-{len(self.reports)}.txt"
      p.write_text(reason + "\n")
      self.reports.append(p)
      return p

    m.report = report
    m.recover = lambda pid: self.recovers.append(pid) or "aborted"
    m.proc_state = lambda pid: self.state["hypr"]
    m.thaw_stopped = lambda: self.state["stopped"] and [self.state["stopped"].pop()]
    m.xwayland = lambda: (77, ":0")
    m.xwayland_ping = lambda d: self.state["x_answers"]
    m.restart_xwayland = lambda pid: self.x_restarts.append(pid) or True
    m.shell_ping = lambda: self.state["shell"].pop(0) if self.state["shell"] else True
    m.restart_shell = lambda: self.shell_restarts.append(1) or (True, "")

  def drive(self, answers, cfg=None, every=2):
    g = self.m.Guard(dict(self.m.DEFAULTS, **(cfg or {})))
    script = list(answers)
    self.m.probe = lambda timeout: script.pop(0)
    t, out = 0.0, None
    while script:
      t += every
      out = g.tick(t, 4242)
      if out:
        break
    return g, t, out

  def test_gpu_freeze_goes_to_restart(self):
    self.state["hypr"] = "D"
    g, t, out = self.drive(["no answer"] * 100)
    self.assertEqual(out, "recovered")
    self.assertTrue(60 <= t <= 64)
    self.assertEqual(len(self.reports), 1)
    self.assertEqual(self.x_restarts, [], "Xwayland is not touched when Hyprland is inside the kernel")
    self.assertIn("state D", self.reports[0].read_text())

  def test_hung_xwayland_is_restarted_and_desktop_thaws(self):
    self.state["x_answers"] = False
    g, t, out = self.drive(["no answer"] * 11 + [None, None])
    self.assertIsNone(out)
    self.assertEqual(self.x_restarts, [77])
    self.assertEqual(self.recovers, [])
    text = self.reports[0].read_text()
    self.assertIn("Xwayland :0 did not answer", text)
    self.assertIn("THAWED", text)

  def test_xwayland_fine_is_left_alone(self):
    g, t, out = self.drive(["no answer"] * 100)
    self.assertEqual(self.x_restarts, [])
    self.assertIn("Xwayland (:0) answers", self.reports[0].read_text())
    self.assertEqual(out, "recovered")

  def test_short_hiccup_no_report(self):
    g, t, out = self.drive(["no answer"] * 3 + [None])
    self.assertEqual(self.reports, [])
    self.assertEqual(self.recovers, [])

  def test_steps_can_be_turned_off(self):
    self.state["x_answers"] = False
    g, t, out = self.drive(["no answer"] * 100, {"recoverAfter": 0, "xwaylandAfter": 0})
    self.assertIsNone(out)
    self.assertEqual(self.recovers, [])
    self.assertEqual(self.x_restarts, [])

  def test_stopped_process_gets_sigcont(self):
    self.state["stopped"] = ["Hyprland (4242)"]
    g, t, out = self.drive(["no answer", None])
    self.assertEqual(self.state["stopped"], [])
    self.assertEqual(self.reports, [], "a thawed hiccup needs no report")

  def test_hung_shell_restarted_after_three_failures(self):
    # ok once (seen), then 3 failures -> one restart
    self.state["shell"] = [True, False, False, False]
    self.drive([None] * 30, {"shellEvery": 10})
    self.assertEqual(self.shell_restarts, [1])

  def test_shell_never_seen_is_not_restarted(self):
    self.state["shell"] = [False] * 20
    self.drive([None] * 40, {"shellEvery": 10})
    self.assertEqual(self.shell_restarts, [])

  def test_shell_restart_rate_limited(self):
    self.state["shell"] = ([True] + [False] * 3) * 6
    self.drive([None] * 200, {"shellEvery": 2})
    self.assertEqual(len(self.shell_restarts), 3)


if __name__ == "__main__":
  unittest.main()
