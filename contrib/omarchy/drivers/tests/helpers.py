import os
import stat
import subprocess
import tempfile
import unittest

DRV = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def put(path, text, mode=None):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text)
    if mode is not None:
        os.chmod(path, mode)


def read(path):
    with open(path) as f:
        return f.read()


class Sandbox(unittest.TestCase):
    """Temp dir with a bin/ of stub commands first in PATH."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.t = self.tmp.name
        self.bin = os.path.join(self.t, "bin")
        os.makedirs(self.bin)
        self.log = os.path.join(self.t, "calls.log")
        put(self.log, "")

    def tearDown(self):
        self.tmp.cleanup()

    def stub(self, name, body):
        put(os.path.join(self.bin, name), "#!/bin/bash\n" + body + "\n", 0o755)

    def calls(self):
        with open(self.log) as f:
            return f.read()

    def env(self, **extra):
        e = {k: v for k, v in os.environ.items() if not k.startswith("STUB_") and k != "HYPRLAND_INSTANCE_SIGNATURE"}
        e["PATH"] = self.bin + ":" + os.environ["PATH"]
        e["PYTHONDONTWRITEBYTECODE"] = "1"
        e.update(extra)
        return e

    def sh(self, args, inp=None, timeout=60, **extra):
        return subprocess.run(args, capture_output=True, text=True, input=inp, timeout=timeout, env=self.env(**extra))
