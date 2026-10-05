#!/usr/bin/python3
"""iCloud Drive (rclone mount) backend for the bar. Prints JSON.

  icloud.py status            setup/auth state, mount, transfers, upload queue, cache, recent files
  icloud.py ls [DIR]          documents in DIR (relative to ~/iCloud) with their sync state
  icloud.py keep PATH         download PATH (file or folder) into the local cache in the background
  icloud.py mount|unmount     start/stop the rclone-icloud user service
  icloud.py photos-mount|photos-unmount   same for the read-only iCloud Photos mount
  icloud.py refresh           re-read the remote's folders

The mount is ~/.config/systemd/user/rclone-icloud.service (remote "icloud:"),
which exposes rclone's remote control on a private unix socket in
$XDG_RUNTIME_DIR. Sync state of a document:
  cloud      only in iCloud (opening it downloads it)
  partial    part of it is in the local cache
  local      fully in the local cache (opens without network)
  queued     changed here, waiting to upload
  uploading  being uploaded now
Apple's trust token lasts 30 days; icloud-setup records when it was issued.
"""

import http.client
import json
import os
import re
import socket
import subprocess
import sys
import time
from pathlib import Path

ENV = os.environ.get
REMOTE = ENV("ICLOUD_REMOTE", "icloud:")
SERVICE = ENV("ICLOUD_SERVICE", "rclone-icloud.service")
PHOTOS_SERVICE = ENV("ICLOUD_PHOTOS_SERVICE", "rclone-icloud-photos.service")
MOUNT = Path(ENV("ICLOUD_MOUNT", str(Path.home() / "iCloud")))
PHOTOS_MOUNT = Path(ENV("ICLOUD_PHOTOS_MOUNT", str(Path.home() / "iCloudPhotos")))
RUNTIME = Path(ENV("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}"))
SOCK = Path(ENV("ICLOUD_SOCK", str(RUNTIME / "rclone-icloud.sock")))
STATE = Path(ENV("XDG_STATE_HOME") or Path.home() / ".local/state") / "omarchy-icloud"
CACHE = Path(ENV("XDG_CACHE_HOME") or Path.home() / ".cache") / "omarchy-icloud-widget"
TRUST_DAYS = 30
AUTH_ERROR = re.compile(r"401|421|trust.?token|reauth|authenticat|2fa|session|PCS cookie|requestPCS|unauthori",
                        re.IGNORECASE)


class UnixHTTP(http.client.HTTPConnection):
  def __init__(self, path):
    super().__init__("localhost", timeout=5)
    self.path = path

  def connect(self):
    self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    self.sock.settimeout(5)
    self.sock.connect(str(self.path))


def rc(method, params=None):
  conn = UnixHTTP(SOCK)
  try:
    conn.request("POST", "/" + method, body=json.dumps(params or {}),
                 headers={"Content-Type": "application/json"})
    resp = conn.getresponse()
    data = json.loads(resp.read() or b"{}")
    if resp.status != 200:
      raise RuntimeError(data.get("error", f"HTTP {resp.status}"))
    return data
  finally:
    conn.close()


def run(cmd, timeout=10):
  try:
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
  except (OSError, subprocess.TimeoutExpired):
    return None


def service_state(unit):
  out = run(["systemctl", "--user", "is-active", unit])
  return (out.stdout.strip() if out else "") or "unknown"


def configured():
  """The remote exists in rclone's config (checked without touching Apple's servers)."""
  out = run(["rclone", "listremotes"])
  return bool(out) and REMOTE in out.stdout.split()


def auth_info():
  """Days left on Apple's 30-day trust token, from the time icloud-setup last authenticated."""
  stamp = STATE / "authenticated"
  try:
    issued = float(stamp.read_text().strip())
  except (OSError, ValueError):
    return {"issued": None, "daysLeft": None}
  left = TRUST_DAYS - (time.time() - issued) / 86400
  return {"issued": int(issued), "daysLeft": round(left, 1)}


def journal_auth_error():
  """Last authentication error the mount logged in the past hour, if any."""
  out = run(["journalctl", "--user", "-u", SERVICE, "--since", "-1h", "-o", "cat", "-n", "40", "--no-pager"])
  if not out:
    return ""
  for line in reversed(out.stdout.splitlines()):
    if AUTH_ERROR.search(line) and ("ERROR" in line or "CRITICAL" in line or "Failed" in line):
      return line.strip()[-240:]
  return ""


def quota():
  """iCloud storage from 'rclone about' (cached 10 min). None when the backend does not report it."""
  path = CACHE / "quota.json"
  try:
    if time.time() - path.stat().st_mtime < 600:
      return json.loads(path.read_text()) or None
  except (OSError, ValueError):
    pass
  out = run(["rclone", "about", REMOTE, "--json"], timeout=30)
  try:
    data = json.loads(out.stdout) if out and out.returncode == 0 else {}
  except ValueError:
    data = {}
  CACHE.mkdir(parents=True, exist_ok=True)
  path.write_text(json.dumps(data))
  return data or None


def vfs_cache_dir(stats=None):
  """Where rclone keeps the local copies of this remote's files."""
  try:
    path = (stats or rc("vfs/stats")).get("diskCache", {}).get("path")
    return Path(path) if path else None
  except Exception:
    return None


def upload_queue():
  try:
    return rc("vfs/queue").get("queue") or []
  except Exception:
    return []


def local_state(cache_dir, rel, size):
  """cloud / partial / local, from the (sparse) file rclone keeps in its VFS cache."""
  if not cache_dir:
    return "cloud"
  try:
    st = os.stat(cache_dir / rel)
  except OSError:
    return "cloud"
  if size == 0 or st.st_blocks * 512 >= size:
    return "local"
  return "partial" if st.st_blocks > 0 else "cloud"


def recent(cache_dir, limit=10):
  """Files this machine touched lately: the VFS cache holds what was opened or written."""
  if not cache_dir or not cache_dir.is_dir():
    return []
  files = []
  for root, _dirs, names in os.walk(cache_dir):
    for n in names:
      p = Path(root) / n
      try:
        st = p.stat()
      except OSError:
        continue
      files.append((st.st_mtime, str(p.relative_to(cache_dir)), st.st_size))
  files.sort(reverse=True)
  return [{"path": rel, "name": os.path.basename(rel), "dir": os.path.dirname(rel),
           "mtime": int(m), "size": s} for m, rel, s in files[:limit]]


def status():
  result = {"configured": configured(), "service": service_state(SERVICE), "mounted": os.path.ismount(MOUNT),
            "mountpoint": str(MOUNT), "photos": {"service": service_state(PHOTOS_SERVICE),
                                                  "mounted": os.path.ismount(PHOTOS_MOUNT),
                                                  "mountpoint": str(PHOTOS_MOUNT)},
            "auth": auth_info(), "authError": "", "rc": False, "cache": {}, "transfers": [], "queue": [],
            "speed": 0, "errors": 0, "lastError": "", "quota": None, "recent": []}
  if not result["configured"]:
    print(json.dumps(result, separators=(",", ":")))
    return
  if SOCK.exists():
    try:
      vfs = rc("vfs/stats")
      result["rc"] = True
      result["cache"] = vfs.get("diskCache") or {}
      stats = rc("core/stats")
      result["speed"] = stats.get("speed", 0)
      result["errors"] = stats.get("errors", 0)
      result["lastError"] = stats.get("lastError", "")
      for t in stats.get("transferring") or []:
        result["transfers"].append({"name": t.get("name", ""), "size": t.get("size", 0),
                                    "bytes": t.get("bytes", 0), "percent": t.get("percentage", 0),
                                    "speed": t.get("speedAvg", t.get("speed", 0)), "eta": t.get("eta")})
      for q in upload_queue()[:20]:
        result["queue"].append({"name": q.get("name", ""), "size": q.get("size", 0),
                                "uploading": bool(q.get("uploading")), "tries": q.get("tries", 0)})
      result["recent"] = recent(vfs_cache_dir(vfs))
    except Exception as exc:
      result["rcError"] = str(exc)
  if result["service"] == "failed" or AUTH_ERROR.search(result["lastError"] or ""):
    result["authError"] = (result["lastError"] if AUTH_ERROR.search(result["lastError"] or "")
                           else journal_auth_error())
  if result["mounted"]:
    result["quota"] = quota()
  print(json.dumps(result, separators=(",", ":")))


def safe_rel(rel):
  """Path inside the mount; refuses anything that climbs out of it."""
  rel = (rel or "").strip("/")
  full = (MOUNT / rel).resolve() if rel else MOUNT
  if full != MOUNT and MOUNT not in full.parents:
    raise ValueError("path outside ~/iCloud")
  return rel, full


def ls(rel):
  rel, full = safe_rel(rel)
  if not os.path.ismount(MOUNT):
    print(json.dumps({"ok": False, "error": "not mounted", "dir": rel, "entries": []}))
    return
  queue = {q.get("name", ""): q for q in upload_queue()}
  cache_dir = vfs_cache_dir()
  entries = []
  try:
    with os.scandir(full) as it:
      for de in it:
        try:
          st = de.stat()
        except OSError:
          continue
        path = f"{rel}/{de.name}" if rel else de.name
        if de.is_dir():
          entries.append({"name": de.name, "path": path, "dir": True, "size": 0, "mtime": int(st.st_mtime),
                          "state": "folder"})
          continue
        q = queue.get(path)
        state = ("uploading" if q.get("uploading") else "queued") if q else local_state(cache_dir, path, st.st_size)
        entries.append({"name": de.name, "path": path, "dir": False, "size": st.st_size,
                        "mtime": int(st.st_mtime), "state": state})
  except OSError as exc:
    print(json.dumps({"ok": False, "error": exc.strerror or str(exc), "dir": rel, "entries": []}))
    return
  entries.sort(key=lambda e: (not e["dir"], e["name"].casefold()))
  counts = {}
  for e in entries:
    counts[e["state"]] = counts.get(e["state"], 0) + 1
  print(json.dumps({"ok": True, "dir": rel, "entries": entries, "counts": counts}, separators=(",", ":")))


def keep(rel):
  """Read the file(s) once so rclone stores them in its cache: they then open without network."""
  rel, full = safe_rel(rel)
  if not rel:
    raise ValueError("pick a file or folder, not all of iCloud")
  if not os.path.ismount(MOUNT) or not full.exists():
    raise ValueError("not mounted" if not os.path.ismount(MOUNT) else "no such file")
  cmd = ["find", str(full), "-type", "f", "-exec", "cat", "{}", "+"]
  subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
  print(json.dumps({"ok": True}))


def systemctl(action, unit):
  proc = run(["systemctl", "--user", action, unit], timeout=60)
  ok = bool(proc) and proc.returncode == 0
  print(json.dumps({"ok": ok, "error": proc.stderr.strip() if proc else "timeout"}))


def main(argv):
  cmd = argv[1] if len(argv) > 1 else "status"
  arg = argv[2] if len(argv) > 2 else ""
  try:
    if cmd == "status":
      status()
    elif cmd == "ls":
      ls(arg)
    elif cmd == "keep":
      keep(arg)
    elif cmd in ("mount", "unmount"):
      systemctl("start" if cmd == "mount" else "stop", SERVICE)
    elif cmd in ("photos-mount", "photos-unmount"):
      systemctl("start" if cmd == "photos-mount" else "stop", PHOTOS_SERVICE)
    elif cmd == "refresh":
      rc("vfs/refresh", {"recursive": "false"})
      print(json.dumps({"ok": True}))
    else:
      print(json.dumps({"error": "usage: icloud.py status|ls [DIR]|keep PATH|mount|unmount|"
                                 "photos-mount|photos-unmount|refresh"}))
      return 2
  except Exception as exc:
    print(json.dumps({"ok": False, "error": str(exc)}))
    return 1
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
