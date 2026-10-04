"""Per-check FAIL/PASS tests for smoke-test.sh and apply.sh, with stub binaries on PATH."""
import os
import subprocess
import tempfile
import unittest

MAINT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SMOKE = os.path.join(MAINT, "smoke-test.sh")
APPLY = os.path.join(MAINT, "apply.sh")

SYSTEMCTL = r'''#!/bin/sh
case "$1" in
  is-enabled) echo "${STUB_EN:-enabled}" ;;
  is-active) echo "${STUB_ACT:-active}" ;;
  --failed) [ -n "$STUB_FAILED" ] && echo "$STUB_FAILED" ;;
  --user) ;;
esac
exit 0
'''
PACMAN = r'''#!/bin/sh
case "$1" in
  -Qdtq) [ -n "$STUB_ORPHANS" ] && printf '%s\n' $STUB_ORPHANS; exit 0 ;;
  -Q) shift; for p in "$@"; do case " $STUB_MISSING " in *" $p "*) exit 1;; esac; done; exit 0 ;;
esac
'''
JOURNALCTL = '#!/bin/sh\n[ -n "$STUB_JOURNAL" ] && cat "$STUB_JOURNAL"\nexit 0\n'
VAINFO = '#!/bin/sh\n[ -n "$STUB_VAINFO" ] && cat "$STUB_VAINFO"\nexit 0\n'
HYPRCTL = '#!/bin/sh\n[ -n "$STUB_HYPR" ] && echo "$STUB_HYPR"\nexit ${STUB_HYPR_RC:-0}\n'
PACCACHE = '#!/bin/sh\necho "${STUB_PACCACHE:-==> no candidate packages found for pruning}"\n'


def put(path, text, mode=None):
    with open(path, "w") as f:
        f.write(text)
    if mode:
        os.chmod(path, mode)


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.t = self.tmp.name
        self.bin = os.path.join(self.t, "bin")
        os.makedirs(self.bin)
        for n, b in (("systemctl", SYSTEMCTL), ("pacman", PACMAN), ("journalctl", JOURNALCTL),
                     ("vainfo", VAINFO), ("hyprctl", HYPRCTL), ("paccache", PACCACHE)):
            put(os.path.join(self.bin, n), b, 0o755)

    def tearDown(self):
        self.tmp.cleanup()

    def env(self, **extra):
        e = {k: v for k, v in os.environ.items() if not k.startswith("STUB_") and k != "HYPRLAND_INSTANCE_SIGNATURE"}
        e["PATH"] = self.bin + ":" + os.environ["PATH"]
        e.update(extra)
        return e

    def smoke(self, check, **extra):
        p = subprocess.run(["bash", SMOKE, "--only", check], capture_output=True, text=True, env=self.env(**extra))
        return p.stdout.split(" ", 1)[0], p


class SmokeStubTest(Base):
    def test_timers(self):
        for cid in ("TRIM02", "PACCACHE01", "SCRUB01"):
            self.assertEqual(self.smoke(cid)[0], "PASS", cid)
            self.assertEqual(self.smoke(cid, STUB_EN="disabled")[0], "FAIL", cid)
            self.assertEqual(self.smoke(cid, STUB_ACT="inactive")[0], "FAIL", cid)

    def test_smart01(self):
        self.assertEqual(self.smoke("SMART01")[0], "PASS")
        self.assertEqual(self.smoke("SMART01", STUB_ACT="failed")[0], "FAIL")

    def journal(self, first, filler_kb):
        path = os.path.join(self.t, "journal")
        put(path, first + "\n" + ("Device: /dev/nvme0, ok line padding padding padding\n" * (filler_kb * 20)))
        return path

    def test_smart03_error_in_big_journal(self):
        path = self.journal("smartd: Unable to parse configuration line 1", 200)
        self.assertGreater(os.path.getsize(path), 65536 * 2)
        self.assertEqual(self.smoke("SMART03", STUB_JOURNAL=path)[0], "FAIL")

    def test_smart03_clean_big_and_empty(self):
        path = self.journal("smartd: started", 200)
        self.assertEqual(self.smoke("SMART03", STUB_JOURNAL=path)[0], "PASS")
        self.assertEqual(self.smoke("SMART03")[0], "SKIP")

    def test_paccache02(self):
        self.assertEqual(self.smoke("PACCACHE02")[0], "PASS")
        self.assertEqual(self.smoke("PACCACHE02", STUB_PACCACHE="==> finished: 3 candidates")[0], "WARN")

    def test_orphan01(self):
        self.assertEqual(self.smoke("ORPHAN01")[0], "PASS")
        st, p = self.smoke("ORPHAN01", STUB_ORPHANS="foo bar")
        self.assertEqual(st, "FAIL")
        self.assertIn("foo bar", p.stdout)

    def test_pkg01(self):
        self.assertEqual(self.smoke("PKG01")[0], "PASS")
        st, p = self.smoke("PKG01", STUB_MISSING="nvme-cli")
        self.assertEqual(st, "FAIL")
        self.assertIn("nvme-cli", p.stdout)

    def test_pkg02(self):
        ok = os.path.join(self.t, "va_ok")
        put(ok, "vainfo: Driver version: VA-API NVDEC driver\n" + "x" * 100 * 1024 + "\nVAProfileH264Main : VAEntrypointVLD\n")
        self.assertEqual(self.smoke("PKG02", STUB_VAINFO=ok)[0], "PASS")  # >64 KB: no SIGPIPE flip
        bad = os.path.join(self.t, "va_bad")
        put(bad, "vainfo: Driver version: Mesa Gallium\nVAProfileH264Main : VAEntrypointVLD\n")
        self.assertEqual(self.smoke("PKG02", STUB_VAINFO=bad)[0], "WARN")

    def test_svc01(self):
        self.assertEqual(self.smoke("SVC01")[0], "PASS")
        st, p = self.smoke("SVC01", STUB_FAILED="broken.service loaded failed failed Broken")
        self.assertEqual(st, "FAIL")
        self.assertIn("broken.service", p.stdout)

    def test_hypr01(self):
        sig = {"HYPRLAND_INSTANCE_SIGNATURE": "x"}
        self.assertEqual(self.smoke("HYPR01")[0], "SKIP")
        self.assertEqual(self.smoke("HYPR01", **sig)[0], "PASS")
        self.assertEqual(self.smoke("HYPR01", STUB_HYPR="config error line 3", **sig)[0], "FAIL")
        self.assertEqual(self.smoke("HYPR01", STUB_HYPR_RC="1", **sig)[0], "WARN")


class ApplyTest(Base):
    def setUp(self):
        super().setUp()
        self.state = os.path.join(self.t, "state")
        os.makedirs(self.state)
        self.sudolog = os.path.join(self.t, "sudo.log")
        put(os.path.join(self.bin, "sudo"), '#!/bin/sh\necho "$@" >> %s\nexit 1\n' % self.sudolog, 0o755)
        self.sysfs = os.path.join(self.t, "sys")
        d = os.path.join(self.sysfs, "block", "dm-0")
        os.makedirs(os.path.join(d, "dm"))
        os.makedirs(os.path.join(d, "queue"))
        put(os.path.join(d, "dm", "name"), "root\n")
        put(os.path.join(d, "queue", "discard_max_bytes"), "0\n")

    def apply(self, *args, **extra):
        return subprocess.run(["bash", APPLY, *args], capture_output=True, text=True,
                              env=self.env(OMARCHY_MAINT_STATE=self.state, SYSFS_ROOT=self.sysfs, **extra))

    def test_only_without_argument(self):
        p = self.apply("--only")
        self.assertEqual(p.returncode, 3)
        self.assertIn("uso:", p.stderr)

    def test_dry_run_never_uses_sudo_or_writes(self):
        p = self.apply("--dry-run", "--only", "S0,1,3", STUB_EN="disabled", STUB_ACT="inactive")
        self.assertEqual(p.returncode, 0, p.stderr)
        for s in ("S0", "1", "3"):
            self.assertIn("STEP %s would-do" % s, p.stdout)
        self.assertIn("-c number", p.stdout)
        self.assertFalse(os.path.exists(self.sudolog))
        self.assertEqual(os.listdir(self.state), [])

    def test_snapshots_fresh_only_when_work_needed(self):
        put(os.path.join(self.state, "pre-number"), "22\n")
        put(os.path.join(self.state, "post-number"), "23\n")
        p = self.apply("--dry-run", "--only", "S0,S1")  # nothing else selected
        self.assertIn("STEP S0 skip", p.stdout)
        self.assertIn("STEP S1 skip", p.stdout)
        p = self.apply("--dry-run", "--only", "S0,3,S1", STUB_EN="disabled")
        self.assertIn("STEP S0 would-do", p.stdout)
        self.assertIn("STEP S1 would-do", p.stdout)
        self.assertIn("STEP 3 would-do", p.stdout)

class SmokeDriversGroupTest(Base):
    """DRIVERS01/02 delegate to the drivers module; a fake DRIVERS_DIR stands in for it."""

    def fake_drivers(self, check_rc, compat_rc, compat_line="FAIL C13c nvidia-smi no responde"):
        self.n = getattr(self, "n", 0) + 1
        d = os.path.join(self.t, "drivers%d" % self.n)
        os.makedirs(d)
        put(os.path.join(d, "install.sh"), "#!/bin/sh\n[ %d -ne 0 ] && echo 'FALTA paquete (repo): foo'\nexit %d\n" % (check_rc, check_rc), 0o755)
        put(os.path.join(d, "compat.py"), "import sys\nprint(%r)\nsys.exit(%d)\n" % (compat_line, compat_rc))
        return d

    def test_drivers01(self):
        self.assertEqual(self.smoke("DRIVERS01", DRIVERS_DIR=self.fake_drivers(0, 0))[0], "PASS")
        st, p = self.smoke("DRIVERS01", DRIVERS_DIR=self.fake_drivers(1, 0))
        self.assertEqual(st, "FAIL")
        self.assertIn("FALTA paquete (repo): foo", p.stdout)

    def test_drivers02(self):
        self.assertEqual(self.smoke("DRIVERS02", DRIVERS_DIR=self.fake_drivers(0, 0))[0], "PASS")
        self.assertEqual(self.smoke("DRIVERS02", DRIVERS_DIR=self.fake_drivers(0, 1, "WARN C14 x"))[0], "WARN")
        st, p = self.smoke("DRIVERS02", DRIVERS_DIR=self.fake_drivers(0, 2))
        self.assertEqual(st, "FAIL")
        self.assertIn("nvidia-smi", p.stdout)


if __name__ == "__main__":
    unittest.main()
