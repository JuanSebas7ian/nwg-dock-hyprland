"""Tests for bin/gphotos-sync without Google: the "gphotos:" and Drive remotes are rclone
alias remotes (defined by environment variables) pointing at temporary folders.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_gphotos
"""

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SYNC = Path(__file__).resolve().parent.parent / "bin" / "gphotos-sync"


class GphotosSync(unittest.TestCase):
  def setUp(self):
    self.tmp = tempfile.TemporaryDirectory()
    t = self.root = Path(self.tmp.name)
    self.photos = t / "photos"          # what "Google Photos" receives
    self.drive = t / "drive"
    self.gf = t / "GoogleFotos"
    self.cam = t / "cam"
    self.pics = t / "Pictures"
    for d in (self.photos, self.drive, self.gf, self.cam, self.pics):
      d.mkdir()
    self.env = dict(os.environ, XDG_CONFIG_HOME=str(t / "config"), XDG_STATE_HOME=str(t / "state"),
                    XDG_CACHE_HOME=str(t / "cache"), RCLONE_CONFIG=str(t / "rclone.conf"),
                    RCLONE_CONFIG_GPFAKE_TYPE="alias", RCLONE_CONFIG_GPFAKE_REMOTE=str(self.photos),
                    RCLONE_CONFIG_DRFAKE_TYPE="alias", RCLONE_CONFIG_DRFAKE_REMOTE=str(self.drive))
    cfg = {"remote": "gpfake:", "paused": False, "bwlimit": "off", "transfers": 2, "drive_rescan_hours": 6,
           "sources": [
             {"id": "googlefotos", "name": "GF", "path": str(self.gf), "album": "{top}", "root_album": "GoogleFotos"},
             {"id": "screenshots", "name": "Shots", "path": str(self.pics), "recursive": False,
              "album": "Capturas", "root_album": "Capturas"},
             {"id": "camera", "name": "Cam", "path": str(self.cam), "album": "Cámara {top}", "root_album": "Cámara"},
             {"id": "drive", "name": "Drive", "remote": "drfake:", "album": "Drive · {top}", "root_album": "Drive",
              "exclude": ["Backups/"]}]}
    (t / "config" / "gphotos-sync").mkdir(parents=True)
    (t / "config" / "gphotos-sync" / "config.json").write_text(json.dumps(cfg))

  def tearDown(self):
    self.tmp.cleanup()

  def put(self, path, data=b"x" * 100):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)

  def sync(self, *args):
    return subprocess.run([str(SYNC), *args], env=self.env, capture_output=True, text=True, timeout=120)

  def status(self):
    return json.loads(self.sync("status").stdout)

  def album(self, name):
    d = self.photos / "album" / name
    return sorted(p.name for p in d.iterdir()) if d.exists() else []

  def test_sources_land_in_their_albums(self):
    self.put(self.gf / "suelta.jpg")
    self.put(self.gf / "Viaje" / "a.jpg")
    self.put(self.pics / "screenshot-1.png")
    self.put(self.pics / "sub" / "nested.png")        # screenshots are not recursive
    self.put(self.cam / "2014" / "b.mp4")
    self.put(self.drive / "Fotos" / "x" / "c.heic")
    self.put(self.drive / "notas.txt")                # not media
    r = self.sync("run")
    self.assertEqual(r.returncode, 0, r.stderr)
    self.assertEqual(self.album("GoogleFotos"), ["suelta.jpg"])
    self.assertEqual(self.album("Viaje"), ["a.jpg"])
    self.assertEqual(self.album("Capturas"), ["screenshot-1.png"])
    self.assertEqual(self.album("Cámara 2014"), ["b.mp4"])
    self.assertEqual(self.album("Drive · Fotos"), ["c.heic"])
    st = self.status()
    self.assertEqual(st["state"], "idle")
    self.assertEqual(st["uploadedTotal"], 5)
    self.assertEqual(st["pendingTotal"], 0)
    self.assertEqual(len(st["recent"]), 5)

  def test_never_uploads_twice(self):
    self.put(self.gf / "a.jpg")
    self.sync("run")
    (self.photos / "album" / "GoogleFotos" / "a.jpg").unlink()   # Photos cannot be asked: the ledger decides
    self.put(self.gf / "b.jpg")
    self.sync("run")
    self.assertEqual(self.album("GoogleFotos"), ["b.jpg"])

  def test_same_name_in_different_folders_of_one_album(self):
    self.put(self.drive / "Viajes" / "2019" / "IMG_1.jpg", b"a" * 10)
    self.put(self.drive / "Viajes" / "2020" / "IMG_1.jpg", b"b" * 20)
    self.sync("run")
    st = self.status()
    drive = [s for s in st["sources"] if s["id"] == "drive"][0]
    self.assertEqual((drive["uploaded"], drive["pending"]), (2, 0))

  def test_excluded_drive_folder(self):
    self.put(self.drive / "Backups" / "x.jpg")
    self.sync("run")
    self.assertEqual(self.album("Drive · Backups"), [])

  def test_disabled_source_and_pause(self):
    self.put(self.cam / "2010" / "a.jpg")
    self.sync("source", "camera", "off")
    self.sync("run")
    self.assertEqual(self.album("Cámara 2010"), [])
    self.sync("source", "camera", "on")
    cfg_path = self.root / "config" / "gphotos-sync" / "config.json"
    cfg = json.loads(cfg_path.read_text())
    cfg["paused"] = True
    cfg_path.write_text(json.dumps(cfg))
    self.sync("run")
    self.assertEqual(self.album("Cámara 2010"), [])
    self.assertEqual(self.status()["state"], "paused")

  def test_missing_remote_asks_for_setup(self):
    del self.env["RCLONE_CONFIG_GPFAKE_TYPE"]
    self.put(self.gf / "a.jpg")
    self.sync("run")
    st = self.status()
    self.assertEqual(st["state"], "setup")
    self.assertIn("gphotos-setup", st["error"])

  def test_plan_lists_albums_without_uploading(self):
    self.put(self.cam / "2012" / "a.jpg")
    out = self.sync("plan").stdout
    self.assertIn("Cámara 2012: 1", out)
    self.assertEqual(self.album("Cámara 2012"), [])

  def test_thumbnail_prefers_freedesktop_cache(self):
    import hashlib
    import urllib.parse
    self.put(self.cam / "2011" / "a b.jpg")
    uri = "file://" + urllib.parse.quote(str(self.cam / "2011" / "a b.jpg"))
    thumb = self.root / "cache" / "thumbnails" / "large" / (hashlib.md5(uri.encode()).hexdigest() + ".png")
    self.put(thumb)
    self.sync("run")
    self.assertEqual(self.status()["recent"][0]["thumb"], str(thumb))


if __name__ == "__main__":
  unittest.main()
