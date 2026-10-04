#!/usr/bin/python3
"""Google Drive (rclone mount) backend for the bar. Prints JSON.

  gdrive.py status        mount state, transfers, upload queue, cache, quota, recent files
  gdrive.py mount|unmount start/stop the rclone-gdrive user service
  gdrive.py refresh       re-read the remote's directory tree

The mount is ~/.config/systemd/user/rclone-gdrive.service, which exposes
rclone's remote control on a private unix socket in $XDG_RUNTIME_DIR.
"""

import http.client
import json
import os
import socket
import subprocess
import sys
import time
from pathlib import Path

SERVICE = "rclone-gdrive.service"
REMOTE = "rclone:"
MOUNT = Path.home() / "GoogleDrive"
SOCK = Path(os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}")) / "rclone-gdrive.sock"
CACHE = Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache") / "omarchy-gdrive-widget"


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


def service_state():
  out = subprocess.run(["systemctl", "--user", "is-active", SERVICE], capture_output=True, text=True)
  return out.stdout.strip() or "unknown"


def quota():
  path = CACHE / "quota.json"
  try:
    if time.time() - path.stat().st_mtime < 600:
      return json.loads(path.read_text())
  except (OSError, ValueError):
    pass
  try:
    out = subprocess.run(["rclone", "about", REMOTE, "--json"], capture_output=True, text=True, timeout=30)
    data = json.loads(out.stdout)
  except Exception:
    try:
      return dict(json.loads(path.read_text()), stale=True)
    except (OSError, ValueError):
      return None
  CACHE.mkdir(parents=True, exist_ok=True)
  path.write_text(json.dumps(data))
  return data


def recent(cache_dir, limit=12):
  """Files this machine touched lately: the VFS cache holds what was opened or written."""
  base = Path(cache_dir) if cache_dir else None
  if not base or not base.is_dir():
    return []
  files = []
  for root, _dirs, names in os.walk(base):
    for n in names:
      p = Path(root) / n
      try:
        st = p.stat()
      except OSError:
        continue
      files.append((st.st_mtime, str(p.relative_to(base)), st.st_size))
  files.sort(reverse=True)
  return [{"path": rel, "name": os.path.basename(rel), "dir": os.path.dirname(rel),
           "mtime": int(m), "size": s} for m, rel, s in files[:limit]]


def status():
  result = {"service": service_state(), "mounted": os.path.ismount(MOUNT), "mountpoint": str(MOUNT),
            "rc": False, "cache": {}, "transfers": [], "queue": [], "speed": 0, "errors": 0,
            "quota": quota(), "recent": []}
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
      try:
        for q in (rc("vfs/queue").get("queue") or [])[:20]:
          result["queue"].append({"name": q.get("name", ""), "size": q.get("size", 0),
                                  "uploading": bool(q.get("uploading")), "tries": q.get("tries", 0)})
      except Exception:
        pass
      result["recent"] = recent(result["cache"].get("path"))
    except Exception as exc:
      result["rcError"] = str(exc)
  print(json.dumps(result, separators=(",", ":")))


def main(argv):
  cmd = argv[1] if len(argv) > 1 else "status"
  if cmd == "status":
    status()
  elif cmd in ("mount", "unmount"):
    action = "start" if cmd == "mount" else "stop"
    proc = subprocess.run(["systemctl", "--user", action, SERVICE], capture_output=True, text=True)
    print(json.dumps({"ok": proc.returncode == 0, "error": proc.stderr.strip()}))
  elif cmd == "refresh":
    try:
      rc("vfs/refresh", {"recursive": "false"})
      print(json.dumps({"ok": True}))
    except Exception as exc:
      print(json.dumps({"ok": False, "error": str(exc)}))
  else:
    print(json.dumps({"error": "usage: gdrive.py status|mount|unmount|refresh"}))
    return 2
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
