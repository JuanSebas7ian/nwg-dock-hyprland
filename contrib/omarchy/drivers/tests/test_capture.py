"""capture.sh against a fake ROOT: deterministic, preserves structure, never copies secrets, never writes outside."""
import hashlib
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from helpers import DRV, Sandbox, put, read  # noqa: E402

CAPTURE = os.path.join(DRV, "capture.sh")


def tree_hash(root):
    h = hashlib.sha256()
    for d, _, fs in sorted(os.walk(root)):
        for f in sorted(fs):
            p = os.path.join(d, f)
            h.update(p.encode())
            with open(p, "rb") as fh:
                h.update(fh.read())
    return h.hexdigest()


class CaptureTest(Sandbox):
    def setUp(self):
        super().setUp()
        self.root = os.path.join(self.t, "root")
        self.repo = os.path.join(self.t, "repo")
        self.man = os.path.join(self.repo, "manifest.json")
        man = {"version": 1,
               "groups": {"nvidia": {"repo": ["pkg-b", "pkg-a", "aur-x"], "aur": []}},
               "etc_files": [{"dest": "/etc/zz.conf"}, {"dest": "/etc/aa.conf"}, {"dest": "/usr/local/lib/omarchy/tool"}, {"dest": "/etc/secret.conf"}],
               "services": {"system": ["b.timer", "a"], "user": []}, "dkms": [], "omarchy_owned": ["/etc/o.conf"], "hook": [], "installers": []}
        put(self.man, json.dumps(man))
        put(self.root + "/etc/zz.conf", "Z=1\n", 0o644)
        put(self.root + "/etc/aa.conf", "A=1\n", 0o600)
        put(self.root + "/usr/local/lib/omarchy/tool", "#!/bin/sh\n", 0o755)
        put(self.root + "/etc/secret.conf", "api_key = abc123\n", 0o644)
        self.stub("pacman", 'case "$1" in -Qqm) echo aur-x;; -Qq) printf "pkg-a\\npkg-b\\naur-x\\n";; '
                  '-Q) printf "pkg-a 1-1\\npkg-b 2:2-1\\naur-x 3-1\\ncuda 13.3.1-1\\n";; esac')
        self.stub("dkms", 'echo "nvidia/610.57.04, 7.2.3-arch1-3, x86_64: installed"; echo "hid-xpadneo/v0.10.4, 7.2.3-arch1-3, x86_64: installed"')
        self.stub("nvidia-smi", 'echo "NVIDIA GeForce RTX 3060, 610.57.04"')
        put(os.path.join(self.t, "proc/driver/nvidia/version"), "NVRM version: Open  610.57.04  Release\nGCC\n")
        put(os.path.join(self.t, "proc/cmdline"), "root=/dev/x quiet\n")
        put(os.path.join(self.t, "sys/class/dmi/id/bios_version"), "1402\n")

    def cap(self, *args, **extra):
        e = dict(MANIFEST=self.man, ETC_DIR=os.path.join(self.repo, "etc"), ROOT=self.root,
                 PROC_ROOT=os.path.join(self.t, "proc"), SYSFS_ROOT=os.path.join(self.t, "sys"), KERNEL_RELEASE="7.2.3-arch1-3")
        e.update(extra)
        return self.sh(["bash", CAPTURE, *args], **e)

    def test_regenerates_and_sorts(self):
        p = self.cap()
        self.assertEqual(p.returncode, 1, p.stdout + p.stderr)  # 1 = a secret was skipped
        m = json.loads(read(self.man))
        self.assertEqual(m["groups"]["nvidia"], {"repo": ["pkg-a", "pkg-b"], "aur": ["aur-x"]})
        self.assertEqual([e["dest"] for e in m["etc_files"]],
                         ["/etc/aa.conf", "/etc/secret.conf", "/etc/zz.conf", "/usr/local/lib/omarchy/tool"])
        modes = {e["dest"]: e["mode"] for e in m["etc_files"]}
        self.assertEqual(modes["/etc/aa.conf"], "600")
        self.assertEqual(modes["/usr/local/lib/omarchy/tool"], "755")
        self.assertEqual(m["etc_files"][2]["src"], "etc/zz.conf")
        self.assertEqual(m["etc_files"][3]["src"], "etc/_root/usr/local/lib/omarchy/tool")
        self.assertEqual(m["dkms"], ["hid-xpadneo", "nvidia"])
        self.assertEqual(m["services"]["system"], ["a", "b.timer"])
        self.assertEqual(m["omarchy_owned"], ["/etc/o.conf"])  # untouched structure
        self.assertEqual(read(os.path.join(self.repo, "etc/zz.conf")), "Z=1\n")
        self.assertEqual(read(os.path.join(self.repo, "etc/_root/usr/local/lib/omarchy/tool")), "#!/bin/sh\n")

    def test_secret_not_copied(self):
        p = self.cap()
        self.assertIn("secreto", p.stdout)
        self.assertFalse(os.path.exists(os.path.join(self.repo, "etc/secret.conf")))

    def test_deterministic_and_idempotent(self):
        self.cap()
        h1 = tree_hash(self.repo)
        p = self.cap()
        self.assertIn("sin cambios", p.stdout)
        self.assertEqual(tree_hash(self.repo), h1)

    def test_detects_system_change(self):
        self.cap()
        put(self.root + "/etc/zz.conf", "Z=2\n", 0o644)
        p = self.cap()
        self.assertIn("etc/zz.conf: cambiado", p.stdout)
        self.assertEqual(read(os.path.join(self.repo, "etc/zz.conf")), "Z=2\n")

    def test_unreadable_source_keeps_repo_copy(self):
        self.cap()
        os.remove(self.root + "/etc/zz.conf")
        p = self.cap()
        self.assertIn("se conserva la copia del repo", p.stdout)
        self.assertEqual(read(os.path.join(self.repo, "etc/zz.conf")), "Z=1\n")

    def test_state_file_deterministic(self):
        out = os.path.join(self.t, "state")
        self.cap("--state", out, "--no-repo", "--quiet")
        a = read(os.path.join(out, "drivers-state.json"))
        self.cap("--state", out, "--no-repo", "--quiet")
        self.assertEqual(a, read(os.path.join(out, "drivers-state.json")))
        s = json.loads(a)
        self.assertEqual(s["packages"]["pkg-b"], "2:2-1")
        self.assertEqual(s["cuda"], "13.3.1-1")
        self.assertEqual(s["kernel"], "7.2.3-arch1-3")
        self.assertEqual(s["bios"], "1402")
        self.assertIn("610.57.04", s["nvidia_loaded"])
        self.assertEqual(s["cmdline"], "root=/dev/x quiet")
        self.assertEqual(len(s["dkms"]), 2)

    def test_no_repo_leaves_repo_untouched(self):
        before = tree_hash(self.repo)
        self.cap("--no-repo", "--state", os.path.join(self.t, "st"), "--quiet")
        self.assertEqual(tree_hash(self.repo), before)

    def test_bad_usage(self):
        self.assertEqual(self.cap("--bogus").returncode, 3)


if __name__ == "__main__":
    unittest.main()
