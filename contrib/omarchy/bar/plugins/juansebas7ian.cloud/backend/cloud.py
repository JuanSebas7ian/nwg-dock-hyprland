#!/usr/bin/python3
"""Status of every cloud service for the juansebas7ian.cloud bar widget, in one
process and one JSON object (the four checks run in parallel, ~80 ms):

  cloud.py status

  gdrive    rclone mount at ~/GoogleDrive (gdrive.py status)
  icloud    rclone mount at ~/iCloud and Apple's 30-day sign-in (icloud.py status)
  gphotos   upload-only Google Photos sync (~/.local/bin/gphotos-sync status)
  dropbox   the Dropbox daemon (dropbox-cli) and its recent files, read with
            Omarchy's own helper; its plan table only knows the base quota, so a
            "used" larger than the plan is reported as an unknown quota.

Actions stay with each service's own CLI (gdrive.py, icloud.py, gphotos-sync,
dropbox-cli); the panel calls them directly.
"""

import io
import json
import os
import shutil
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from contextlib import redirect_stdout
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

GPHOTOS = Path(os.environ.get("GPHOTOS_SYNC") or Path.home() / ".local/bin/gphotos-sync")
DROPBOX_HELPER = Path(os.environ.get("OMARCHY_PATH") or "/usr/share/omarchy") / "shell/plugins/panels/dropbox/status.py"


def captured(fn):
  """Run a helper that prints JSON and return the parsed object."""
  buf = io.StringIO()
  try:
    with redirect_stdout(buf):
      fn()
    return json.loads(buf.getvalue() or "{}")
  except Exception as exc:  # one broken service must not hide the others
    return {"error": str(exc)[:300]}


def json_cmd(cmd, timeout=15):
  try:
    out = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout).stdout
    return json.loads(out or "{}")
  except (OSError, subprocess.SubprocessError, ValueError) as exc:
    return {"error": str(exc)[:300]}


def gdrive():
  import gdrive as g
  return captured(g.status)


def icloud():
  import icloud as i
  return captured(i.status)


def gphotos():
  if not GPHOTOS.exists():
    return {"state": "missing", "error": "gphotos-sync is not installed"}
  return json_cmd([str(GPHOTOS), "status"])


def dropbox():
  if not shutil.which("dropbox-cli"):
    return {"installed": False}
  data = json_cmd([sys.executable, "-B", str(DROPBOX_HELPER), "12"]) if DROPBOX_HELPER.exists() else {}
  if not data or data.get("error"):
    try:
      text = subprocess.run(["dropbox-cli", "status"], capture_output=True, text=True, timeout=5).stdout.strip()
    except (OSError, subprocess.SubprocessError):
      text = ""
    data = {"installed": True, "running": bool(text) and "isn't running" not in text.lower(), "statusText": text or "Stopped",
            "authenticated": (Path.home() / ".dropbox" / "info.json").exists(), "files": [], "usedBytes": 0, "quotaBytes": 0}
  if data.get("quotaBytes") and data.get("usedBytes", 0) > data["quotaBytes"]:
    data["quotaKnown"] = False  # bonus space or a plan the table does not know
  status = (data.get("statusText") or "").lower()
  data["syncing"] = any(w in status for w in ("syncing", "uploading", "downloading", "indexing"))
  return data


def mounts():
  # gdrive and icloud print their JSON: redirect_stdout swaps sys.stdout for the
  # whole process, so they must run one after the other, never in parallel.
  return {"gdrive": gdrive(), "icloud": icloud()}


def status():
  with ThreadPoolExecutor(max_workers=3) as pool:
    m = pool.submit(mounts)
    g = pool.submit(gphotos)
    d = pool.submit(dropbox)
    result = dict(m.result(), gphotos=g.result(), dropbox=d.result())
  result["t"] = int(time.time())
  return result


def main(argv):
  if len(argv) > 1 and argv[1] != "status":
    print(__doc__, file=sys.stderr)
    return 2
  print(json.dumps(status(), separators=(",", ":")))
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
