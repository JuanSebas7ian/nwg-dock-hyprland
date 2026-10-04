"""install.sh --check / --dry-run / real run against a fake ROOT with stub binaries (sudo passes through)."""
import json
import os
import shutil
import stat
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from helpers import DRV, Sandbox, put, read  # noqa: E402

INSTALL = os.path.join(DRV, "install.sh")
FILES = {  # dest -> (content, mode)
    "/etc/modprobe.d/nvidia.conf": ("options nvidia_drm modeset=1\n", "644"),
    "/etc/limine-entry-tool.d/claude-x.conf": ('KERNEL_CMDLINE[default]+=" foo=1"\n', "644"),
    "/etc/systemd/system/ollama.service.d/context.conf": ("[Service]\nEnvironment=X=1\n", "644"),
    "/usr/local/lib/omarchy/tool": ("#!/bin/sh\n", "755"),
    "/etc/conf.d/misc": ("A=1\n", "644"),
    "/etc/modprobe.d/omarchy-made.conf": ("options omarchy=1\n", "644"),
}
OMARCHY_FILES = {"/etc/modprobe.d/omarchy-made.conf"}  # policy if-missing: never overwritten


def src_of(dest):
    return "etc/" + dest[5:] if dest.startswith("/etc/") else "etc/_root/" + dest.lstrip("/")


class InstallBase(Sandbox):
    def setUp(self):
        super().setUp()
        self.repo = os.path.join(self.t, "repo")
        self.root = os.path.join(self.t, "root")
        self.state = os.path.join(self.t, "state")
        os.makedirs(self.root)
        man = {
            "version": 1,
            "groups": {"nvidia": {"repo": ["pkg-a", "pkg-b"], "aur": []}, "cuda": {"repo": ["pkg-c"], "aur": ["aur-d"]}},
            "etc_files": [{"dest": d, "src": src_of(d), "mode": m, "owner": "root:root",
                          **({"policy": "if-missing"} if d in OMARCHY_FILES else {})} for d, (c, m) in FILES.items()],
            "services": {"system": ["svc-a", "svc-resume.service"], "user": []},
            "dkms": ["nvidia"],
            "omarchy_owned": ["/etc/omarchy-owned.conf"],
            "hook": [{"src": "pacman-hook/h.hook", "dest": "/etc/pacman.d/hooks/h.hook", "mode": "644"},
                     {"src": "compat.py", "dest": "/usr/local/lib/omarchy/compat.py", "mode": "755"}],
        }
        put(os.path.join(self.repo, "manifest.json"), json.dumps(man))
        put(os.path.join(self.repo, "pacman-hook/h.hook"), "[Trigger]\n")
        put(os.path.join(self.repo, "compat.py"), "print('x')\n")
        for d, (c, m) in FILES.items():
            put(os.path.join(self.repo, src_of(d)), c)
            put(self.root + d, c, int(m, 8))
        put(self.root + "/etc/omarchy-owned.conf", "x\n")
        put(self.root + "/etc/pacman.d/hooks/h.hook", "[Trigger]\n", 0o644)
        put(self.root + "/usr/local/lib/omarchy/compat.py", "print('x')\n", 0o755)
        # stubs: everything is installed/enabled unless STUB_* says otherwise
        self.stub("pacman", 'echo "pacman $*" >> "$STUB_LOG"; case "$1" in -Q) for p in "${@:2}"; do '
                  '[[ " $STUB_MISSING " == *" $p "* ]] && exit 1; done; exit 0;; esac; exit 0')
        self.stub("systemctl", 'echo "systemctl $*" >> "$STUB_LOG"; case "$1" in is-enabled) '
                  '[[ " $STUB_SVC_OFF " == *" $2 "* ]] && { echo disabled; exit 1; }; echo enabled;; esac; exit 0')
        self.stub("dkms", 'echo "dkms $*" >> "$STUB_LOG"; [ "$1" = status ] && echo "${STUB_DKMS-nvidia/610.57.04, 7.2.3-arch1-3, x86_64: installed}"; exit 0')
        self.stub("sudo", 'echo "sudo $*" >> "$STUB_LOG"; case "$1" in -v) exit 0;; -n) exit 0;; esac; exec "$@"')
        self.stub("snapper", 'echo "snapper $*" >> "$STUB_LOG"; echo 42')
        self.stub("limine-update", 'echo "limine-update" >> "$STUB_LOG"')
        # like Omarchy's wrapper without presets: interactive, so it fails here
        self.stub("mkinitcpio", 'echo "mkinitcpio $*" >> "$STUB_LOG"; exit 1')
        self.stub("checkupdates", '[ -n "$STUB_PENDING" ] && { echo "$STUB_PENDING"; exit 0; }; exit 2')
        self.stub("yay", 'echo "yay $*" >> "$STUB_LOG"')
        self.stub("omarchy", 'echo "omarchy-stub $*" >> "$STUB_LOG"; echo "no pkg here"')

    def fake_compat(self, rc):
        path = os.path.join(self.t, "fake-compat-%d" % rc)
        put(path, "#!/bin/sh\nexit %d\n" % rc, 0o755)
        return path

    def inst(self, *args, **extra):
        e = dict(MANIFEST=os.path.join(self.repo, "manifest.json"), ROOT=self.root, KERNEL_RELEASE="7.2.3-arch1-3",
                 OMARCHY_DRIVERS_STATE=self.state, COMPAT_CMD="true", STUB_LOG=self.log, HOME=self.t)
        e.update(extra)
        return self.sh(["bash", INSTALL, *args], **e)


class CheckTest(InstallBase):
    def test_clean(self):
        p = self.inst("--check")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("OK:", p.stdout)
        self.assertNotIn("sudo", self.calls())

    def test_missing_package(self):
        p = self.inst("--check", STUB_MISSING="pkg-b aur-d")
        self.assertEqual(p.returncode, 1)
        self.assertIn("FALTA paquete (repo): pkg-b", p.stdout)
        self.assertIn("FALTA paquete (AUR): aur-d", p.stdout)

    def test_file_differs_missing_and_mode(self):
        put(self.root + "/etc/modprobe.d/nvidia.conf", "changed\n", 0o644)
        os.remove(self.root + "/etc/conf.d/misc")
        os.chmod(self.root + "/usr/local/lib/omarchy/tool", 0o644)
        p = self.inst("--check")
        self.assertEqual(p.returncode, 1)
        self.assertIn("DIFIERE archivo: /etc/modprobe.d/nvidia.conf (differs)", p.stdout)
        self.assertIn("DIFIERE archivo: /etc/conf.d/misc (missing)", p.stdout)
        self.assertIn("DIFIERE archivo: /usr/local/lib/omarchy/tool (mode)", p.stdout)

    def test_service_dkms_owned(self):
        p = self.inst("--check", STUB_SVC_OFF="svc-a")
        self.assertIn("FALTA servicio habilitado (system): svc-a", p.stdout)
        p = self.inst("--check", STUB_DKMS="nvidia/610.57.04, 7.1.0-arch1-1, x86_64: installed")  # other kernel only
        self.assertIn("FALTA módulo DKMS instalado para 7.2.3-arch1-3: nvidia", p.stdout)
        p = self.inst("--check", STUB_DKMS="nvidia/610.57.04, 7.2.3-arch1-3, x86_64: built")
        self.assertEqual(p.returncode, 1)
        os.remove(self.root + "/etc/omarchy-owned.conf")
        p = self.inst("--check")
        self.assertIn("FALTA archivo de Omarchy: /etc/omarchy-owned.conf", p.stdout)

    def test_hook_missing_is_info_not_failure(self):
        os.remove(self.root + "/etc/pacman.d/hooks/h.hook")
        p = self.inst("--check")
        self.assertEqual(p.returncode, 0)
        self.assertIn("INFO gancho", p.stdout)

    def test_deterministic(self):
        a = self.inst("--check", STUB_MISSING="pkg-b pkg-a")
        b = self.inst("--check", STUB_MISSING="pkg-a pkg-b")
        self.assertEqual(a.stdout, b.stdout)

    def test_bad_usage(self):
        self.assertEqual(self.inst("--nope").returncode, 3)


class DryRunTest(InstallBase):
    def test_nothing_to_do(self):
        p = self.inst("--dry-run")
        self.assertEqual(p.returncode, 0)
        self.assertIn("nada que hacer", p.stdout)
        self.assertNotIn("sudo", self.calls())

    def test_lists_actions_without_touching_anything(self):
        os.remove(self.root + "/etc/conf.d/misc")
        put(self.root + "/etc/limine-entry-tool.d/claude-x.conf", "old\n", 0o644)
        p = self.inst("--dry-run", STUB_MISSING="pkg-b", STUB_SVC_OFF="svc-a")
        self.assertEqual(p.returncode, 0)
        for s in ("[dry] sudo pacman -S --needed --noconfirm pkg-b", "[dry] sudo install -D -m 644", "[dry] sudo limine-update",
                  "[dry] sudo systemctl enable --now svc-a", "[dry] sudo snapper"):
            self.assertIn(s, p.stdout)
        self.assertNotIn("mkinitcpio", p.stdout)  # no modprobe/mkinitcpio file changed
        self.assertFalse(os.path.exists(self.root + "/etc/conf.d/misc"))
        self.assertEqual(read(self.root + "/etc/limine-entry-tool.d/claude-x.conf"), "old\n")
        self.assertNotIn("sudo ", self.calls())
        self.assertFalse(os.path.exists(self.state))

    def test_aur_helper_chosen(self):
        p = self.inst("--dry-run", STUB_MISSING="aur-d")
        self.assertIn("[dry] yay -S --needed --noconfirm aur-d", p.stdout)


class RealRunTest(InstallBase):
    def test_real_run_then_idempotent(self):
        os.remove(self.root + "/etc/conf.d/misc")
        put(self.root + "/etc/modprobe.d/nvidia.conf", "old\n", 0o644)
        put(self.root + "/etc/limine-entry-tool.d/claude-x.conf", "old\n", 0o644)
        put(self.root + "/etc/systemd/system/ollama.service.d/context.conf", "old\n", 0o644)
        p = self.inst(STUB_MISSING="pkg-b", STUB_SVC_OFF="svc-a svc-resume.service", STUB_DKMS="")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        calls = self.calls()
        for s in ("sudo pacman -S --needed --noconfirm pkg-b", "sudo limine-update", "sudo systemctl daemon-reload",
                  "sudo dkms autoinstall -k 7.2.3-arch1-3", "sudo systemctl enable --now svc-a", "sudo systemctl enable svc-resume.service",
                  "snapper -c root create -t pre", "snapper -c root create -t post --pre-number 42"):
            self.assertIn(s, calls)
        self.assertNotIn("enable --now svc-resume", calls)
        self.assertNotIn("mkinitcpio", calls)  # no presets: limine-update is the only rebuild
        self.assertEqual(calls.splitlines().count("limine-update"), 1)
        self.assertEqual(read(self.root + "/etc/modprobe.d/nvidia.conf"), FILES["/etc/modprobe.d/nvidia.conf"][0])
        baks = [f for f in os.listdir(self.root + "/etc/modprobe.d") if ".bak." in f]
        self.assertEqual(len(baks), 1)
        self.assertEqual(read(os.path.join(self.root, "etc/modprobe.d", baks[0])), "old\n")
        self.assertEqual(os.listdir(self.root + "/etc/conf.d"), ["misc"])  # new file: no backup
        logs = [f for f in os.listdir(self.state) if f.startswith("install-")]
        self.assertTrue(read(os.path.join(self.state, logs[0])).rstrip().endswith("exit=0"))
        # second run: everything already matches -> nothing to do, no root, no snapshot
        put(self.log, "")
        p = self.inst()
        self.assertEqual(p.returncode, 0)
        self.assertIn("nada que hacer", p.stdout)
        self.assertNotIn("sudo", self.calls())

    def test_only_changed_inputs_trigger_rebuilds(self):
        put(self.root + "/etc/conf.d/misc", "old\n", 0o644)
        p = self.inst()
        self.assertEqual(p.returncode, 0)
        calls = self.calls()
        self.assertNotIn("limine-update", calls)
        self.assertNotIn("mkinitcpio", calls)
        self.assertNotIn("dkms autoinstall", calls)

    def test_rebuild_exactly_once_when_limine_and_modprobe_change(self):
        put(self.root + "/etc/limine-entry-tool.d/claude-x.conf", "old\n", 0o644)
        put(self.root + "/etc/modprobe.d/nvidia.conf", "old\n", 0o644)
        put(self.root + "/etc/mkinitcpio.d/linux.preset", "x\n")  # presets present: still only one rebuild
        p = self.inst()
        self.assertEqual(p.returncode, 0, p.stdout)
        calls = self.calls()
        self.assertEqual(calls.splitlines().count("limine-update"), 1)
        self.assertNotIn("mkinitcpio", calls)

    def test_modprobe_change_alone_rebuilds_once(self):
        put(self.root + "/etc/modprobe.d/nvidia.conf", "old\n", 0o644)
        self.inst()
        self.assertEqual(self.calls().splitlines().count("limine-update"), 1)

    def test_omarchy_owned_files_never_overwritten(self):
        path = self.root + "/etc/modprobe.d/omarchy-made.conf"
        put(path, "omarchy changed this\n", 0o644)
        p = self.inst("--check")
        self.assertEqual(p.returncode, 0)
        self.assertNotIn("omarchy-made", p.stdout)
        os.remove(self.root + "/etc/conf.d/misc")  # force a real run
        p = self.inst()
        self.assertEqual(p.returncode, 0)
        self.assertEqual(read(path), "omarchy changed this\n")
        self.assertEqual([f for f in os.listdir(self.root + "/etc/modprobe.d") if "bak" in f], [])
        os.remove(path)  # missing: it is installed
        p = self.inst("--check")
        self.assertIn("DIFIERE archivo: /etc/modprobe.d/omarchy-made.conf (missing)", p.stdout)
        self.assertEqual(self.inst().returncode, 0)
        self.assertEqual(read(path), FILES["/etc/modprobe.d/omarchy-made.conf"][0])

    def test_group_failure_skips_dependents(self):
        self.stub("pacman", 'echo "pacman $*" >> "$STUB_LOG"; case "$1" in -Q) [[ " $STUB_MISSING " == *" $2 "* ]] && exit 1; exit 0;; -S) exit 1;; esac')
        put(self.root + "/etc/limine-entry-tool.d/claude-x.conf", "old\n", 0o644)
        p = self.inst(STUB_MISSING="pkg-a", STUB_SVC_OFF="svc-a", STUB_DKMS="")
        self.assertEqual(p.returncode, 1)
        self.assertIn("OMITIDO", p.stdout)
        calls = self.calls()
        for bad in ("limine-update", "enable --now svc-a", "dkms autoinstall"):
            self.assertNotIn(bad, calls)
        self.assertEqual(read(self.root + "/etc/limine-entry-tool.d/claude-x.conf"), "old\n")

    def test_pending_updates_stop_a_real_run(self):
        p = self.inst(STUB_MISSING="pkg-a", STUB_PENDING="foo 1-1 -> 2-1")
        self.assertEqual(p.returncode, 1)
        self.assertIn("actualizaciones pendientes", p.stdout)
        self.assertNotIn("sudo", self.calls())
        p = self.inst("--allow-pending", STUB_MISSING="pkg-a", STUB_PENDING="foo 1-1 -> 2-1")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn("pacman -S --needed --noconfirm pkg-a", self.calls())

    def test_pending_only_warns_in_dry_run_and_ignored_by_check_and_without_package_work(self):
        p = self.inst("--dry-run", STUB_MISSING="pkg-a", STUB_PENDING="foo 1-1 -> 2-1")
        self.assertEqual(p.returncode, 0)
        self.assertIn("actualizaciones pendientes", p.stdout)
        p = self.inst("--check", STUB_MISSING="pkg-a", STUB_PENDING="foo 1-1 -> 2-1")
        self.assertNotIn("actualizaciones pendientes", p.stdout)
        os.remove(self.root + "/etc/conf.d/misc")  # etc-only work: no package install, no partial-upgrade risk
        p = self.inst(STUB_PENDING="foo 1-1 -> 2-1")
        self.assertEqual(p.returncode, 0, p.stdout)

    def test_failure_is_reported(self):
        self.stub("pacman", 'echo "pacman $*" >> "$STUB_LOG"; case "$1" in -Q) [[ " $STUB_MISSING " == *" $2 "* ]] && exit 1; exit 0;; -S) exit 1;; esac')
        p = self.inst(STUB_MISSING="pkg-a")
        self.assertEqual(p.returncode, 1)
        self.assertIn("pasos fallidos: 1", p.stdout)

    def test_compat_failure_fails_run(self):
        os.remove(self.root + "/etc/conf.d/misc")
        p = self.inst(COMPAT_CMD=self.fake_compat(2))
        self.assertEqual(p.returncode, 1)

    def test_groups_restricts_packages_only(self):
        os.remove(self.root + "/etc/conf.d/misc")
        p = self.inst("--check", "--groups", "cuda", STUB_MISSING="pkg-a pkg-c")
        self.assertIn("pkg-c", p.stdout)
        self.assertNotIn("pkg-a", p.stdout)
        self.assertNotIn("DIFIERE", p.stdout)

    def test_hook_only(self):
        os.remove(self.root + "/etc/pacman.d/hooks/h.hook")
        put(self.root + "/usr/local/lib/omarchy/compat.py", "old\n", 0o755)
        os.remove(self.root + "/etc/conf.d/misc")  # unrelated drift must be left alone
        p = self.inst("--hook-only", STUB_MISSING="pkg-a")
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertTrue(os.path.exists(self.root + "/etc/pacman.d/hooks/h.hook"))
        self.assertEqual(read(self.root + "/usr/local/lib/omarchy/compat.py"), "print('x')\n")
        self.assertFalse(os.path.exists(self.root + "/etc/conf.d/misc"))
        self.assertNotIn("pacman -S", self.calls())
        self.assertTrue(stat.S_IMODE(os.stat(self.root + "/usr/local/lib/omarchy/compat.py").st_mode) == 0o755)
        put(self.log, "")
        p = self.inst("--hook-only")
        self.assertIn("nada que hacer", p.stdout)
        self.assertNotIn("sudo", self.calls())


if __name__ == "__main__":
    unittest.main()
