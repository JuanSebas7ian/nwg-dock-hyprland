"""Scenario tests for bin/omarchy-freeze-guard: the real daemon() loop runs
against a simulated desktop (Hyprland, the shell, Xwayland, the screen, a
clock that can be suspended) with one fault injected at a time. Each test
checks that the guard notices, applies the right remedy (and only that
one), the desktop comes back, and within how long.

Also: how it starts (environment, config, previous boot, log rotation),
that nothing in a tick can kill it, the systemd unit that brings it back,
and the real rescue helpers (restart_shell, recover, rescue) with the
process calls faked.

Run: cd contrib/omarchy/bar && python3 -m unittest tests.test_freeze_guard_scenarios
"""

import contextlib
import io
import json
import os
import signal
import tempfile
import types
import unittest
from importlib.machinery import SourceFileLoader
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
BIN = HERE / "bin" / "omarchy-freeze-guard"
UNIT = HERE / "systemd" / "omarchy-freeze-guard.service"
INSTALL = HERE / "install.sh"


class FakeOs:
  """The real os module, except that signals are recorded and sync is a no-op."""

  def __init__(self, kill):
    self.kill = kill

  def sync(self):
    pass

  def __getattr__(self, name):
    return getattr(os, name)


class StopSim(BaseException):
  """Ends a simulation; BaseException so the daemon's `except Exception` cannot swallow it."""


def load(tmp):
  m = SourceFileLoader("omarchy_freeze_guard_sim", str(BIN)).load_module()
  m.STATE = Path(tmp) / "state"
  m.SESSION_DIR = Path(tmp) / "session"
  m.RUN = Path(tmp) / "run"
  m.CONFIG = Path(tmp) / "config.json"
  m.log = lambda *a: None
  return m


class Desktop:
  """A desktop with one fault. Remedies clear the faults they can fix."""

  SOCKET_FAULTS = {"hypr_loop", "xwayland", "stopped_hypr", "gpu_D"}
  RENDER_FAULTS = {"render_kick", "render_dpms", "render_dead"}
  SHELL_FAULTS = {"shell_hung", "stopped_shell"}

  def __init__(self, m, fault=None, at=20.0, panel=False, screens=True, limit=600.0,
               shell_restart_fails=0, transient=0.0, suspend_at=None, suspend_for=0.0,
               crash_check=None, hypr_exits_at=None, shell_slow=()):
    self.m = m
    self.t = 0.0
    self.boot_offset = 0.0
    self.fault, self.at, self.panel, self.screens, self.limit = fault, at, panel, screens, limit
    self.shell_restart_fails = shell_restart_fails
    self.transient = transient
    self.suspend_at, self.suspend_for = suspend_at, suspend_for
    self.crash_check = crash_check
    self.hypr_exits_at = hypr_exits_at
    self.shell_slow = shell_slow  # (start, end) windows where the shell is slow but not hung
    self.hypr_alive = True
    self.fixed_at = None
    self.events = []          # (t, remedy)
    self.reports = []
    self.notes = []
    self.wire()

  # -- state
  def active(self, kind):
    return self.fault in kind and self.t >= self.at and self.fixed_at is None

  def fix(self, remedy):
    self.events.append((self.t, remedy))
    if self.fault is not None and self.fixed_at is None and self.t >= self.at:
      cures = {"hypr_loop": {"recover"}, "xwayland": {"restart_xwayland", "recover"},
               "stopped_hypr": {"sigcont"}, "gpu_D": set(),
               "render_kick": {"kick", "dpms", "recover"}, "render_dpms": {"dpms", "recover"},
               "render_dead": {"recover"}, "shell_hung": {"restart_shell"}, "stopped_shell": {"sigcont"}}
      if remedy in cures.get(self.fault, set()):
        self.fixed_at = self.t
    if remedy == "recover":
      self.hypr_alive = False

  def remedies(self):
    return [r for _, r in self.events]

  # -- fakes wired into the module
  def wire(self):
    m, d = self.m, self

    def sleep(s):
      if d.suspend_at is not None and d.t < d.suspend_at <= d.t + s:
        d.boot_offset += d.suspend_for       # the machine slept: boottime jumps, monotonic does not
        d.suspend_at = None
      d.t += s
      if d.hypr_exits_at is not None and d.t >= d.hypr_exits_at:
        d.hypr_alive = False
      if d.t > d.limit:
        raise StopSim()

    m.time = types.SimpleNamespace(sleep=sleep, monotonic=lambda: d.t,
                                   clock_gettime=lambda c: d.t + d.boot_offset, CLOCK_BOOTTIME=7)
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "sim"
    m.hypr_pid = lambda: 4242 if d.hypr_alive else None
    m.check_previous_boot = lambda force=False: None
    m.rotate_log = lambda: None
    m.mirror_log = lambda size: size

    def probe(timeout):
      if d.crash_check == "probe":
        raise RuntimeError("probe exploded")
      if d.transient and d.at <= d.t < d.at + d.transient:
        return "no answer in 2 s"
      if d.active(d.SOCKET_FAULTS):
        return "no answer in 2 s"
      return None

    m.probe = probe

    def screens_on():
      if d.crash_check == "screens":
        raise RuntimeError("screens exploded")
      return d.screens

    m.screens_on = screens_on
    m.render_probe = lambda timeout: "no frame in 3 s" if d.active(d.RENDER_FAULTS) else None
    m.proc_state = lambda pid: "D" if d.active({"gpu_D"}) else "S"

    def thaw():
      if d.active({"stopped_hypr", "stopped_shell"}):
        d.fix("sigcont")
        return ["Hyprland (4242)" if d.fault == "stopped_hypr" else "quickshell (77)"]
      return []

    m.thaw_stopped = thaw
    m.xwayland = lambda: (1239, ":0")
    m.xwayland_ping = lambda disp: not d.active({"xwayland"})
    m.restart_xwayland = lambda pid: d.fix("restart_xwayland") or True
    m.kick_renderer = lambda: d.fix("kick")
    m.dpms_cycle = lambda: d.fix("dpms")
    m.recover = lambda pid: d.fix("recover") or "aborted"
    m.shell_ping = lambda: not d.active(d.SHELL_FAULTS) and not any(a <= d.t < b for a, b in d.shell_slow)
    m.keyboard_panel_open = lambda: d.panel

    def restart_shell():
      if d.shell_restart_fails > 0:
        d.shell_restart_fails -= 1
        d.events.append((d.t, "restart_shell(failed)"))
        return False, "did not come back"
      d.fix("restart_shell")
      return True, "ok"

    m.restart_shell = restart_shell

    def report(reason, pid=None, failures=()):
      m.STATE.mkdir(parents=True, exist_ok=True)
      p = m.STATE / f"freeze-{len(d.reports):03d}.txt"
      p.write_text(reason + "\n")
      d.reports.append((d.t, reason, p))
      return p

    m.report = report
    m.notify = lambda summary, body, urgency="normal": d.notes.append((d.t, summary, urgency))

  def run(self):
    try:
      return self.m.daemon()
    except StopSim:
      return "still running"


class Scenario(unittest.TestCase):
  def sim(self, **kw):
    self.tmp = tempfile.mkdtemp()
    m = load(self.tmp)
    d = Desktop(m, **kw)
    d.rc = d.run()
    return d

  def assertFixedWithin(self, d, seconds, remedy):
    self.assertIsNotNone(d.fixed_at, f"never fixed; remedies {d.events}")
    self.assertLessEqual(d.fixed_at - d.at, seconds, f"took {d.fixed_at - d.at} s; remedies {d.events}")
    self.assertEqual(d.remedies()[-1], remedy, d.events)


class Healthy(Scenario):
  def test_ten_quiet_minutes_do_nothing(self):
    d = self.sim(fault=None, limit=600)
    self.assertEqual(d.rc, "still running")
    self.assertEqual((d.events, d.reports, d.notes), ([], [], []))

  def test_short_hiccup_is_not_a_freeze(self):
    d = self.sim(fault=None, transient=4, limit=120)
    self.assertEqual((d.events, d.reports), ([], []))

  def test_screens_off_with_no_frames_is_normal(self):
    d = self.sim(fault="render_dead", screens=False, limit=300)
    self.assertEqual((d.events, d.reports), ([], []))

  def test_logout_stops_the_daemon_cleanly(self):
    d = self.sim(fault=None, hypr_exits_at=50, limit=300)
    self.assertEqual(d.rc, 0)
    self.assertEqual(d.events, [])


class FrozenHyprland(Scenario):
  def test_stopped_hyprland_is_resumed_at_once(self):
    d = self.sim(fault="stopped_hypr")
    self.assertFixedWithin(d, 2, "sigcont")
    self.assertEqual(d.rc, "still running")
    self.assertNotIn("recover", d.remedies())

  def test_hung_xwayland_is_restarted_and_windows_survive(self):
    d = self.sim(fault="xwayland")
    self.assertFixedWithin(d, 16, "restart_xwayland")
    self.assertEqual(d.rc, "still running", "Hyprland was not restarted")
    self.assertNotIn("recover", d.remedies())
    self.assertTrue(any("desktop was frozen" in n[1] for n in d.notes), d.notes)
    self.assertIn("THAWED", d.reports[0][2].read_text())

  def test_hyprland_main_loop_hang_is_restarted_by_30_s(self):
    d = self.sim(fault="hypr_loop")
    self.assertFixedWithin(d, 34, "recover")
    self.assertEqual(d.rc, 0)
    self.assertEqual(len(d.reports), 1)
    # the report says "8 s": the first probe already waited its 2 s timeout
    self.assertTrue(5 <= d.reports[0][0] - d.at <= 12, d.reports)

  def test_gpu_stuck_in_kernel_still_tries_and_leaves_xwayland_alone(self):
    d = self.sim(fault="gpu_D")
    self.assertIn("recover", d.remedies())
    self.assertNotIn("restart_xwayland", d.remedies())
    self.assertIn("state D", d.reports[0][2].read_text())


class FrozenPicture(Scenario):
  def test_renderer_stall_fixed_by_reload_without_blanking(self):
    d = self.sim(fault="render_kick")
    self.assertFixedWithin(d, 12, "kick")
    self.assertNotIn("dpms", d.remedies())
    self.assertTrue(any("screen was frozen" in n[1] for n in d.notes), d.notes)

  def test_display_stall_fixed_by_dpms(self):
    d = self.sim(fault="render_dpms")
    self.assertFixedWithin(d, 18, "dpms")
    self.assertEqual(d.remedies(), ["kick", "dpms"])
    self.assertEqual(d.rc, "still running")

  def test_no_frames_at_all_restarts_hyprland(self):
    d = self.sim(fault="render_dead")
    self.assertFixedWithin(d, 38, "recover")
    self.assertEqual(d.remedies(), ["kick", "dpms", "recover"])
    self.assertEqual(d.rc, 0)


class FrozenShell(Scenario):
  def test_hung_shell_with_panel_open_is_replaced_fast(self):
    # 2026-10-08 09:40: Cloud panel open, the shell spun, no typing anywhere.
    d = self.sim(fault="shell_hung", panel=True)
    self.assertFixedWithin(d, 10, "restart_shell")
    self.assertIn("panel open", d.reports[0][1])

  def test_hung_shell_without_panel(self):
    d = self.sim(fault="shell_hung", panel=False)
    self.assertFixedWithin(d, 16, "restart_shell")

  def test_stopped_shell_is_resumed_not_killed(self):
    d = self.sim(fault="stopped_shell", panel=True)
    self.assertFixedWithin(d, 2, "sigcont")
    self.assertNotIn("restart_shell", d.remedies())

  def test_failed_shell_restart_is_retried(self):
    d = self.sim(fault="shell_hung", panel=True, shell_restart_fails=1)
    self.assertFixedWithin(d, 25, "restart_shell")
    self.assertEqual(d.remedies(), ["restart_shell(failed)", "restart_shell"])

  def test_shell_that_never_comes_back_is_rate_limited_then_retried(self):
    d = self.sim(fault="shell_hung", panel=True, shell_restart_fails=99, limit=20 + 1000)
    first = [t for t, r in d.events if r == "restart_shell(failed)"]
    in_window = [t for t in first if t < first[0] + 900]
    self.assertEqual(len(in_window), 3, first)
    self.assertGreater(len(first), 3, "tries again once the 15 min window passes")
    criticals = [n for n in d.notes if "keeps freezing" in n[1] and n[0] < first[0] + 900]
    self.assertEqual(len(criticals), 1, "warned once per window, not on every miss")

  def test_shell_never_seen_is_left_alone(self):
    # e.g. the shell has not started yet in this session
    d = self.sim(fault="shell_hung", at=0, panel=True, limit=200)
    self.assertEqual(d.events, [])


class Robustness(Scenario):
  def test_suspend_is_not_a_freeze(self):
    d = self.sim(fault=None, suspend_at=40, suspend_for=3600, limit=300)
    self.assertEqual((d.events, d.reports), ([], []))

  def test_freeze_after_resume_is_still_caught(self):
    d = self.sim(fault="xwayland", at=100, suspend_at=40, suspend_for=3600, limit=300)
    self.assertFixedWithin(d, 16, "restart_xwayland")

  def test_slow_shell_around_a_suspend_is_not_counted_twice(self):
    # one miss going to sleep and one waking up are not "2 misses in a row"
    d = self.sim(fault=None, suspend_at=41, suspend_for=3600, shell_slow=[(36, 41), (41, 46)], limit=200)
    self.assertEqual(d.events, [])

  def test_a_crashing_check_does_not_stop_the_guard(self):
    d = self.sim(fault=None, crash_check="probe", limit=60)
    self.assertEqual(d.rc, "still running")

  def test_crashing_screen_check_still_lets_shell_rescue_run_later(self):
    tmp = tempfile.mkdtemp()
    m = load(tmp)
    d = Desktop(m, fault="xwayland", at=30, crash_check="screens", limit=200)
    self.assertEqual(d.run(), "still running")
    self.assertIn("restart_xwayland", d.remedies())

  def test_steps_switched_off_by_config(self):
    tmp = tempfile.mkdtemp()
    m = load(tmp)
    m.CONFIG.write_text(json.dumps({"recoverAfter": 0, "xwaylandAfter": 0}))
    d = Desktop(m, fault="xwayland", limit=200)
    self.assertEqual(d.run(), "still running")
    self.assertNotIn("restart_xwayland", d.remedies())
    self.assertNotIn("recover", d.remedies())
    self.assertEqual(len(d.reports), 1, "still reports it")


class Startup(unittest.TestCase):
  def setUp(self):
    self.tmp = tempfile.mkdtemp()
    self.m = load(self.tmp)

  def test_without_hyprland_environment_exits_for_systemd_to_retry(self):
    os.environ.pop("HYPRLAND_INSTANCE_SIGNATURE", None)
    self.assertEqual(self.m.daemon(), 1)

  def test_bad_config_files_fall_back_to_defaults(self):
    m = self.m
    for content in ["{not json", "[1, 2]", '"text"', "null",
                    json.dumps({"recoverAfter": "abc", "probeEvery": 0, "reportAfter": -5,
                                "renderEvery": float("inf") if False else 1e999, "bogus": 3, "shellFailures": None})]:
      m.CONFIG.write_text(content)
      cfg = m.config()
      for k, v in m.DEFAULTS.items():
        self.assertEqual(cfg[k], float(v), f"{content!r}: {k}")
      m.Guard(cfg)  # must not raise

  def test_valid_config_values_apply(self):
    self.m.CONFIG.write_text(json.dumps({"recoverAfter": 45, "thawStopped": False, "shellFailures": 0}))
    cfg = self.m.config()
    self.assertEqual((cfg["recoverAfter"], cfg["thawStopped"], cfg["shellFailures"]), (45.0, 0.0, 0.0))

  def test_previous_boot_check_failure_does_not_stop_startup(self):
    m = self.m
    d = Desktop(m, fault=None, limit=10)

    def boom(force=False):
      raise RuntimeError("journal unreadable")

    m.check_previous_boot = boom
    self.assertEqual(d.run(), "still running")

  def test_log_from_the_crashed_instance_is_kept(self):
    m = self.m
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "new"
    m.STATE.mkdir(parents=True)
    (m.STATE / "hyprland-current.log").write_text("# instance old\nlast words\n")
    m.rotate_log()
    kept = list(m.STATE.glob("hyprland-2*.log"))
    self.assertEqual(len(kept), 1)
    self.assertIn("last words", kept[0].read_text())
    self.assertFalse((m.STATE / "hyprland-current.log").exists())

  def test_log_of_this_instance_is_not_rotated(self):
    m = self.m
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "same"
    m.STATE.mkdir(parents=True)
    (m.STATE / "hyprland-current.log").write_text("# instance same\n")
    m.rotate_log()
    self.assertTrue((m.STATE / "hyprland-current.log").exists())

  def test_previous_boot_is_reported_once_per_boot(self):
    m = self.m
    notes = []
    m.notify = lambda *a, **k: notes.append(a)
    m.previous_boot = lambda: ("reset", ["Oct 07 20:44:42.598032 x: last line"])
    m.read = (lambda real: lambda p, default="": "boot-A" if str(p).endswith("boot_id") else real(p, default))(m.read)
    self.assertEqual(m.check_previous_boot(), "reset")
    self.assertIsNone(m.check_previous_boot())
    self.assertEqual(len(notes), 1)
    self.assertEqual(len(list(m.STATE.glob("boot-*-reset.txt"))), 1)


class Unit(unittest.TestCase):
  """What brings the guard up, and back."""

  def setUp(self):
    self.unit = UNIT.read_text()

  def test_restarts_always_without_limit(self):
    self.assertIn("Restart=always", self.unit)
    self.assertIn("StartLimitIntervalSec=0", self.unit)

  def test_starts_with_every_graphical_session(self):
    self.assertIn("WantedBy=graphical-session.target", self.unit)
    self.assertIn("PartOf=graphical-session.target", self.unit)
    self.assertIn("ExecStart=%h/.local/bin/omarchy-freeze-guard daemon", self.unit)

  def test_installer_installs_and_enables_it(self):
    sh = INSTALL.read_text()
    self.assertRegex(sh, r"COLLECTORS=\([^)]*omarchy-freeze-guard")
    self.assertIn("systemctl --user enable omarchy-freeze-guard.service", sh)
    self.assertIn("systemctl --user restart omarchy-freeze-guard.service", sh)


class Helpers(unittest.TestCase):
  """The real rescue helpers, with signals and processes faked."""

  def setUp(self):
    self.tmp = tempfile.mkdtemp()
    self.m = load(self.tmp)
    self.m.time = types.SimpleNamespace(sleep=lambda s: None)
    self.ran = []
    self.m.run = lambda cmd, timeout=5: self.ran.append(cmd) or ""
    self.m.threads = lambda pid: ""
    self.m.proc_state = lambda pid: "R"

  def fake_kill(self, on_kill):
    self.kills = []

    def kill(pid, sig):
      self.kills.append((pid, sig))
      on_kill(pid, sig)

    self.m.os = FakeOs(kill)

  # restart_shell
  def test_shell_relaunched_by_its_supervisor(self):
    pids = {"now": [500]}
    self.fake_kill(lambda pid, sig: pids.__setitem__("now", [501]))
    self.m.shell_pids = lambda: list(pids["now"])
    self.m.shell_ping = lambda: True
    self.m.session_locked = lambda: False
    ok, out = self.m.restart_shell()
    self.assertTrue(ok)
    self.assertEqual(self.kills, [(500, signal.SIGKILL)])
    self.assertFalse(any("omarchy-launch-shell" in " ".join(c) for c in self.ran))

  def test_shell_launched_when_nothing_relaunches_it(self):
    pids = {"now": [500]}
    self.fake_kill(lambda pid, sig: pids.__setitem__("now", []))

    def run(cmd, timeout=5):
      self.ran.append(cmd)
      if "omarchy-launch-shell" in " ".join(cmd):
        pids["now"] = [600]
      return ""

    self.m.run = run
    self.m.shell_pids = lambda: list(pids["now"])
    self.m.shell_ping = lambda: True
    self.m.session_locked = lambda: False
    ok, out = self.m.restart_shell()
    self.assertTrue(ok)
    self.assertIn("started omarchy-launch-shell", out)

  def test_locked_session_is_locked_again(self):
    pids = {"now": [500]}
    self.fake_kill(lambda pid, sig: pids.__setitem__("now", [501]))
    self.m.shell_pids = lambda: list(pids["now"])
    self.m.shell_ping = lambda: True
    self.m.session_locked = lambda: True
    ok, out = self.m.restart_shell()
    self.assertTrue(ok)
    self.assertTrue(any(c[-2:] == ["lock", "lock"] for c in self.ran), self.ran)

  def test_shell_that_never_answers_reports_failure(self):
    self.fake_kill(lambda pid, sig: None)
    self.m.shell_pids = lambda: [500]
    self.m.shell_ping = lambda: False
    self.m.session_locked = lambda: True
    ok, out = self.m.restart_shell()
    self.assertFalse(ok)
    self.assertFalse(any(c[-2:] == ["lock", "lock"] for c in self.ran), "no relock without a shell")

  def test_kill_errors_are_tolerated(self):
    def kill(pid, sig):
      raise PermissionError

    self.m.os = FakeOs(kill)
    self.m.shell_pids = lambda: [500]
    self.m.shell_ping = lambda: True
    self.m.session_locked = lambda: False
    ok, _ = self.m.restart_shell()
    self.assertTrue(ok)

  # recover
  def recover_with(self, dies_on):
    alive = {"v": True}

    def on_kill(pid, sig):
      if sig in dies_on:
        alive["v"] = False

    self.fake_kill(on_kill)
    real_path = Path
    self.m.Path = lambda p: types.SimpleNamespace(exists=lambda: alive["v"]) if str(p).startswith("/proc/") else real_path(p)
    self.m.SESSION_DIR.mkdir(parents=True)
    (self.m.SESSION_DIR / "session.json").write_text('{"windows": [1, 2]}')
    return self.m.recover(4242)

  def test_recover_abort_leaves_a_core_and_saves_the_session(self):
    out = self.recover_with({signal.SIGABRT})
    self.assertIn("aborted", out)
    self.assertEqual([s for _, s in self.kills], [signal.SIGABRT])
    self.assertEqual((self.m.SESSION_DIR / "session.freeze.json").read_text(), '{"windows": [1, 2]}')

  def test_recover_escalates_to_sigkill(self):
    out = self.recover_with({signal.SIGKILL})
    self.assertEqual(out, "killed")
    self.assertEqual([s for _, s in self.kills], [signal.SIGABRT, signal.SIGKILL])

  def test_recover_says_when_only_sysrq_can_help(self):
    out = self.recover_with(set())
    self.assertIn("state D", out)

  # rescue (manual)
  def test_manual_rescue_on_a_live_desktop_keeps_windows(self):
    m = self.m
    calls = []
    m.thaw_stopped = lambda: []
    m.hypr_pid = lambda: 4242
    m.probe = lambda t: None
    m.submap_reset = lambda: calls.append("submap")
    m.kick_renderer = lambda: calls.append("kick")
    m.xwayland = lambda: (None, None)
    m.shell_ping = lambda: False
    m.restart_shell = lambda: calls.append("shell") or (True, "")
    m.restart_dock = lambda: calls.append("dock") or True
    m.recover = lambda pid: calls.append("recover") or "x"
    m.report = lambda reason, pid=None, failures=(): Path(self.tmp) / "r.txt"
    m.notify = lambda *a, **k: None
    with contextlib.redirect_stdout(io.StringIO()):
      m.rescue()
    self.assertEqual(calls, ["submap", "kick", "shell", "dock"])

  def test_manual_rescue_hard_restarts_a_frozen_hyprland(self):
    m = self.m
    calls = []
    m.thaw_stopped = lambda: []
    m.hypr_pid = lambda: 4242
    m.probe = lambda t: "no answer"
    m.xwayland = lambda: (None, None)
    m.recover = lambda pid: calls.append("recover") or "aborted"
    m.report = lambda reason, pid=None, failures=(): Path(self.tmp) / "r.txt"
    m.notify = lambda *a, **k: None
    with contextlib.redirect_stdout(io.StringIO()):
      m.rescue(hard=False)
    self.assertEqual(calls, [])
    with contextlib.redirect_stdout(io.StringIO()):
      m.rescue(hard=True)
    self.assertEqual(calls, ["recover"])

  # small pieces
  def test_thaw_only_touches_stopped_processes(self):
    m = self.m
    states = {1: "T", 2: "S", 3: "t"}
    m.desktop_pids = lambda: {1: "Hyprland", 2: "Xwayland", 3: "quickshell"}
    m.proc_state = lambda pid: states[pid]
    self.fake_kill(lambda pid, sig: None)
    self.assertEqual(m.thaw_stopped(), ["Hyprland (1)", "quickshell (3)"])
    self.assertEqual(self.kills, [(1, signal.SIGCONT), (3, signal.SIGCONT)])

  def test_mirror_writes_only_when_the_log_grows(self):
    m = self.m
    os.environ["HYPRLAND_INSTANCE_SIGNATURE"] = "sig"
    d = m.RUN / "hypr" / "sig"
    d.mkdir(parents=True)
    (d / "hyprland.log").write_text("line 1\n")
    size = m.mirror_log(-1)
    cur = m.STATE / "hyprland-current.log"
    self.assertIn("line 1", cur.read_text())
    cur.write_text("marker")
    self.assertEqual(m.mirror_log(size), size)
    self.assertEqual(cur.read_text(), "marker", "unchanged log, no rewrite")

  def test_reports_are_pruned(self):
    m = self.m
    m.STATE.mkdir(parents=True)
    for i in range(30):
      (m.STATE / f"freeze-2026{i:04d}.txt").write_text("x")
    m.prune(m.STATE.glob("freeze-*.txt"), m.KEEP_REPORTS)
    left = sorted(p.name for p in m.STATE.glob("freeze-*.txt"))
    self.assertEqual(len(left), m.KEEP_REPORTS)
    self.assertEqual(left[-1], "freeze-20260029.txt", "keeps the newest")


if __name__ == "__main__":
  unittest.main()
