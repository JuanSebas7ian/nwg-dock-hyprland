"""One OK and one breaking scenario per compat.py rule, plus the CLI with stub binaries."""
import os
import sys
import tempfile
import time
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from helpers import DRV, Sandbox, put  # noqa: E402

sys.path.insert(0, DRV)
import compat  # noqa: E402

BASE = {
    "linux": "7.2.3.arch1-3", "linux-headers": "7.2.3.arch1-3", "amd-ucode": "20260810-2",
    "nvidia-open-dkms": "610.57.04-1", "nvidia-utils": "610.57.04-1", "lib32-nvidia-utils": "610.57.04-1",
    "opencl-nvidia": "610.57.04-1", "cuda": "13.3.1-1", "cudnn": "9.25.1.1-1", "ollama-cuda": "0.33.3-1",
    "mesa": "1:26.2.2-1", "lib32-mesa": "1:26.2.2-1", "vulkan-radeon": "1:26.2.2-1", "lib32-vulkan-radeon": "1:26.2.2-1",
    "vulkan-icd-loader": "1.4.357.0-1", "lib32-vulkan-icd-loader": "1.4.357.0-1",
    "gamemode": "1.8.2-3", "lib32-gamemode": "1.8.2-1", "mangohud": "0.8.4-1", "lib32-mangohud": "0.8.4-1",
    "xpadneo-dkms": "0.10.4-1.1", "hyprland": "0.56.2-1", "quickshell": "0.3-1", "omarchy": "4.0.4-1",
}


def ctx(pending=None, boot=649, root=292.0):
    c = compat.Ctx(BASE, pending)
    c.boot_free_mb, c.root_free_gb = boot, root
    return c


def level(results, rid):
    return [l for l, i, _ in results if i == rid]


class ElfDirs(unittest.TestCase):
    def test_lib32_scanned(self):
        self.assertIn("/usr/lib32/", compat.ELF_DIRS)


class VersionHelpers(unittest.TestCase):
    def test_helpers(self):
        self.assertEqual(compat.upstream("1:26.2.2-1"), "1:26.2.2")
        self.assertEqual(compat.major_minor("7.2.3.arch1-3"), (7, 2))
        self.assertEqual(compat.major("610.57.04-1"), 610)
        self.assertEqual(compat.parse_updates("a 1-1 -> 2-1\nb 1:3-1 -> 1:4-1\n"), {"a": "2-1", "b": "1:4-1"})


class FamilyRules(unittest.TestCase):
    def test_all_aligned_ok(self):
        r = compat.rule_families(ctx())
        self.assertTrue(r and all(l == compat.OK for l, _, _ in r))

    def test_c01_nvidia_fail(self):
        r = compat.rule_families(ctx({"nvidia-utils": "611.1-1"}))
        self.assertEqual(level(r, "C01"), [compat.FAIL])
        r = compat.rule_families(ctx({k: "611.1-1" for k in ("nvidia-utils", "lib32-nvidia-utils", "opencl-nvidia", "nvidia-open-dkms")}))
        self.assertEqual(level(r, "C01"), [compat.OK])

    def test_c02_kernel_headers_fail(self):
        self.assertEqual(level(compat.rule_families(ctx({"linux": "7.2.4.arch1-1"})), "C02"), [compat.FAIL])
        self.assertEqual(level(compat.rule_families(ctx({"linux": "7.2.4.arch1-1", "linux-headers": "7.2.4.arch1-1"})), "C02"), [compat.OK])

    def test_c07_mesa_fail(self):
        self.assertEqual(level(compat.rule_families(ctx({"lib32-mesa": "1:26.3.0-1"})), "C07"), [compat.FAIL])

    def test_c08_warn(self):
        r = compat.rule_families(ctx({"lib32-vulkan-icd-loader": "1.5-1", "mangohud": "0.9-1"}))
        self.assertEqual(level(r, "C08a"), [compat.WARN])
        self.assertEqual(level(r, "C08c"), [compat.WARN])
        self.assertEqual(level(r, "C08b"), [compat.OK])

    def test_pkgrel_difference_is_fine(self):
        self.assertEqual(level(compat.rule_families(ctx({"gamemode": "1.8.2-9"})), "C08b"), [compat.OK])

    def test_missing_member_skips(self):
        inst = {k: v for k, v in BASE.items() if k != "lib32-mangohud"}
        self.assertEqual(level(compat.rule_families(compat.Ctx(inst)), "C08c"), [])


class KernelRules(unittest.TestCase):
    def test_c03(self):
        self.assertEqual(level(compat.rule_c03(ctx()), "C03"), [compat.OK])
        self.assertEqual(level(compat.rule_c03(ctx({"linux": "7.2.4.arch1-1"})), "C03"), [compat.OK])  # same 7.2
        self.assertEqual(level(compat.rule_c03(ctx({"linux": "7.3.1.arch1-1"})), "C03"), [compat.WARN])
        both = ctx({"linux": "7.3.1.arch1-1", "nvidia-open-dkms": "611.1-1"})
        self.assertEqual(level(compat.rule_c03(both), "C03"), [compat.OK])

    def test_c04(self):
        self.assertEqual(level(compat.rule_c04(ctx()), "C04"), [compat.OK])
        r = compat.rule_c04(ctx({"linux": "7.3.1.arch1-1"}))
        self.assertEqual(level(r, "C04"), [compat.WARN])
        self.assertIn("xpadneo-dkms", r[0][2])

    def test_c09(self):
        self.assertEqual(level(compat.rule_c09(ctx()), "C09"), [compat.OK])
        up = {"linux": "7.2.4.arch1-1"}
        self.assertEqual(level(compat.rule_c09(ctx(up, boot=649)), "C09"), [compat.OK])
        self.assertEqual(level(compat.rule_c09(ctx(up, boot=150)), "C09"), [compat.FAIL])
        self.assertEqual(level(compat.rule_c09(ctx({"linux-firmware-nvidia": "2-1"}, boot=150)), "C09"), [compat.FAIL])
        self.assertEqual(level(compat.rule_c09(ctx({"vim": "9-1"}, boot=10)), "C09"), [compat.OK])
        for pkg in ("nvidia-open-dkms", "nvidia-utils", "mkinitcpio", "limine", "limine-snapper-sync", "amd-ucode"):
            self.assertEqual(level(compat.rule_c09(ctx({pkg: "99-1"}, boot=150)), "C09"), [compat.FAIL], pkg)

    def test_c10(self):
        self.assertEqual(level(compat.rule_c10(ctx()), "C10"), [compat.OK])
        self.assertEqual(level(compat.rule_c10(ctx({"vim": "9-1"}, root=50)), "C10"), [compat.OK])
        self.assertEqual(level(compat.rule_c10(ctx({"vim": "9-1"}, root=2.5)), "C10"), [compat.WARN])

    def test_c11(self):
        self.assertEqual(level(compat.rule_c11(ctx({"vim": "9-1"})), "C11"), [compat.OK])
        for pkg in ("hyprland", "quickshell", "omarchy", "omarchy-shell"):
            self.assertEqual(level(compat.rule_c11(ctx({pkg: "99-1"})), "C11"), [compat.WARN], pkg)
        self.assertEqual(level(compat.rule_c11(ctx({"omarchy-nvim": "99-1"})), "C11"), [compat.OK])  # not a core package


class CudaRules(unittest.TestCase):
    def test_c05(self):
        self.assertEqual(level(compat.rule_c05(ctx()), "C05"), [compat.OK])
        self.assertEqual(level(compat.rule_c05(ctx({"nvidia-utils": "570.1-1"})), "C05"), [compat.FAIL])
        self.assertEqual(level(compat.rule_c05(ctx({"cuda": "12.9.1-1", "nvidia-utils": "550.1-1"})), "C05"), [compat.OK])
        self.assertEqual(level(compat.rule_c05(ctx({"cuda": "12.9.1-1", "nvidia-utils": "500.1-1"})), "C05"), [compat.FAIL])
        self.assertEqual(level(compat.rule_c05(ctx({"cuda": "14.0-1"})), "C05"), [compat.WARN])  # unknown major

    def test_c06(self):
        self.assertEqual(level(compat.rule_c06(ctx({"cuda": "13.4.0-1"})), "C06"), [compat.OK])  # same major
        self.assertEqual(level(compat.rule_c06(ctx({"cuda": "14.0-1"})), "C06"), [compat.WARN])
        both = ctx({"cuda": "14.0-1", "cudnn": "10-1", "ollama-cuda": "0.34-1"})
        self.assertEqual(level(compat.rule_c06(both), "C06"), [compat.OK])


class PostRules(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.t = self.tmp.name

    def tearDown(self):
        self.tmp.cleanup()

    def good(self):
        put(os.path.join(self.t, "driver/nvidia/params"), "PreserveVideoMemoryAllocations: 1\nUseKernelSuspendNotifiers: 1\nOther: 0\n")
        put(os.path.join(self.t, "driver/nvidia/version"),
            "NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  610.57.04  Release Build  (root@x)\nGCC version: gcc\n")
        c = compat.Ctx({"nvidia-utils": "610.57.04-1", "ollama": "0.33.3-1"})
        c.proc_root = self.t
        c.kernel = "7.2.3-arch1-3"
        c.has_session = True

        def fake_run(cmd, timeout=15, env=None):
            key = " ".join(cmd)
            if key == "dkms status":
                return 0, "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: installed\nnvidia/609, 7.1.0-arch1-1, x86_64: built\n"
            if cmd[0] == "nvidia-smi":
                return 0, "610.57.04\n"
            if cmd[0] == "vulkaninfo":
                return 0, "GPU0: NVIDIA GeForce RTX 3060\nGPU1: AMD Radeon (RADV RAPHAEL_MENDOCINO)\n"
            if cmd[0] == "vainfo":
                return 0, "vainfo: Driver version: VA-API NVDEC driver\nVAProfileH264Main : VAEntrypointVLD\n"
            if cmd[0] == "hyprctl":
                return 0, ""
            if cmd[0] == "systemctl":
                return 0, ""
            return 127, ""
        c.run = fake_run
        c.cuinit = lambda: 0
        c.ollama_up = lambda: True
        return c

    def levels(self, c):
        return {i: l for l, i, _ in compat.rule_c13(c)}

    def test_all_good(self):
        lv = self.levels(self.good())
        self.assertTrue(all(v == compat.OK for v in lv.values()), lv)
        self.assertEqual(sorted(lv), ["C13a", "C13b", "C13c", "C13d", "C13e", "C13f", "C13g", "C13h", "C13i", "C13j"])

    def test_c13a_driver_mismatch_and_not_loaded(self):
        c = self.good()
        c.installed["nvidia-utils"] = "611.2-1"
        self.assertEqual(self.levels(c)["C13a"], compat.FAIL)
        os.remove(os.path.join(self.t, "driver/nvidia/version"))
        self.assertEqual(self.levels(c)["C13a"], compat.FAIL)

    def test_c13b_dkms_not_installed_for_running_kernel(self):
        c = self.good()
        base = c.run
        c.run = lambda cmd, timeout=15, env=None: (0, "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: built\n") if cmd[:2] == ["dkms", "status"] else base(cmd, timeout)
        self.assertEqual(self.levels(c)["C13b"], compat.FAIL)

    def broken(self, name, rc, out=""):
        c = self.good()
        base = c.run
        c.run = lambda cmd, timeout=15, env=None: (rc, out) if cmd[0] == name else base(cmd, timeout)
        return c

    def test_c13c_smi(self):
        self.assertEqual(self.levels(self.broken("nvidia-smi", 9))["C13c"], compat.FAIL)

    def test_c13d_cuinit(self):
        c = self.good()
        c.cuinit = lambda: 100
        self.assertEqual(self.levels(c)["C13d"], compat.FAIL)

    def test_c13e_vulkan(self):
        self.assertEqual(self.levels(self.broken("vulkaninfo", 0, "GPU0: AMD RADV only\n"))["C13e"], compat.FAIL)

    def test_c13f_vaapi(self):
        self.assertEqual(self.levels(self.broken("vainfo", 0, "vainfo: Mesa Gallium\nVAProfileH264Main\n"))["C13f"], compat.FAIL)

    def test_c13j_sleep_params(self):
        c = self.good()
        put(os.path.join(self.t, "driver/nvidia/params"), "PreserveVideoMemoryAllocations: 0\nUseKernelSuspendNotifiers: 1\n")
        self.assertEqual(self.levels(c)["C13j"], compat.FAIL)
        os.remove(os.path.join(self.t, "driver/nvidia/params"))
        self.assertEqual(self.levels(c)["C13j"], compat.FAIL)

    def test_c13f_sets_libva_driver(self):
        c = self.good()
        seen = {}
        base = c.run

        def spy(cmd, timeout=15, env=None):
            if cmd[0] == "vainfo":
                seen.update(env or {})
            return base(cmd, timeout)
        c.run = spy
        os.environ.pop("LIBVA_DRIVER_NAME", None)
        compat.rule_c13(c)
        self.assertEqual(seen.get("LIBVA_DRIVER_NAME"), "nvidia")

    def test_c13g_ollama(self):
        c = self.good()
        c.ollama_up = lambda: False
        self.assertEqual(self.levels(c)["C13g"], compat.FAIL)

    def test_c13h_hyprland(self):
        self.assertEqual(self.levels(self.broken("hyprctl", 0, "Config error in file x line 3\n"))["C13h"], compat.FAIL)
        c = self.good()
        c.has_session = False
        self.assertEqual(self.levels(c)["C13h"], compat.SKIP)

    def test_c13i_failed_units(self):
        self.assertEqual(self.levels(self.broken("systemctl", 0, "foo.service loaded failed failed Foo\n"))["C13i"], compat.FAIL)

    def test_c12_aur_libs(self):
        elf = os.path.join(self.t, "usr/bin/tool")
        put(elf, "\x7fELF fake")
        txt = os.path.join(self.t, "usr/bin/script")
        put(txt, "#!/bin/sh\n")
        old = compat.ELF_DIRS
        compat.ELF_DIRS = (self.t + "/usr/",)
        try:
            c = compat.Ctx({})
            c.aur = ["foo"]
            c.run = lambda cmd, timeout=15, env=None: (0, "foo pkg\n".replace("foo pkg", "foo %s\nfoo %s\n" % (elf, txt))) if cmd[0] == "pacman" else (0, "\tlibgone.so.1 => not found\n\tlibc.so.6 => /usr/lib/libc.so.6\n")
            r = compat.rule_c12(c)
            self.assertEqual(level(r, "C12"), [compat.FAIL])
            self.assertIn("libgone.so.1", r[0][2])
            c.run = lambda cmd, timeout=15, env=None: (0, "foo %s\n" % elf) if cmd[0] == "pacman" else (0, "\tlibc.so.6 => /usr/lib/libc.so.6\n")
            self.assertEqual(level(compat.rule_c12(c), "C12"), [compat.OK])
            self.assertEqual(level(compat.rule_c12(compat.Ctx({})), "C12"), [compat.OK])  # no AUR packages
            # A library the package itself ships (Zoom's /opt/zoom/libcef.so) is bundled, not missing.
            own = os.path.join(self.t, "usr/lib/foo/libcef.so")
            put(own, "x")
            c.run = lambda cmd, timeout=15, env=None: (0, "foo %s\nfoo %s\n" % (elf, own)) if cmd[0] == "pacman" else (0, "\tlibcef.so => not found\n")
            r = compat.rule_c12(c)
            self.assertEqual(level(r, "C12"), [compat.OK])
            self.assertIn("incluidas", r[0][2])
            # A symbol-version clash inside an app with its own runtime is a warning, not a failure.
            c.run = lambda cmd, timeout=15, env=None: (0, "foo %s\n" % elf) if cmd[0] == "pacman" else (0, "\t/opt/foo/lib/libQt6Core.so.6: version `Qt_6.11' not found (required by /usr/lib/libQt6LabsAnimation.so.6)\n")
            r = compat.rule_c12(c)
            self.assertEqual(level(r, "C12"), [compat.WARN])
            self.assertIn("Qt_6.11", r[0][2])
            # A gap listed in known-gaps.json is reported with its reason, as a warning.
            c.known_gaps = {"foo": {"libgone.so.1": "vendor slip"}}
            c.run = lambda cmd, timeout=15, env=None: (0, "foo %s\n" % elf) if cmd[0] == "pacman" else (0, "\tlibgone.so.1 => not found\n")
            r = compat.rule_c12(c)
            self.assertEqual(level(r, "C12"), [compat.WARN])
            self.assertIn("vendor slip", r[0][2])
            c.known_gaps = {"bar": {"libgone.so.1": "other package"}}
            self.assertEqual(level(compat.rule_c12(c), "C12"), [compat.FAIL])  # only for the listed package
        finally:
            compat.ELF_DIRS = old

    def test_c14_pacnew(self):
        c = compat.Ctx({})
        self.assertEqual(level(compat.rule_c14(c), "C14"), [compat.OK])
        c.pacnew = ["/etc/pacman.conf.pacnew"]
        self.assertEqual(level(compat.rule_c14(c), "C14"), [compat.WARN])

    def test_find_pacnew_only_newer_than_stable(self):
        etc, stable = os.path.join(self.t, "etc"), os.path.join(self.t, "stable")
        put(os.path.join(etc, "a.pacnew"), "x")
        put(os.path.join(etc, "b.conf"), "x")
        os.environ["ETC_PATH"], os.environ["STABLE_DIR"] = etc, stable
        try:
            self.assertEqual(compat.find_pacnew(), [os.path.join(etc, "a.pacnew")])  # no stable snapshot yet
            snap = os.path.join(stable, "2026-10-04", "snapshots.txt")
            put(snap, "root=1\n")
            os.utime(snap, (time.time() + 100, time.time() + 100))
            self.assertEqual(compat.find_pacnew(), [])
        finally:
            del os.environ["ETC_PATH"], os.environ["STABLE_DIR"]


class Cli(Sandbox):
    def setUp(self):
        super().setUp()
        inst = "".join("%s %s\n" % kv for kv in BASE.items())
        self.stub("pacman", 'if [ "$1" = -Q ]; then printf "%s" "$STUB_INSTALLED"; exit 0; fi; exit 1')
        self.inst = inst

    def run_pre(self, updates, **extra):
        f = os.path.join(self.t, "updates")
        put(f, updates)
        return self.sh([sys.executable, os.path.join(DRV, "compat.py"), "preflight", "--from-file", f],
                       STUB_INSTALLED=self.inst, BOOT_PATH=self.t, ROOT_PATH=self.t, **extra)

    def test_preflight_clean(self):
        p = self.run_pre("")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertNotIn("FAIL", p.stdout)

    def test_preflight_breaking_is_2(self):
        p = self.run_pre("nvidia-utils 610.57.04-1 -> 611.1-1\n")
        self.assertEqual(p.returncode, 2)
        self.assertIn("FAIL C01", p.stdout)

    def test_preflight_warning_is_1(self):
        p = self.run_pre("hyprland 0.56.2-1 -> 0.57-1\n")
        self.assertEqual(p.returncode, 1)
        self.assertIn("WARN C11", p.stdout)

    def test_preflight_json_and_deterministic(self):
        a = self.run_pre("hyprland 0.56.2-1 -> 0.57-1\n", )
        b = self.run_pre("hyprland 0.56.2-1 -> 0.57-1\n")
        self.assertEqual(a.stdout, b.stdout)
        f = os.path.join(self.t, "u")
        put(f, "")
        p = self.sh([sys.executable, os.path.join(DRV, "compat.py"), "preflight", "--from-file", f, "--json"],
                    STUB_INSTALLED=self.inst, BOOT_PATH=self.t, ROOT_PATH=self.t)
        import json
        ids = [c["id"] for c in json.loads(p.stdout)["checks"]]
        self.assertIn("C01", ids)

    def test_preflight_via_checkupdates_stub(self):
        self.stub("checkupdates", 'echo "linux 7.2.3.arch1-3 -> 7.3.0.arch1-1"; exit 0')
        p = self.sh([sys.executable, os.path.join(DRV, "compat.py"), "preflight"], STUB_INSTALLED=self.inst, BOOT_PATH=self.t, ROOT_PATH=self.t)
        self.assertEqual(p.returncode, 2)  # linux without headers
        self.assertIn("FAIL C02", p.stdout)
        self.stub("checkupdates", "exit 1")
        p = self.sh([sys.executable, os.path.join(DRV, "compat.py"), "preflight"], STUB_INSTALLED=self.inst)
        self.assertEqual(p.returncode, 3)  # offline / mirror down: cannot evaluate, not a FAIL
        self.assertIn("WARN C00", p.stdout)
        self.assertNotIn("FAIL", p.stdout)

    def test_from_file_without_argument(self):
        p = self.sh([sys.executable, os.path.join(DRV, "compat.py"), "preflight", "--from-file"], STUB_INSTALLED=self.inst)
        self.assertEqual(p.returncode, 3)
        self.assertIn("uso:", p.stderr)
        self.assertNotIn("Traceback", p.stderr)

    def test_bad_usage(self):
        self.assertEqual(self.sh([sys.executable, os.path.join(DRV, "compat.py")]).returncode, 3)


if __name__ == "__main__":
    unittest.main()
