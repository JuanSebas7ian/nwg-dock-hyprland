"""Save-gate tests for bin/omarchy-session: a mass close (Omarchy closes every
window before logout/reboot/shutdown) must not overwrite the saved session,
and a deliberate close must still be saved once things settle.

Run: cd contrib/omarchy/bar && python3 -m unittest tests.test_session
"""

import socket
import tempfile
import types
import unittest
from importlib.machinery import SourceFileLoader
from pathlib import Path

BIN = Path(__file__).resolve().parent.parent / "bin" / "omarchy-session"


def run(events, start_windows):
  m = SourceFileLoader("omarchy_session", str(BIN)).load_module()
  clock = [1000.0]
  wins = [start_windows]
  writes = []
  m.time = types.SimpleNamespace(time=lambda: clock[0], sleep=lambda s: None)
  m.snapshot = lambda: {"windows": [0] * wins[0]}
  m.write_session = lambda snap: writes.append((clock[0], len(snap["windows"])))
  m.config = lambda: dict(m.DEFAULTS, restore=False)
  m.STATE = Path(tempfile.mkdtemp())
  script = list(events)

  class FakeSock:
    def settimeout(self, t):
      pass

    def recv(self, n):
      if not script:
        raise StopIteration
      t, payload = script.pop(0)
      clock[0] = t
      if payload is None:
        raise socket.timeout()
      if payload.startswith(b"close"):
        wins[0] = max(0, wins[0] - 1)
      elif payload.startswith(b"open"):
        wins[0] += 1
      return payload

  m.socket2 = lambda: FakeSock()
  try:
    m.daemon()
  except StopIteration:
    pass
  return writes


def ticks(a, b):
  return [(t, None) for t in range(a, b)]


class SaveGate(unittest.TestCase):
  def test_shutdown_close_all_keeps_saved_session(self):
    # Every window closes within a second, then the session ends.
    events = [(1001.0 + i / 10, b"closewindow>>x\n") for i in range(4)] + ticks(1002, 1010)
    writes = run(events, 4)
    self.assertEqual(writes, [(1000.0, 4)])

  def test_deliberate_close_is_saved_after_freeze(self):
    events = [(1001, b"closewindow>>a\n"), (1003, b"closewindow>>b\n"), (1005, b"closewindow>>c\n")] + ticks(1006, 1060)
    writes = run(events, 4)
    self.assertEqual(writes[-1], (1035, 1))

  def test_single_change_is_saved_after_debounce(self):
    writes = run([(1001, b"openwindow>>a\n")] + ticks(1002, 1010), 2)
    self.assertEqual(writes[-1], (1004, 3))


if __name__ == "__main__":
  unittest.main()
