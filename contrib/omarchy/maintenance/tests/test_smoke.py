import json
import os
import socket
import stat
import subprocess
import tempfile
import unittest

MAINT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SMOKE = os.path.join(MAINT, "smoke-test.sh")
NOTIFY = os.path.join(MAINT, "smart-notify")


def put(path, text):
    with open(path, "w") as f:
        f.write(text)


def fake_sys(root, discard, name="root"):
    d = os.path.join(root, "block", "dm-0")
    os.makedirs(os.path.join(d, "dm"))
    os.makedirs(os.path.join(d, "queue"))
    other = os.path.join(root, "block", "dm-1")
    os.makedirs(os.path.join(other, "dm"))
    os.makedirs(os.path.join(other, "queue"))
    put(os.path.join(other, "dm", "name"), "swap\n")
    put(os.path.join(other, "queue", "discard_max_bytes"), "999\n")
    put(os.path.join(d, "dm", "name"), name + "\n")
    put(os.path.join(d, "queue", "discard_max_bytes"), str(discard) + "\n")


def run_smoke(sysroot, proc, only, extra_env=None, args=()):
    env = {**os.environ, "SYSFS_ROOT": sysroot, "PROC_ROOT": proc}
    env.update(extra_env or {})
    p = subprocess.run(["bash", SMOKE, "--only", only, *args], capture_output=True, text=True, env=env)
    return p


class SmokeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.sys = os.path.join(self.tmp.name, "sys")
        self.proc = os.path.join(self.tmp.name, "proc")
        os.makedirs(self.proc)
        put(os.path.join(self.proc, "mounts"), "/dev/nvme0n1p1 /boot vfat rw 0 0\n")

    def tearDown(self):
        self.tmp.cleanup()

    def test_discard_zero_fails(self):
        fake_sys(self.sys, 0)
        p = run_smoke(self.sys, self.proc, "TRIM01")
        self.assertTrue(p.stdout.startswith("FAIL TRIM01"), p.stdout)
        self.assertEqual(p.returncode, 1)

    def test_discard_positive_passes(self):
        fake_sys(self.sys, 2147450880)
        p = run_smoke(self.sys, self.proc, "TRIM01")
        self.assertTrue(p.stdout.startswith("PASS TRIM01"), p.stdout)
        self.assertEqual(p.returncode, 0)

    def test_root_mapping_missing(self):
        fake_sys(self.sys, 5, name="home")
        p = run_smoke(self.sys, self.proc, "TRIM01")
        self.assertTrue(p.stdout.startswith("FAIL TRIM01"), p.stdout)

    def test_boot_levels(self):
        fake_sys(self.sys, 1)
        for pct, st in (("40", "PASS"), ("85", "WARN"), ("97", "FAIL")):
            p = run_smoke(self.sys, self.proc, "BOOT01", {"BOOT_USED_PCT": pct})
            self.assertTrue(p.stdout.startswith(st + " BOOT01"), (pct, p.stdout))

    def test_boot_not_mounted_skips(self):
        put(os.path.join(self.proc, "mounts"), "/dev/x / btrfs rw 0 0\n")
        p = run_smoke(self.sys, self.proc, "BOOT01")
        self.assertTrue(p.stdout.startswith("SKIP BOOT01"), p.stdout)

    def test_json_and_order_deterministic(self):
        fake_sys(self.sys, 7)
        a = run_smoke(self.sys, self.proc, "TRIM01,BOOT01", {"BOOT_USED_PCT": "10"}, ("--json",))
        b = run_smoke(self.sys, self.proc, "TRIM01,BOOT01", {"BOOT_USED_PCT": "10"}, ("--json",))
        self.assertEqual(a.stdout, b.stdout)
        data = json.loads(a.stdout)
        self.assertEqual([c["id"] for c in data["checks"]], ["TRIM01", "BOOT01"])

    def test_smart02_config(self):
        script = os.path.join(self.tmp.name, "notify")
        put(script, "#!/bin/sh\n")
        conf = os.path.join(self.tmp.name, "smartd.conf")
        put(conf, "DEVICESCAN -a -m <nomailer> -M exec %s\n" % script)
        p = run_smoke(self.sys, self.proc, "SMART02", {"SMARTD_CONF": conf})
        self.assertTrue(p.stdout.startswith("FAIL SMART02"), p.stdout)  # not executable
        os.chmod(script, os.stat(script).st_mode | stat.S_IXUSR)
        p = run_smoke(self.sys, self.proc, "SMART02", {"SMARTD_CONF": conf})
        self.assertTrue(p.stdout.startswith("PASS SMART02"), p.stdout)
        put(conf, "# DEVICESCAN -M exec %s\nDEVICESCAN -a\n" % script)
        p = run_smoke(self.sys, self.proc, "SMART02", {"SMARTD_CONF": conf})
        self.assertTrue(p.stdout.startswith("FAIL SMART02"), p.stdout)


class GuardChecksTest(unittest.TestCase):
    """GUARD/MEMG: the desktop guards are enabled, current and always restarted."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = self.tmp.name
        self.bar, self.ubin, self.units, self.stubs = (os.path.join(t, d) for d in ("bar", "ubin", "units", "stubs"))
        for d in (os.path.join(self.bar, "bin"), self.ubin, self.units, self.stubs):
            os.makedirs(d)
        ui = '{"apps": [], "config": {"schema": [{"key": "enabled"}]}}'
        for name in ("omarchy-freeze-guard", "omarchy-memguard"):
            body = "#!/bin/sh\n[ \"$1\" = ui ] && echo '%s' && exit 0\necho PASS ok\n" % ui
            put(os.path.join(self.bar, "bin", name), body)
            put(os.path.join(self.ubin, name), body)
            os.chmod(os.path.join(self.ubin, name), 0o755)
            put(os.path.join(self.units, name + ".service"),
                "[Unit]\nStartLimitIntervalSec=0\n[Service]\nRestart=always\n")
        self.systemctl("enabled", "active")
        self.plugins = os.path.join(t, "plugins")
        for root in (os.path.join(self.bar, "plugins", "juansebas7ian.memguard"), os.path.join(self.plugins, "juansebas7ian.memguard")):
            os.makedirs(root)
            put(os.path.join(root, "Panel.qml"), "Panel {}\n")
        self.shell = os.path.join(t, "shell.json")
        put(self.shell, '{"bar": {"layout": {"right": [{"id": "juansebas7ian.memguard"}]}}}')

    def tearDown(self):
        self.tmp.cleanup()

    def systemctl(self, enabled, active):
        p = os.path.join(self.stubs, "systemctl")
        put(p, '#!/bin/sh\ncase "$2" in is-enabled) echo %s;; is-active) echo %s;; esac\n' % (enabled, active))
        os.chmod(p, 0o755)

    def run_ids(self, ids):
        env = {**os.environ, "PATH": self.stubs + ":" + os.environ["PATH"], "BAR_DIR": self.bar,
               "USER_BIN": self.ubin, "USER_UNITS": self.units, "PLUGINS_DIR": self.plugins, "SHELL_JSON": self.shell}
        p = subprocess.run(["bash", SMOKE, "--only", ids], capture_output=True, text=True, env=env)
        return {l.split()[1]: l.split()[0] for l in p.stdout.splitlines() if l.strip()}

    def test_all_good(self):
        r = self.run_ids("GUARD01,GUARD02,GUARD03,MEMG01,MEMG02,MEMG03,MEMG04,MEMG05,MEMG06")
        self.assertEqual(set(r.values()), {"PASS"}, r)

    def test_inactive_service_fails(self):
        self.systemctl("enabled", "inactive")
        self.assertEqual(self.run_ids("GUARD01,MEMG01"), {"GUARD01": "FAIL", "MEMG01": "FAIL"})

    def test_no_user_session_skips(self):
        self.systemctl("Failed to connect to bus", "Failed to connect to bus")
        self.assertEqual(self.run_ids("GUARD01"), {"GUARD01": "SKIP"})

    def test_stale_install_warns_missing_fails(self):
        put(os.path.join(self.ubin, "omarchy-memguard"), "#!/bin/sh\nold\n")
        os.remove(os.path.join(self.ubin, "omarchy-freeze-guard"))
        self.assertEqual(self.run_ids("GUARD02,MEMG02"), {"GUARD02": "FAIL", "MEMG02": "WARN"})

    def test_unit_that_gives_up_fails(self):
        put(os.path.join(self.units, "omarchy-memguard.service"), "[Service]\nRestart=on-failure\n")
        self.assertEqual(self.run_ids("MEMG03"), {"MEMG03": "FAIL"})

    def test_widget_missing_stale_or_off_the_bar(self):
        put(self.shell, '{"bar": {"layout": {"right": []}}}')
        self.assertEqual(self.run_ids("MEMG05"), {"MEMG05": "WARN"})
        put(os.path.join(self.plugins, "juansebas7ian.memguard", "Panel.qml"), "old\n")
        self.assertEqual(self.run_ids("MEMG05"), {"MEMG05": "WARN"})
        import shutil
        shutil.rmtree(os.path.join(self.plugins, "juansebas7ian.memguard"))
        self.assertEqual(self.run_ids("MEMG05"), {"MEMG05": "FAIL"})

    def test_installer_marker_is_not_a_difference(self):
        put(os.path.join(self.plugins, "juansebas7ian.memguard", ".placed"), "")
        self.assertEqual(self.run_ids("MEMG05"), {"MEMG05": "PASS"})

    def test_widget_backend_json(self):
        put(os.path.join(self.ubin, "omarchy-memguard"), "#!/bin/sh\necho not json\n")
        self.assertEqual(self.run_ids("MEMG06"), {"MEMG06": "FAIL"})

    def test_memguard_self_check(self):
        mg = os.path.join(self.ubin, "omarchy-memguard")
        put(mg, "#!/bin/sh\necho PASS a\necho WARN NVML not available\n")
        self.assertEqual(self.run_ids("MEMG04"), {"MEMG04": "WARN"})
        put(mg, "#!/bin/sh\necho FAIL no status.json\nexit 1\n")
        self.assertEqual(self.run_ids("MEMG04"), {"MEMG04": "FAIL"})


class NotifyTest(unittest.TestCase):
    def test_logs_and_notifies(self):
        with tempfile.TemporaryDirectory() as t:
            bindir = os.path.join(t, "bin")
            os.makedirs(bindir)
            out = os.path.join(t, "out")

            def stub(name, body):
                p = os.path.join(bindir, name)
                put(p, "#!/bin/sh\n" + body + "\n")
                os.chmod(p, 0o755)
            stub("logger", 'echo "logger $*" >> %s' % out)
            stub("getent", "echo tester:x:1000:1000::/home/tester:/bin/sh")
            stub("runuser", 'echo "runuser $*" >> %s' % out)
            rundir = os.path.join(t, "run")
            os.makedirs(rundir)
            s = socket.socket(socket.AF_UNIX)
            s.bind(os.path.join(rundir, "bus"))
            env = {**os.environ, "PATH": bindir + ":" + os.environ["PATH"], "NOTIFY_RUNDIR": rundir,
                   "SMARTD_DEVICE": "/dev/nvme0", "SMARTD_MESSAGE": "Temperature 80", "SMARTD_FAILTYPE": "Temperature"}
            p = subprocess.run(["bash", NOTIFY], env=env, capture_output=True, text=True)
            s.close()
            self.assertEqual(p.returncode, 0, p.stderr)
            with open(out) as f:
                text = f.read()
            self.assertIn("smartd-notify", text)
            self.assertIn("-u tester", text)
            self.assertIn("DBUS_SESSION_BUS_ADDRESS=unix:path=%s/bus" % rundir, text)
            self.assertIn("notify-send -u critical", text)
            self.assertIn("/dev/nvme0", text)

    def test_no_session_still_exits_zero(self):
        with tempfile.TemporaryDirectory() as t:
            env = {**os.environ, "NOTIFY_RUNDIR": t, "SMARTD_DEVICE": "d", "SMARTD_MESSAGE": "m"}
            p = subprocess.run(["bash", NOTIFY], env=env, capture_output=True, text=True)
            self.assertEqual(p.returncode, 0)


if __name__ == "__main__":
    unittest.main()
