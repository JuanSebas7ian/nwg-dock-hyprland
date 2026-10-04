"""The pacman hook: syntax, warnings, and the guarantee that it never fails nor takes more than 5 s."""
import configparser
import os
import sys
import time
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from helpers import DRV, Sandbox, put  # noqa: E402

HOOK = os.path.join(DRV, "pacman-hook", "90-omarchy-compat.hook")
WRAPPER = os.path.join(DRV, "pacman-hook", "compat-hook")
INSTALLED = "linux 7.2.3.arch1-3\nlinux-headers 7.2.3.arch1-3\nnvidia-utils 610.57.04-1\nlib32-nvidia-utils 610.57.04-1\nvulkan-tools 1.4-1\n"
SYNC = {
    "linux": "7.3.0.arch1-1", "nvidia-utils": "611.1-1", "vulkan-tools": "1.4-1",
}


def read(p):
    with open(p) as f:
        return f.read()


class HookFile(unittest.TestCase):
    def test_alpm_syntax(self):
        text = read(HOOK)
        cp = configparser.ConfigParser(allow_no_value=True, strict=False)
        cp.optionxform = str
        cp.read_string(text)
        self.assertEqual(set(cp.sections()), {"Trigger", "Action"})
        act = dict(cp.items("Action"))
        self.assertEqual(act["When"], "PreTransaction")
        self.assertIn("NeedsTargets", act)
        self.assertNotIn("AbortOnFail", text)  # must never abort a transaction
        self.assertEqual(act["Exec"], "/usr/local/lib/omarchy/compat-hook")
        for op in ("Install", "Upgrade"):
            self.assertIn("Operation = " + op, text)
        self.assertNotIn("Operation = Remove", text)  # names only: a removal cannot be told from an upgrade
        self.assertIn("Target = *", text)
        self.assertIn("Type = Package", text)

    def test_wrapper_always_exits_zero(self):
        t = read(WRAPPER)
        self.assertTrue(t.rstrip().endswith("exit 0"))
        self.assertIn("timeout -k 1 5", t)


class HookRun(Sandbox):
    def setUp(self):
        super().setUp()
        sync = "".join("%s %s\n" % kv for kv in SYNC.items())
        # -Si prints a block only for the names asked for, like the real thing
        self.stub("pacman", 'case "$1" in -Q) printf "%s" "$STUB_INSTALLED";; '
                  '-Si) shift; for n in "$@"; do v=$(printf "%s" "$STUB_SYNC" | awk -v n="$n" \'$1==n{print $2}\'); '
                  '[ -n "$v" ] && printf "Name            : %s\\nVersion         : %s\\n\\n" "$n" "$v"; done;; *) exit 1;; esac')
        self.env_extra = dict(STUB_INSTALLED=INSTALLED, STUB_SYNC=sync, BOOT_PATH=self.t, ROOT_PATH=self.t)
        # the wrapper with its absolute install paths pointed at the repo, so it can run before installation
        w = read(WRAPPER).replace("/usr/local/lib/omarchy/compat.py", os.path.join(DRV, "compat.py"))
        self.wrapper = os.path.join(self.t, "compat-hook")
        put(self.wrapper, w, 0o755)

    def hook(self, inp, **extra):
        return self.sh([self.wrapper], inp=inp, **{**self.env_extra, **extra})

    def test_warns_but_exits_zero(self):
        p = self.hook("linux\nnvidia-utils\n")
        self.assertEqual(p.returncode, 0)
        self.assertIn("[omarchy-compat] ROMPERÍA C01", p.stdout)  # nvidia-utils alone: family mismatch
        self.assertIn("[omarchy-compat] ROMPERÍA C02", p.stdout)  # linux without headers
        self.assertIn("2 paquetes revisados", p.stdout)

    def test_harmless_transaction_prints_summary_only(self):
        p = self.hook("vulkan-tools\n")
        self.assertEqual(p.returncode, 0)
        self.assertEqual(p.stdout.strip().count("\n"), 0, p.stdout)
        self.assertIn("1 paquetes revisados, 0 avisos", p.stdout)

    def test_never_nonzero_on_garbage(self):
        for inp in ("", "\n\n", "\x00\xff", "no-such-pkg\n"):
            self.assertEqual(self.hook(inp).returncode, 0, repr(inp))
        self.stub("pacman", "exit 1")
        self.assertEqual(self.hook("linux\n").returncode, 0)
        self.stub("pacman", 'echo boom >&2; kill -9 $$')
        self.assertEqual(self.hook("linux\n").returncode, 0)

    def test_first_repo_block_wins(self):
        self.stub("pacman", 'case "$1" in -Q) printf "%s" "$STUB_INSTALLED";; -Si) '
                  'printf "Name : vulkan-tools\\nVersion : 1.4-1\\n\\nName : vulkan-tools\\nVersion : 9.9-1\\n\\n";; esac')
        p = self.hook("vulkan-tools\n")
        self.assertIn("0 avisos", p.stdout)  # the 9.9 block of the second repo is ignored

    def test_unevaluable_says_so(self):
        self.stub("pacman", 'case "$1" in -Q) printf "%s" "$STUB_INSTALLED";; *) exit 1;; esac')
        p = self.hook("linux\n")
        self.assertEqual(p.returncode, 0)
        self.assertIn("no se pudo evaluar", p.stdout)
        self.assertNotIn("0 avisos", p.stdout)
        self.stub("pacman", "exec sleep 30")
        self.assertEqual(self.hook("linux\n").returncode, 0)

    def test_python_missing_or_broken_compat_py_still_zero(self):
        w = read(WRAPPER).replace("/usr/local/lib/omarchy/compat.py", "/nonexistent/compat.py")
        bad = os.path.join(self.t, "bad-hook")
        put(bad, w, 0o755)
        self.assertEqual(self.sh([bad], inp="linux\n", **self.env_extra).returncode, 0)

    def test_under_five_seconds_even_if_pacman_hangs(self):
        self.stub("pacman", "exec sleep 30")
        t0 = time.time()
        p = self.hook("linux\n")
        self.assertEqual(p.returncode, 0)
        self.assertLess(time.time() - t0, 5.5)

    def test_direct_python_hook_deterministic(self):
        a = self.sh([sys.executable, os.path.join(DRV, "compat.py"), "hook"], inp="linux\nnvidia-utils\n", **self.env_extra)
        b = self.sh([sys.executable, os.path.join(DRV, "compat.py"), "hook"], inp="nvidia-utils\nlinux\n", **self.env_extra)
        self.assertEqual(a.stdout, b.stdout)
        self.assertEqual(a.returncode, 0)


if __name__ == "__main__":
    unittest.main()
