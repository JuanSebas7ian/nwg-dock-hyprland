"""drivers/smoke-test.sh with stub install.sh/compat and fake hook directories."""
import json
import os
import shutil
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from helpers import DRV, Sandbox, put  # noqa: E402


class DriversSmoke(Sandbox):
    def setUp(self):
        super().setUp()
        self.d = os.path.join(self.t, "drivers")
        os.makedirs(os.path.join(self.d, "pacman-hook"))
        shutil.copy(os.path.join(DRV, "smoke-test.sh"), self.d)
        shutil.copy(os.path.join(DRV, "compat.py"), self.d)
        for f in ("90-omarchy-compat.hook", "compat-hook"):
            shutil.copy(os.path.join(DRV, "pacman-hook", f), os.path.join(self.d, "pacman-hook"))
        self.etc, self.lib = os.path.join(self.t, "hooks"), os.path.join(self.t, "lib")
        self.set_check(0, "OK: todo")

    def set_check(self, rc, out):
        put(os.path.join(self.d, "install.sh"), "#!/bin/sh\ncat <<'EOT'\n%s\nEOT\nexit %d\n" % (out, rc), 0o755)

    def install_hook(self):
        os.makedirs(self.etc, exist_ok=True)
        os.makedirs(self.lib, exist_ok=True)
        shutil.copy(os.path.join(self.d, "pacman-hook/90-omarchy-compat.hook"), self.etc)
        for src, name in (("pacman-hook/compat-hook", "compat-hook"), ("compat.py", "compat.py")):
            shutil.copy(os.path.join(self.d, src), os.path.join(self.lib, name))
            os.chmod(os.path.join(self.lib, name), 0o755)

    def smoke(self, *args, compat_rc=0, compat_out="OK C14 x"):
        fake = os.path.join(self.t, "fake-compat")
        put(fake, "#!/bin/sh\necho '%s'\nexit %d\n" % (compat_out, compat_rc), 0o755)
        return self.sh(["bash", os.path.join(self.d, "smoke-test.sh"), *args], HOOK_ETC=self.etc, HOOK_LIB=self.lib,
                       COMPAT_CMD=fake)

    def test_all_pass(self):
        self.install_hook()
        p = self.smoke()
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual([l.split()[0] for l in p.stdout.splitlines()], ["PASS"] * 4)

    def test_install_check_failure_is_fail(self):
        self.install_hook()
        self.set_check(1, "FALTA paquete (repo): foo\nDIFIERE archivo: /etc/x (differs)")
        p = self.smoke()
        self.assertEqual(p.returncode, 1)
        self.assertIn("FAIL INST01", p.stdout)
        self.assertIn("FAIL ETC01", p.stdout)

    def test_etc_only_difference(self):
        self.install_hook()
        self.set_check(1, "DIFIERE archivo: /etc/x (differs)")
        p = self.smoke()
        self.assertIn("PASS INST01", p.stdout)
        self.assertIn("FAIL ETC01", p.stdout)

    def test_compat_levels(self):
        self.install_hook()
        self.assertIn("FAIL COMPAT01", self.smoke(compat_rc=2, compat_out="FAIL C13c broken").stdout)
        self.assertIn("WARN COMPAT01", self.smoke(compat_rc=1, compat_out="WARN C14 pacnew").stdout)

    def test_hook_missing_is_warn_not_fail(self):
        p = self.smoke("--only", "HOOK01")
        self.assertTrue(p.stdout.startswith("WARN HOOK01"))
        self.assertEqual(p.returncode, 0)
        self.install_hook()
        self.assertTrue(self.smoke("--only", "HOOK01").stdout.startswith("PASS HOOK01"))
        os.chmod(os.path.join(self.lib, "compat-hook"), 0o644)
        self.assertTrue(self.smoke("--only", "HOOK01").stdout.startswith("WARN HOOK01"))

    def test_json(self):
        self.install_hook()
        data = json.loads(self.smoke("--json").stdout)
        self.assertEqual([c["id"] for c in data["checks"]], ["INST01", "ETC01", "COMPAT01", "HOOK01"])


if __name__ == "__main__":
    unittest.main()
