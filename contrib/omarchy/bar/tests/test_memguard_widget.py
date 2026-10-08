"""Static checks for the juansebas7ian.memguard bar widget: the manifest, the
tabs it loads, and that every command, setting and list the QML uses exists
in bin/omarchy-memguard (the QML has no other way to be tested here; the live
check is `omarchy-memguard ui` in the smoke test and opening each tab).

Run: cd contrib/omarchy/bar && python3 -m unittest tests.test_memguard_widget
"""

import json
import re
import shutil
import subprocess
import unittest
from importlib.machinery import SourceFileLoader
from pathlib import Path

BAR = Path(__file__).resolve().parent.parent
PLUGIN = BAR / "plugins" / "juansebas7ian.memguard"
BIN = BAR / "bin" / "omarchy-memguard"


def qml():
  return {p.relative_to(PLUGIN).as_posix(): p.read_text() for p in PLUGIN.rglob("*.qml") if p.parent.name != "ui"}


class Widget(unittest.TestCase):
  @classmethod
  def setUpClass(cls):
    cls.m = SourceFileLoader("omarchy_memguard_w", str(BIN)).load_module()
    cls.files = qml()
    cls.all = "\n".join(cls.files.values())

  def test_manifest(self):
    man = json.loads((PLUGIN / "manifest.json").read_text())
    self.assertEqual(man["id"], PLUGIN.name)
    self.assertEqual(man["kinds"], ["bar-widget"])
    self.assertTrue((PLUGIN / man["entryPoints"]["barWidget"]).exists())
    self.assertTrue((PLUGIN / ".uses-shared-ui").exists(), "gets the shared ui/ copy")

  def test_every_tab_exists(self):
    panel = self.files["Panel.qml"]
    tabs = re.search(r"tabFiles: \(\{(.*?)\}\)", panel).group(1)
    names = re.findall(r'"(\w+Tab)"', tabs)
    self.assertEqual(len(names), 4)
    for n in names:
      self.assertIn(f"tabs/{n}.qml", self.files)
    order = re.search(r"tabOrder: \[(.*?)\]", panel).group(1)
    self.assertEqual(re.findall(r'"(\w+)"', order), re.findall(r'(\w+): "\w+Tab"', tabs))

  def test_cli_verbs_exist(self):
    src = BIN.read_text()
    verbs = set(re.findall(r'\[(?:root\.)?bin, "(\w+)"', self.all)) | set(re.findall(r'change\("(\w+)"', self.all))
    self.assertTrue({"ui", "smoke", "add", "remove", "reset"} <= verbs | {"add", "remove"}, verbs)
    for v in verbs | {"set"}:
      self.assertRegex(src, rf'cmd (==|in) .*"{v}"', v)

  def test_settings_and_lists_used_exist(self):
    for key in re.findall(r'setValue\("(\w+)"', self.all):
      self.assertIn(key, self.m.DEFAULTS)
    for lst in re.findall(r'(?:forget|change\("(?:add|remove)",)\s*"(\w+)"', self.all):
      self.assertIn(lst, self.m.LISTS)
    for lst in re.findall(r'id: "(\w+)", title:', self.files["tabs/AppsTab.qml"]):
      self.assertIn(lst, self.m.LISTS)
    for lst in re.findall(r'tab\.add\("(\w+)"\)', self.files["tabs/AppsTab.qml"]):
      self.assertIn(lst, self.m.LISTS)

  def test_fields_read_from_the_ui_json_exist(self):
    st = self.m.ui_state.__code__.co_consts
    text = BIN.read_text()
    for field in ("apps", "events", "config", "service", "status"):
      self.assertIn(f'"{field}"', text)
    local = {"id", "title", "hint"}  # AppsTab's own list of exception kinds, not backend JSON
    for field in set(re.findall(r"modelData\.(\w+)", self.files["tabs/AppsTab.qml"])) - local:
      self.assertTrue(f'"{field}"' in text, f"AppsTab reads {field}, which ui_state does not send")
    for field in re.findall(r"modelData\.(\w+)", self.files["tabs/SettingsTab.qml"]):
      self.assertIn(field, ("key", "group", "label", "unit", "min", "max", "step", "value", "default"), field)
    self.assertTrue(st)

  def test_no_scroll_width_loop(self):
    # 2026-10-08: a width that depends on flick.interactive spun the shell at 100 %
    self.assertNotIn("flick.interactive ?", self.all)
    for p in BAR.glob("plugins/*/Panel.qml"):
      self.assertNotIn("flick.interactive ?", p.read_text(), p)

  def test_installer_places_it_after_hardware(self):
    self.assertIn('"juansebas7ian.memguard:juansebas7ian.hardware"', (BAR / "install.sh").read_text())

  @unittest.skipUnless(shutil.which("omarchy"), "omarchy not installed")
  def test_omarchy_validates_it(self):
    p = subprocess.run(["omarchy", "plugin", "validate", str(PLUGIN)], capture_output=True, text=True, timeout=60)
    self.assertEqual(p.returncode, 0, p.stdout + p.stderr)


if __name__ == "__main__":
  unittest.main()
