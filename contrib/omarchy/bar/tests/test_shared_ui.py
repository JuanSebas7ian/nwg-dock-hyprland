"""The shared QML components are copied into each plugin (Omarchy rejects
symlinks inside a plugin); every copy must match bar/shared/ui.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_shared_ui
"""

import unittest
from pathlib import Path

BAR = Path(__file__).resolve().parent.parent


class SharedUi(unittest.TestCase):
  def test_plugin_copies_match_shared_ui(self):
    shared = BAR / "shared" / "ui"
    for marker in BAR.glob("plugins/*/.uses-shared-ui"):
      copy = marker.parent / "ui"
      names = sorted(p.name for p in shared.iterdir())
      self.assertEqual(sorted(p.name for p in copy.iterdir()), names, f"{copy}: run sync-ui.sh")
      for n in names:
        self.assertEqual((copy / n).read_text(), (shared / n).read_text(), f"{copy / n}: run sync-ui.sh")


if __name__ == "__main__":
  unittest.main()
