"""Tests for bin/omarchy-memguard.

Scenarios run the real Guard.tick against a simulated machine (RAM, swap,
pressure, VRAM, protected and ordinary apps) on an AI/ML workstation, and
check two things every time: the machine is defended against the sudden or
runaway spike, and nobody's work is slowed, paused, unloaded or killed
without need. Plus classification, config, leftovers after a crash, the
daemon loop, the systemd unit and the installer.

Run: cd contrib/omarchy/bar && python3 -m unittest tests.test_memguard
"""

import json
import os
import tempfile
import types
import unittest
from importlib.machinery import SourceFileLoader
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
BIN = HERE / "bin" / "omarchy-memguard"
UNIT = HERE / "systemd" / "omarchy-memguard.service"
INSTALL = HERE / "install.sh"
GB = 1 << 30
MB = 1 << 20


def load(tmp):
  m = SourceFileLoader("omarchy_memguard", str(BIN)).load_module()
  m.STATE = Path(tmp) / "state"
  m.CONFIG = Path(tmp) / "config.json"
  m.log = lambda *a: None
  return m


class FakeScope:
  def __init__(self, name, kind=None, mem=1 * GB, cpu_busy=False):
    self.name = name
    self.path = Path("/fake") / name
    self.kind = kind
    self.why = kind or ""
    self.current = mem
    self.swap = 0
    self.cpu = 0
    self.busy = cpu_busy
    self.pids = [1]


class Machine:
  """RAM/VRAM over time. `script(m, t)` mutates the machine each tick."""

  def __init__(self, m, total=30 * GB, avail=20 * GB, swap=60 * GB, scopes=(), vram=None, script=None,
               units_active=("gphotos-sync.service",)):
    self.m = m
    self.total, self.avail, self.swap_total, self.swap_free = total, avail, swap, swap
    self.psi_some = self.psi_full = 0.0
    self.scopes = list(scopes)
    self.vram = vram  # {"total", "free", "used", "procs": [...]}
    self.script = script or (lambda mach, t: None)
    self.active = set(units_active)
    self.frozen, self.thawed, self.highs, self.reclaims, self.notes = [], [], {}, [], []
    self.unloaded, self.avoid = [], set()
    self.loaded_models = ["qwen2.5-coder:7b"]
    self.ollama_sm = 0
    self.wire()

  def wire(self):
    m, mc = self.m, self
    m.unit_active = lambda u: u in mc.active
    m.freeze_unit = lambda u: mc.frozen.append(u) or True
    m.thaw_unit = lambda u: mc.thawed.append(u) or True
    m.set_memory_high = lambda path, v: mc.highs.__setitem__(path.name, v) or True
    m.reclaim = lambda path, n: mc.reclaims.append(path.name) or True
    def read(p, default="", real=m.read):
      p = Path(p)
      if str(p).startswith("/fake/"):
        return "max" if p.name == "memory.high" else str(mc.scope(p.parent.name).current)
      return real(p, default)

    m.read = read
    m.notify = lambda s, b, u="normal": mc.notes.append((s, u))
    m.set_avoid = lambda path, on: (mc.avoid.add(path.name) if on else mc.avoid.discard(path.name)) or True
    m.has_avoid = lambda path: path.name in mc.avoid
    m.ollama_loaded = lambda: list(mc.loaded_models)

    def unload(model):
      mc.loaded_models.remove(model)
      mc.unloaded.append(model)
      return True

    m.ollama_unload = unload
    m.ollama_generating = lambda vram: mc.ollama_sm >= 5
    m.comm_of = lambda pid: {900: "ollama", 950: "voxtype"}.get(pid, "python3")
    self.vox_resident = 1552 * MB
    self.vox_idle = True
    self.vox_calls = []
    m.voxtype_resident = lambda vram: mc.vox_resident
    m.voxtype_idle = lambda: mc.vox_idle

    def set_iso(on):
      mc.vox_calls.append(on)
      mc.vox_resident = 0 if on else 1552 * MB
      return True

    m.voxtype_set_isolation = set_iso
    m.label = lambda s: s.name

  def scope(self, name):
    return next(s for s in self.scopes if s.name == name)

  def mi(self):
    return {"MemTotal": self.total, "MemAvailable": max(0, self.avail),
            "SwapTotal": self.swap_total, "SwapFree": self.swap_free}

  def run(self, seconds, cfg=None, every=2):
    g = self.m.Guard(dict(self.m.config(), **(cfg or {})))
    t = 0.0
    self.levels = []
    while t < seconds:
      t += every
      self.script(self, t)
      for s in self.scopes:
        if s.busy:
          s.cpu += int(every * 1e6)
      lvl = g.tick(t, self.mi(), (self.psi_some, self.psi_full), self.vram, self.scopes)
      self.levels.append((t, lvl))
    self.guard = g
    return g


def ai_box(**kw):
  """A typical session: a training run, the IDE, a terminal, Chrome, Slack."""
  return [FakeScope("train.scope", "ai", 8 * GB, cpu_busy=True), FakeScope("ide.scope", "dev", 3 * GB, cpu_busy=True),
          FakeScope("foot.scope", "dev", 200 * MB), FakeScope("hyprland.scope", "desktop", 300 * MB, cpu_busy=True),
          FakeScope("chrome.scope", None, 3 * GB, cpu_busy=kw.get("chrome_busy", True)),
          FakeScope("slack.scope", None, 1 * GB)]


class Base(unittest.TestCase):
  def setUp(self):
    self.m = load(tempfile.mkdtemp())


class NormalWork(Base):
  def test_heavy_but_calm_ai_work_is_left_alone(self):
    mc = Machine(self.m, avail=6 * GB, scopes=ai_box())  # 20 % free: busy, not in danger
    mc.run(600)
    self.assertEqual(max(l for _, l in mc.levels), 0)
    self.assertEqual((mc.frozen, mc.highs, mc.reclaims, mc.notes, mc.unloaded), ([], {}, [], [], []))

  def test_protected_scopes_are_marked_for_oomd(self):
    mc = Machine(self.m, scopes=ai_box())
    mc.run(10)
    self.assertEqual(mc.avoid, {"train.scope", "ide.scope", "foot.scope", "hyprland.scope"})

  def test_mark_removed_when_an_app_stops_being_protected(self):
    mc = Machine(self.m, scopes=ai_box())
    mc.run(10)
    mc.scope("foot.scope").kind = None
    mc.run(10)  # a new Guard: as after a daemon restart
    self.assertNotIn("foot.scope", mc.avoid)

  def test_marks_set_by_systemd_itself_are_kept(self):
    mc = Machine(self.m, scopes=ai_box())
    mc.avoid.add("slack.scope")  # e.g. a unit started with ManagedOOMPreference=avoid
    mc.run(10)
    self.assertIn("slack.scope", mc.avoid)

  def test_loading_a_dataset_fast_with_swap_to_spare_only_warns(self):
    # +1 GB/s for 15 s from 20 GB free: RAM fills, but 60 GB of zram/swap remain and nobody waits yet.
    def load_data(mc, t):
      if t <= 16:
        mc.avail -= 1 * GB
        mc.scope("train.scope").current += 1 * GB

    mc = Machine(self.m, scopes=ai_box(), script=load_data)
    mc.run(120)
    self.assertLessEqual(max(l for _, l in mc.levels), 1)
    self.assertEqual((mc.frozen, mc.highs), ([], {}), "no pause, no cap for a legitimate load")

  def test_short_pressure_blip_does_not_escalate(self):
    def blip(mc, t):
      mc.psi_some = 12 if 20 <= t < 24 else 0

    mc = Machine(self.m, scopes=ai_box(), script=blip)
    mc.run(120)
    self.assertEqual(mc.frozen, [])
    self.assertEqual(mc.highs, {})


class Spikes(Base):
  def runaway_chrome(self, mc, t):
    """A tab leaks 600 MB/s; the machine starts waiting on memory."""
    if 20 <= t <= 60:
      mc.avail -= int(1.2 * GB)
      mc.swap_free -= int(1.5 * GB)
      mc.scope("chrome.scope").current += int(1.2 * GB)
      mc.psi_some = 30
    elif t > 60:
      mc.avail, mc.swap_free, mc.psi_some = 15 * GB, 55 * GB, 0  # it settled (or oomd took it)

  def test_runaway_app_is_capped_batch_paused_work_untouched(self):
    mc = Machine(self.m, scopes=ai_box(), script=self.runaway_chrome)
    mc.run(70)
    self.assertIn("chrome.scope", mc.highs, "the runaway is capped")
    self.assertEqual(set(mc.highs), {"chrome.scope"}, "nothing else is capped")
    self.assertEqual(mc.frozen, ["gphotos-sync.service"])
    self.assertNotIn("train.scope", mc.reclaims)
    self.assertTrue(any(u == "critical" for _, u in mc.notes))

  def test_everything_is_undone_once_calm(self):
    mc = Machine(self.m, scopes=ai_box(), script=self.runaway_chrome)
    mc.run(150)
    self.assertEqual(mc.highs.get("chrome.scope"), "max", "cap released")
    self.assertEqual(mc.thawed, ["gphotos-sync.service"])
    self.assertEqual(mc.guard.frozen, {})
    self.assertEqual(mc.guard.throttled, {})
    self.assertTrue(any("back to normal" in s for s, _ in mc.notes))

  def test_cap_stays_while_the_capped_app_keeps_leaking_into_swap(self):
    def leak(mc, t):
      c = mc.scope("chrome.scope")
      if t <= 10:  # spike: RAM grows, processes wait
        c.current += 1 * GB
        mc.avail -= 1 * GB
        mc.psi_some = 30
      elif t <= 120:  # capped: RAM stays, the leak goes on into swap; the machine is calm
        c.swap += 300 * MB
        mc.psi_some = 0
      # after 120 s the leak stops

    mc = Machine(self.m, scopes=ai_box(), script=leak)
    mc.run(110)
    self.assertNotEqual(mc.highs.get("chrome.scope"), "max", "still leaking: keep the cap")
    mc.run(0)
    mc2 = Machine(self.m, scopes=ai_box(), script=leak)
    mc2.run(200)
    self.assertEqual(mc2.highs.get("chrome.scope"), "max", "released once it stopped growing")

  def test_undo_waits_for_a_real_calm(self):
    def flapping(mc, t):
      mc.psi_some = 30 if (t // 10) % 2 == 0 and t < 100 else 0
      if t < 100:
        mc.scope("chrome.scope").current += 200 * MB

    mc = Machine(self.m, scopes=ai_box(), script=flapping)
    mc.run(100)
    self.assertEqual(mc.thawed, [], "10 s of calm is not 30 s")

  def test_under_pressure_your_training_keeps_growing_the_app_is_capped(self):
    # level 2 (processes waiting), not the edge: the run grows fastest but is work; Chrome is capped instead.
    def both_grow(mc, t):
      mc.scope("train.scope").current += 1 * GB
      mc.scope("chrome.scope").current += 200 * MB
      mc.psi_some, mc.psi_full = 30, 0

    mc = Machine(self.m, scopes=ai_box(), script=both_grow)
    mc.run(10)
    self.assertEqual(max(l for _, l in mc.levels), 2)
    self.assertNotIn("train.scope", mc.highs)
    self.assertIn("chrome.scope", mc.highs)

  def test_protected_runaway_is_capped_only_when_the_machine_is_about_to_die(self):
    def training_explodes(mc, t):
      mc.avail -= 2 * GB
      mc.swap_free -= 6 * GB
      mc.scope("train.scope").current += 2 * GB
      mc.psi_some, mc.psi_full = 40, 30

    mc = Machine(self.m, avail=12 * GB, swap=24 * GB, scopes=ai_box(chrome_busy=True), script=training_explodes)
    mc.run(10)
    self.assertEqual(max(l for _, l in mc.levels), 3)
    self.assertIn("train.scope", mc.highs, "slowed instead of letting the machine (and the run) crash")

  def test_desktop_is_never_capped(self):
    def compositor_grows(mc, t):
      mc.avail -= 2 * GB
      mc.swap_free -= 8 * GB
      mc.scope("hyprland.scope").current += 2 * GB
      mc.psi_some, mc.psi_full = 50, 40

    scopes = [FakeScope("hyprland.scope", "desktop", 1 * GB)]
    mc = Machine(self.m, avail=10 * GB, swap=20 * GB, scopes=scopes, script=compositor_grows)
    mc.run(10)
    self.assertEqual(mc.highs, {})

  def test_frozen_job_is_thawed_after_the_limit_even_under_pressure(self):
    def stuck(mc, t):
      mc.psi_some = 30

    mc = Machine(self.m, scopes=ai_box(), script=stuck)
    mc.run(31 * 60)
    self.assertEqual(mc.thawed, ["gphotos-sync.service"])

  def test_fuse_mounts_are_never_frozen(self):
    self.m.FREEZABLE.append("rclone-gdrive.service")
    try:
      mc = Machine(self.m, scopes=ai_box(), script=lambda mc, t: setattr(mc, "psi_some", 30),
                   units_active=("rclone-gdrive.service", "gphotos-sync.service"))
      mc.run(10)
      self.assertNotIn("rclone-gdrive.service", mc.frozen)
    finally:
      self.m.FREEZABLE.remove("rclone-gdrive.service")


class Quiet(Base):
  """Notifications are interruptions: only for what matters (seen live 2026-10-08:
  a build growing 400 MB/s warned and said "back to normal" every minute)."""

  def test_a_build_bursting_memory_with_swap_to_spare_is_silent(self):
    def build(mc, t):
      phase = int(t) % 60
      if phase < 20:  # 20 s of +400 MB/s, then the compiler frees it
        mc.avail -= 800 * MB
      else:
        mc.avail = 20 * GB

    mc = Machine(self.m, scopes=ai_box(), script=build)
    mc.run(15 * 60)
    self.assertIn(1, [l for _, l in mc.levels], "it does see the bursts")
    self.assertEqual(mc.notes, [], "but does not interrupt for them")

  def test_low_ram_warns_at_most_every_15_min(self):
    def flapping_low(mc, t):
      mc.avail = int(2.5 * GB) if int(t) % 120 < 60 else 15 * GB

    mc = Machine(self.m, scopes=ai_box(), script=flapping_low)
    mc.run(30 * 60)
    warns = [n for n in mc.notes if "running low" in n[0]]
    self.assertEqual(len(warns), 2, warns)
    self.assertFalse(any("back to normal" in n[0] for n in mc.notes), "nothing was paused or slowed")

  def test_escalation_warns_even_after_a_quiet_level_1(self):
    def rising(mc, t):
      mc.avail -= 900 * MB
      mc.psi_some = 0 if t < 10 else 30

    mc = Machine(self.m, scopes=ai_box(), script=rising)
    mc.run(16)
    self.assertTrue(any(u == "critical" for _, u in mc.notes), mc.notes)

  def test_escalation_warns_again_after_a_level_1_warning(self):
    def low_then_waiting(mc, t):
      mc.avail = int(2.5 * GB)  # level 1, real: warned
      mc.psi_some = 0 if t < 20 else 30  # then work starts waiting: level 2

    mc = Machine(self.m, scopes=ai_box(), script=low_then_waiting)
    mc.run(30)
    self.assertEqual([u for _, u in mc.notes if True][:2], ["normal", "critical"], mc.notes)


class IdleReclaim(Base):
  def pressure(self, mc, t):
    mc.avail = int(2.5 * GB)  # level 1
    mc.psi_some = 0

  def test_only_idle_ordinary_apps_give_memory_to_zram(self):
    mc = Machine(self.m, scopes=ai_box(chrome_busy=True), script=self.pressure)
    mc.run(11 * 60)
    self.assertIn("slack.scope", mc.reclaims, "Slack idle for 10 min")
    self.assertNotIn("chrome.scope", mc.reclaims, "Chrome in use")
    for protected in ("train.scope", "ide.scope", "foot.scope", "hyprland.scope"):
      self.assertNotIn(protected, mc.reclaims)

  def test_reclaim_is_rate_limited(self):
    mc = Machine(self.m, scopes=ai_box(), script=self.pressure)
    mc.run(15 * 60)
    n = mc.reclaims.count("slack.scope")
    self.assertTrue(3 <= n <= 6, n)  # once a minute after the first 10 min


class Vram(Base):
  def vram(self, free_mb, procs):
    total = 12288 * MB
    return {"total": total, "free": free_mb * MB, "used": total - free_mb * MB, "procs": procs}

  def test_a_big_model_filling_the_gpu_is_not_unloaded(self):
    mc = Machine(self.m, vram=self.vram(900, [{"pid": 900, "mem": 10 * GB, "type": "C", "sm": 0}]))
    mc.run(600)
    self.assertEqual(mc.unloaded, [], "the GPU full of your own model is the point")

  def test_training_that_needs_vram_gets_the_idle_model_out(self):
    procs = [{"pid": 900, "mem": 6 * GB, "type": "C", "sm": 0}, {"pid": 777, "mem": 4 * GB, "type": "C", "sm": 90}]

    def grow(mc, t):
      procs[1]["mem"] += 100 * MB
      mc.vram = self.vram(1200 - int(t) * 10, procs)

    mc = Machine(self.m, vram=self.vram(1200, procs), script=grow)
    mc.run(20)
    self.assertEqual(mc.unloaded, ["qwen2.5-coder:7b"])

  def test_model_answering_a_request_is_never_unloaded(self):
    procs = [{"pid": 900, "mem": 6 * GB, "type": "C", "sm": 80}, {"pid": 777, "mem": 4 * GB, "type": "C", "sm": 90}]

    def grow(mc, t):
      procs[1]["mem"] += 100 * MB
      mc.vram = self.vram(1000, procs)

    mc = Machine(self.m, vram=self.vram(1000, procs), script=grow)
    mc.ollama_sm = 80
    mc.run(30)
    self.assertEqual(mc.unloaded, [])

  def test_gpu_almost_full_warns_once(self):
    mc = Machine(self.m, vram=self.vram(300, [{"pid": 777, "mem": 11 * GB, "type": "C", "sm": 99}]))
    mc.run(300)
    self.assertEqual(sum(1 for s, _ in mc.notes if "almost full" in s), 1)


class Dictation(Base):
  """voxtype resident by default; moved off the GPU only when someone needs the room."""

  def vram(self, free_mb, procs):
    total = 12288 * MB
    return {"total": total, "free": free_mb * MB, "used": total - free_mb * MB, "procs": procs}

  def competing(self, start_free=1200, grow_until=40):
    procs = [{"pid": 777, "mem": 4 * GB, "type": "C", "sm": 90}, {"pid": 950, "mem": 1552 * MB, "type": "C", "sm": 0}]

    def script(mc, t):
      if t <= grow_until:
        procs[0]["mem"] += 100 * MB
      free = start_free if t <= grow_until else 6000
      mc.vram = self.vram(free, procs)
    return procs, script

  def test_ollama_goes_first_then_dictation(self):
    procs, script = self.competing()
    mc = Machine(self.m, vram=self.vram(1200, procs), script=script)
    mc.run(30)
    self.assertEqual(mc.unloaded, ["qwen2.5-coder:7b"])
    self.assertEqual(mc.vox_calls, [True], "then dictation moves off the GPU")
    ev = mc.guard.events
    t_ollama = next(t for t, e in ev if "unloaded idle Ollama" in e)
    t_vox = next(t for t, e in ev if "dictation model moved" in e)
    self.assertLess(t_ollama, t_vox, "Ollama first; dictation only if that was not enough")

  def test_never_while_dictating(self):
    procs, script = self.competing()
    mc = Machine(self.m, vram=self.vram(1200, procs), script=script)
    mc.loaded_models = []
    mc.vox_idle = False
    mc.run(30)
    self.assertEqual(mc.vox_calls, [])

  def test_full_gpu_of_your_own_work_does_not_touch_dictation(self):
    procs = [{"pid": 777, "mem": 9 * GB, "type": "C", "sm": 99}, {"pid": 950, "mem": 1552 * MB, "type": "C", "sm": 0}]
    mc = Machine(self.m, vram=self.vram(800, procs))
    mc.run(300)
    self.assertEqual(mc.vox_calls, [], "nobody is asking for more: leave it instant")

  def test_dictating_is_not_competition(self):
    procs = [{"pid": 950, "mem": 1552 * MB, "type": "C", "sm": 80}, {"pid": 900, "mem": 9 * GB, "type": "C", "sm": 0}]

    def transcribing(mc, t):
      procs[0]["mem"] += 300 * MB if t < 10 else 0  # whisper's buffers while transcribing
      mc.vram = self.vram(900, procs)

    mc = Machine(self.m, vram=self.vram(900, procs), script=transcribing)
    mc.run(20)
    self.assertEqual((mc.unloaded, mc.vox_calls), ([], []))

  def test_back_on_the_gpu_after_five_free_minutes(self):
    procs, script = self.competing(grow_until=40)
    mc = Machine(self.m, vram=self.vram(1200, procs), script=script)
    mc.run(40 + 4 * 60)
    self.assertEqual(mc.vox_calls, [True], "4 min free: not yet")
    mc2 = Machine(self.m, vram=self.vram(1200, procs), script=script)
    procs[0]["mem"] = 4 * GB
    mc2.vox_resident = 0
    mc2.run(40 + 6 * 60)
    self.assertIn(False, mc2.vox_calls, "reloaded (the offload survived a Guard restart)")

  def test_not_reloaded_while_dictating(self):
    procs, script = self.competing(grow_until=40)
    mc = Machine(self.m, vram=self.vram(1200, procs), script=script)
    mc.run(40)
    mc.vox_idle = False
    mc.run(10 * 60)
    self.assertNotIn(False, mc.vox_calls)

  def test_can_be_switched_off(self):
    procs, script = self.competing()
    mc = Machine(self.m, vram=self.vram(1200, procs), script=script)
    mc.loaded_models = []
    mc.run(30, cfg={"voxtypeOffload": 0})
    self.assertEqual(mc.vox_calls, [])


class Classify(Base):
  def test_kinds(self):
    c = self.m.classify
    self.assertEqual(c("python3", "/home/u/ai/.venv/bin/python train.py --model unsloth/x"), "ai")
    self.assertEqual(c("python3", "python3 backup_photos.py"), "dev", "any script: work until proven otherwise")
    self.assertEqual(c("python3", "python -m torch.distributed.run train.py"), "ai")
    self.assertEqual(c("python3", "/home/u/ai/.venv/bin/python -m ipykernel_launcher -f k.json"), "ai")
    self.assertEqual(c("pt_main_thread", "python train.py"), "ai")
    self.assertEqual(c("python3", "python -m torchrun --nproc 1 x.py"), "ai")
    self.assertEqual(c("pt_data_worker", "python train.py"), "ai")
    self.assertEqual(c("myapp", "/opt/x/myapp", cuda=True), "ai")
    self.assertEqual(c("chrome", "/opt/google/chrome/chrome --type=gpu-process", cuda=True), None)
    self.assertEqual(c("ollama", "/usr/bin/ollama runner --model x"), "ai")
    self.assertEqual(c("voxtype", "/usr/bin/voxtype daemon"), "ai", "dictation is AI work, also without CUDA")
    self.assertEqual(c("dockerd", "/usr/bin/dockerd"), "ai")
    self.assertEqual(c("claude", "claude --resume"), "dev")
    self.assertEqual(c("java", "java -jar jdtls.jar"), "dev")
    self.assertEqual(c("Hyprland", "Hyprland"), "desktop")
    self.assertEqual(c("rclone", "rclone mount rclone: ~/GoogleDrive"), "desktop")
    self.assertEqual(c("slack", "/usr/lib/slack/slack"), None)
    self.assertEqual(c("chrome", "/opt/google/chrome/chrome"), None)


class Pressure(Base):
  """Pressure is what the innocent feel, not a process thrashing inside its own cap."""

  def scope(self, name, some, current=1 * GB, limit=float("inf")):
    s = FakeScope(name, None, current)
    s.pressure, s.limit = (some, some / 2), limit
    return s

  def test_capped_runaway_does_not_count(self):
    runaway = self.scope("tail.scope", 80, current=5 * GB, limit=5 * GB)
    calm = self.scope("slack.scope", 1)
    self.assertEqual(self.m.effective_psi([runaway, calm], extra=[(0.5, 0)])[0], 1)

  def test_the_scope_memguard_capped_does_not_count(self):
    capped = self.scope("chrome.scope", 90)
    self.assertEqual(self.m.effective_psi([capped], throttled={"chrome.scope"}, extra=[(2, 0)])[0], 2)

  def test_a_victim_counts(self):
    victim = self.scope("ide.scope", 35)
    self.assertEqual(self.m.effective_psi([victim], extra=[(0, 0)]), (35, 17.5))

  def test_desktop_or_system_services_count(self):
    self.assertEqual(self.m.effective_psi([], extra=[(40, 20), (3, 0)]), (40, 20))

  def test_parse(self):
    self.assertEqual(self.m.parse_psi("some avg10=12.50 avg60=1 avg300=0 total=9\nfull avg10=3.00 avg60=0 avg300=0 total=1"),
                     (12.5, 3.0))


class Config(Base):
  def test_bad_config_falls_back_to_defaults(self):
    for content in ["{bad", "[1]", "null", json.dumps({"psiCrit": "x", "every": 0, "ramWarnAvailPct": -1,
                                                       "tteCrit": True, "nope": 3, "vramWarnFreeMB": None})]:
      self.m.CONFIG.write_text(content)
      cfg = self.m.config()
      for k, v in self.m.DEFAULTS.items():
        self.assertEqual(cfg[k], float(v), f"{content}: {k}")
      self.m.Guard(cfg)

  def test_valid_values_apply(self):
    self.m.CONFIG.write_text(json.dumps({"psiCrit": 40, "maxFreezeMin": 0}))
    cfg = self.m.config()
    self.assertEqual((cfg["psiCrit"], cfg["maxFreezeMin"]), (40.0, 0.0))


class Recovery(Base):
  """A daemon that dies must not leave work paused or capped."""

  def test_leftovers_of_a_crashed_run_are_undone_at_start(self):
    m = self.m
    m.STATE.mkdir(parents=True)
    capped = Path(tempfile.mkdtemp())
    (m.STATE / "applied.json").write_text(json.dumps({"frozen": ["gphotos-sync.service"],
                                                      "throttled": {"chrome.scope": [str(capped), "max"]}}))
    thawed, highs = [], {}
    m.thaw_unit = lambda u: thawed.append(u) or True
    m.set_memory_high = lambda p, v: highs.__setitem__(str(p), v) or True
    done = m.undo_leftovers()
    self.assertEqual(thawed, ["gphotos-sync.service"])
    self.assertEqual(highs, {str(capped): "max"})
    self.assertEqual(len(done), 2)
    left = json.loads((m.STATE / "applied.json").read_text())
    self.assertEqual((left["frozen"], left["throttled"]), ([], {}))

  def test_applied_state_is_written_while_acting(self):
    mc = Machine(self.m, scopes=ai_box(), script=Spikes.runaway_chrome.__get__(Spikes()))
    mc.run(40)
    data = json.loads((self.m.STATE / "applied.json").read_text())
    self.assertEqual(data["frozen"], ["gphotos-sync.service"])
    self.assertIn("chrome.scope", data["throttled"])

  def test_daemon_survives_bad_samples_and_undoes_on_stop(self):
    m = self.m
    calls = {"n": 0}
    undone = []

    def sleep(s):
      calls["n"] += 1
      if calls["n"] > 5:
        raise SystemExit(0)  # what SIGTERM does

    m.time = types.SimpleNamespace(sleep=sleep, monotonic=lambda: calls["n"] * 2.0, time=lambda: 0)
    m.open_nvml = lambda: None
    m.meminfo = lambda: (_ for _ in ()).throw(OSError("proc gone"))  # every sample fails
    m.undo_leftovers = lambda: []
    m.signal = types.SimpleNamespace(signal=lambda *a: None, SIGTERM=15)
    orig = m.Guard.undo_all
    m.Guard.undo_all = lambda self, t, why: undone.append(why)
    try:
      with self.assertRaises(SystemExit):
        m.daemon()
    finally:
      m.Guard.undo_all = orig
    self.assertEqual(calls["n"], 6, "kept looping through failing samples")
    self.assertEqual(undone, ["memguard stopped"])


class Exceptions(Base):
  """protect / ordinary / freezable, as the widget writes them."""

  def scope_dir(self, name, procs):
    """A fake app.slice unit with processes [(pid, comm, cmdline)]."""
    root = Path(tempfile.mkdtemp())
    m = self.m
    m.user_cgroup = lambda: root
    d = root / "app.slice" / name
    d.mkdir(parents=True)
    (d / "cgroup.procs").write_text("\n".join(str(p) for p, _, _ in procs))
    (d / "memory.current").write_text(str(GB))
    table = {p: (c, cl) for p, c, cl in procs}
    m.comm_of = lambda pid: table.get(pid, ("", ""))[0]
    m.cmdline_of = lambda pid: table.get(pid, ("", ""))[1]
    return d

  def kind(self, cfg, name="app-slack.scope", procs=((10, "slack", "/usr/lib/slack/slack"),)):
    self.scope_dir(name, procs)
    return self.m.app_scopes((), cfg)[0]

  def test_protect_by_process_unit_or_app_name(self):
    for entry in ("slack", "app-slack.scope", "app-slack"):
      s = self.kind({"protect": [entry]})
      self.assertEqual((s.kind, s.why), ("user", "your exception"), entry)

  def test_ordinary_removes_builtin_protection(self):
    s = self.kind({"ordinary": ["java"]}, "app-ide.scope", ((5, "java", "java -jar big.jar"),))
    self.assertIsNone(s.kind)
    self.assertIn("ordinary", s.why)

  def test_the_desktop_cannot_be_made_ordinary(self):
    s = self.kind({"ordinary": ["Hyprland"]}, "hypr.scope", ((1, "Hyprland", "Hyprland"),))
    self.assertEqual(s.kind, "desktop")

  def test_extra_freezable_job_is_paused_and_not_protected(self):
    s = self.kind({"freezable": ["my-sync.service"]}, "my-sync.service", ((7, "python3", "python3 sync.py"),))
    self.assertIsNone(s.kind)
    mc = Machine(self.m, scopes=ai_box(), script=lambda mc, t: setattr(mc, "psi_some", 30),
                 units_active=("my-sync.service",))
    mc.run(10, cfg={"freezable": ["my-sync.service"]})
    self.assertIn("my-sync.service", mc.frozen)


class Settings(Base):
  """What the widget calls: validated edits, live reload, the on/off switch."""

  def edit(self, *a):
    return self.m.edit_config(*a)

  def test_set_validates_range_and_type(self):
    self.assertEqual(self.edit("set", "psiCrit", "40"), (True, ""))
    self.assertEqual(self.m.config()["psiCrit"], 40.0)
    self.assertFalse(self.edit("set", "psiCrit", "500")[0])
    self.assertFalse(self.edit("set", "psiCrit", "abc")[0])
    self.assertFalse(self.edit("set", "nope", "1")[0])

  def test_setting_the_default_removes_the_key(self):
    self.edit("set", "psiCrit", "40")
    self.edit("set", "psiCrit", str(self.m.DEFAULTS["psiCrit"]))
    self.assertNotIn("psiCrit", json.loads(self.m.CONFIG.read_text()))

  def test_lists_add_remove_and_one_rule_per_name(self):
    self.assertTrue(self.edit("add", "protect", "slack")[0])
    self.assertTrue(self.edit("add", "protect", "slack")[0])
    self.assertEqual(self.m.config()["protect"], ["slack"], "no duplicates")
    self.edit("add", "ordinary", "slack")
    cfg = self.m.config()
    self.assertEqual((cfg["protect"], cfg["ordinary"]), ([], ["slack"]))
    self.edit("remove", "ordinary", "slack")
    self.assertNotIn("ordinary", json.loads(self.m.CONFIG.read_text()))

  def test_names_are_checked(self):
    for bad in ("", "a b", "x;rm -rf ~", "$(id)", "a" * 65, "../etc"):
      self.assertFalse(self.edit("add", "protect", bad)[0], bad)
    self.assertFalse(self.edit("add", "freezable", "rclone-gdrive.service")[0], "mounts are never paused")
    self.assertFalse(self.edit("add", "bogus", "x")[0])

  def test_reset_keeps_exceptions(self):
    self.edit("set", "psiCrit", "40")
    self.edit("add", "protect", "slack")
    self.edit("reset")
    cfg = self.m.config()
    self.assertEqual((cfg["psiCrit"], cfg["protect"]), (float(self.m.DEFAULTS["psiCrit"]), ["slack"]))

  def test_every_setting_is_on_the_settings_screen(self):
    self.assertEqual(set(self.m.SCHEMA), set(self.m.DEFAULTS))
    for k, (g, lab, unit, lo, hi, step) in self.m.SCHEMA.items():
      self.assertTrue(lo <= self.m.DEFAULTS[k] <= hi, k)

  def test_switched_off_undoes_and_never_acts(self):
    mc = Machine(self.m, scopes=ai_box(), script=Spikes.runaway_chrome.__get__(Spikes()))
    mc.run(40)
    self.assertTrue(mc.frozen and mc.highs)
    mc.guard.cfg = dict(mc.guard.cfg, enabled=0)
    mc.guard.tick(100, mc.mi(), (60, 40), None, mc.scopes)
    self.assertEqual(mc.thawed, ["gphotos-sync.service"])
    self.assertEqual(mc.highs.get("chrome.scope"), "max")
    self.assertFalse(mc.guard.status["enabled"])
    mc2 = Machine(self.m, scopes=ai_box(), script=Spikes.runaway_chrome.__get__(Spikes()))
    mc2.run(40, cfg={"enabled": 0})
    self.assertEqual((mc2.frozen, mc2.highs, mc2.notes), ([], {}, []))

  def test_daemon_reloads_settings_without_restarting(self):
    m = self.m
    seen = []
    n = {"i": 0}

    def sleep(s):
      n["i"] += 1
      if n["i"] == 2:
        m.edit_config("set", "psiCrit", "60")
        os.utime(m.CONFIG, ns=(1, 10 ** 18))  # mtime changes even within the same tick
      if n["i"] > 3:
        raise SystemExit(0)

    m.time = types.SimpleNamespace(sleep=sleep, monotonic=lambda: n["i"] * 2.0, time=lambda: 0)
    m.open_nvml = lambda: None
    m.undo_leftovers = lambda: []
    m.signal = types.SimpleNamespace(signal=lambda *a: None, SIGTERM=15)
    m.meminfo = lambda: {"MemTotal": 30 * GB, "MemAvailable": 20 * GB, "SwapTotal": 0, "SwapFree": 0}
    m.app_scopes = lambda cuda, cfg=None: []
    m.effective_psi = lambda scopes, throttled=(): (0.0, 0.0)
    orig = m.Guard.tick
    m.Guard.tick = lambda self, t, *a: seen.append(self.cfg["psiCrit"]) or 0
    try:
      with self.assertRaises(SystemExit):
        m.daemon()
    finally:
      m.Guard.tick = orig
    self.assertEqual(seen[0], float(m.DEFAULTS["psiCrit"]))
    self.assertEqual(seen[-1], 60.0)

  def test_ui_state_shape(self):
    m = self.m
    m.app_scopes = lambda cuda, cfg=None: [FakeScope("app-slack.scope", None, GB)]
    m.label = lambda s: s.name
    m.run = lambda cmd, timeout=10: (0, "active")
    m.open_nvml = lambda: None
    m.STATE.mkdir(parents=True)
    (m.STATE / "status.json").write_text(json.dumps({"t": 1, "level": 0, "throttled": []}))
    (m.STATE / "events.log").write_text("2026-10-08 11:15:42 slowed x\n")
    st = m.ui_state()
    self.assertEqual(st["service"], "active")
    self.assertEqual(st["apps"][0]["unit"], "app-slack.scope")
    self.assertEqual(st["events"][0], {"when": "2026-10-08 11:15:42", "text": "slowed x"})
    self.assertEqual({r["key"] for r in st["config"]["schema"]}, set(m.DEFAULTS))
    json.dumps(st)  # the widget parses it


class Unit(unittest.TestCase):
  def test_unit_restarts_always_and_starts_with_the_session(self):
    u = UNIT.read_text()
    for line in ("Restart=always", "StartLimitIntervalSec=0", "WantedBy=graphical-session.target",
                 "ExecStart=%h/.local/bin/omarchy-memguard daemon"):
      self.assertIn(line, u)

  def test_installer_wires_it(self):
    sh = INSTALL.read_text()
    self.assertRegex(sh, r"COLLECTORS=\([^)]*omarchy-memguard")
    self.assertIn("systemctl --user enable omarchy-memguard.service", sh)
    self.assertIn("omarchy-memguard.service", sh.split("--remove")[1] if "--remove" in sh else sh)


if __name__ == "__main__":
  unittest.main()
