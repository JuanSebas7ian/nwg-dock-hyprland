#!/usr/bin/python3
"""Spotify Web API backend for the bar panel. Prints JSON.

  spotify.py auth <client_id>     one-time login (PKCE, opens the browser)
  spotify.py status               now playing, devices, queue
  spotify.py library              recently played, top tracks, playlists
  spotify.py <action> [arg]       play | pause | toggle | next | previous | seek <ms> |
                                  volume <0-100> | shuffle <true|false> | repeat <off|context|track> |
                                  play-uri <spotify:uri> | play-context <spotify:uri> | device <id>

Uses the user's own Spotify developer app (Client ID only, PKCE: no secret).
Tokens live in ~/.config/spotify-bar/token.json (mode 0600). Playback on this
PC goes to the spotifyd Spotify Connect daemon (device "Omarchy"), started on
demand, so the Spotify app never has to open.
"""

import base64
import hashlib
import http.server
import json
import os
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

CONF = Path(os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config") / "spotify-bar"
TOKEN = CONF / "token.json"
CACHE = Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache") / "spotify-bar"
REDIRECT = "http://127.0.0.1:8898/callback"
SCOPES = ("user-read-playback-state user-modify-playback-state user-read-currently-playing "
          "user-read-recently-played user-top-read playlist-read-private playlist-read-collaborative "
          "user-library-read")
LOCAL_DEVICE = "Omarchy"
API = "https://api.spotify.com/v1"


def out(obj):
  print(json.dumps(obj, separators=(",", ":")), flush=True)


# ------------------------------------------------------------------ auth

def save_token(data):
  CONF.mkdir(parents=True, exist_ok=True, mode=0o700)
  tmp = TOKEN.with_suffix(".tmp")
  fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
  with os.fdopen(fd, "w") as f:
    json.dump(data, f)
  tmp.replace(TOKEN)


def token_request(params):
  body = urllib.parse.urlencode(params).encode()
  req = urllib.request.Request("https://accounts.spotify.com/api/token", data=body,
                               headers={"Content-Type": "application/x-www-form-urlencoded"})
  with urllib.request.urlopen(req, timeout=15) as r:
    return json.loads(r.read())


def auth(client_id):
  verifier = secrets.token_urlsafe(64)[:96]
  challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
  state = secrets.token_urlsafe(16)
  url = "https://accounts.spotify.com/authorize?" + urllib.parse.urlencode({
    "client_id": client_id, "response_type": "code", "redirect_uri": REDIRECT, "scope": SCOPES,
    "code_challenge_method": "S256", "code_challenge": challenge, "state": state})
  result = {}

  class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
      q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
      if q.get("state", [""])[0] == state:
        result["code"] = q.get("code", [""])[0]
        result["error"] = q.get("error", [""])[0]
      ok = bool(result.get("code"))
      self.send_response(200)
      self.send_header("Content-Type", "text/html; charset=utf-8")
      self.end_headers()
      self.wfile.write(("<h2>Spotify " + ("conectado. Ya puedes cerrar esta pestaña." if ok
                        else "no autorizado.") + "</h2>").encode())

    def log_message(self, *args):
      pass

  server = http.server.HTTPServer(("127.0.0.1", 8898), Handler)
  server.timeout = 300
  subprocess.Popen(["xdg-open", url], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
  print("Open this URL if the browser did not:", url, file=sys.stderr)
  deadline = time.time() + 300
  while "code" not in result and time.time() < deadline:
    server.handle_request()
  server.server_close()
  if not result.get("code"):
    out({"ok": False, "error": result.get("error") or "timed out"})
    return 1
  tok = token_request({"grant_type": "authorization_code", "code": result["code"], "redirect_uri": REDIRECT,
                       "client_id": client_id, "code_verifier": verifier})
  tok["client_id"] = client_id
  tok["expires_at"] = time.time() + tok.get("expires_in", 3600) - 60
  save_token(tok)
  out({"ok": True})
  return 0


def access_token():
  try:
    tok = json.loads(TOKEN.read_text())
  except (OSError, ValueError):
    return None
  if time.time() >= tok.get("expires_at", 0):
    new = token_request({"grant_type": "refresh_token", "refresh_token": tok["refresh_token"],
                         "client_id": tok["client_id"]})
    tok.update(new)
    tok["expires_at"] = time.time() + new.get("expires_in", 3600) - 60
    save_token(tok)
  return tok["access_token"]


class NotAuthorized(Exception):
  pass


def call(method, path, params=None, body=None):
  tok = access_token()
  if not tok:
    raise NotAuthorized()
  url = API + path + ("?" + urllib.parse.urlencode(params) if params else "")
  data = json.dumps(body).encode() if body is not None else (b"" if method in ("PUT", "POST") else None)
  req = urllib.request.Request(url, data=data, method=method,
                               headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"})
  try:
    with urllib.request.urlopen(req, timeout=10) as r:
      raw = r.read()
      return json.loads(raw) if raw else {}
  except urllib.error.HTTPError as e:
    try:
      msg = json.loads(e.read()).get("error", {}).get("message", "")
    except Exception:
      msg = ""
    raise RuntimeError(f"{e.code} {msg}".strip())


# ------------------------------------------------------------------ shaping

def image(images, want=300):
  if not images:
    return ""
  best = min(images, key=lambda i: abs((i.get("width") or want) - want))
  return best.get("url", "")


def track(t):
  if not t:
    return None
  album = t.get("album") or t.get("show") or {}
  return {"uri": t.get("uri", ""), "name": t.get("name", ""),
          "artist": ", ".join(a.get("name", "") for a in t.get("artists") or []) or (t.get("show") or {}).get("publisher", ""),
          "album": album.get("name", ""), "image": image(album.get("images") or t.get("images")),
          "duration": t.get("duration_ms", 0)}


def status():
  try:
    player = call("GET", "/me/player", {"additional_types": "episode"}) or {}
    devices = call("GET", "/me/player/devices").get("devices", [])
    queue = call("GET", "/me/player/queue") if player else {}
  except NotAuthorized:
    out({"authorized": False})
    return
  except Exception as exc:
    out({"authorized": True, "error": str(exc)})
    return
  ctx = player.get("context") or {}
  out({
    "authorized": True,
    "playing": player.get("is_playing", False),
    "track": track(player.get("item")),
    "progress": player.get("progress_ms", 0),
    "shuffle": player.get("shuffle_state", False),
    "repeat": player.get("repeat_state", "off"),
    "device": (player.get("device") or {}).get("name", ""),
    "deviceId": (player.get("device") or {}).get("id", ""),
    "volume": (player.get("device") or {}).get("volume_percent"),
    "context": ctx.get("uri", ""),
    "devices": [{"id": d.get("id"), "name": d.get("name"), "type": d.get("type"), "active": d.get("is_active")}
                for d in devices],
    "queue": [track(t) for t in (queue.get("queue") or [])[:20] if t],
    "localDevice": LOCAL_DEVICE,
  })


def library():
  try:
    recent = call("GET", "/me/player/recently-played", {"limit": 30}).get("items", [])
    top = call("GET", "/me/top/tracks", {"limit": 20, "time_range": "short_term"}).get("items", [])
    playlists = call("GET", "/me/playlists", {"limit": 40}).get("items", [])
  except NotAuthorized:
    out({"authorized": False})
    return
  except Exception as exc:
    out({"authorized": True, "error": str(exc)})
    return
  # "Jump back in": distinct playlists/albums the recent plays came from.
  contexts, seen = [], set()
  ctx_ids = []
  for item in recent:
    c = item.get("context") or {}
    uri = c.get("uri")
    if uri and uri not in seen and c.get("type") in ("playlist", "album", "artist"):
      seen.add(uri)
      ctx_ids.append((c.get("type"), uri))
  for kind, uri in ctx_ids[:8]:
    try:
      obj_id = uri.split(":")[-1]
      obj = call("GET", f"/{kind}s/{obj_id}", {"fields": "name,images,owner(display_name),uri"} if kind == "playlist" else None)
      contexts.append({"uri": uri, "kind": kind, "name": obj.get("name", ""), "image": image(obj.get("images"), 64),
                       "sub": kind.capitalize() + (" · " + obj["owner"]["display_name"] if obj.get("owner") else "")})
    except Exception:
      continue
  out({
    "authorized": True,
    "recentContexts": contexts,
    "recentTracks": [dict(track(i.get("track")), playedAt=i.get("played_at", "")) for i in recent[:20] if i.get("track")],
    "top": [track(t) for t in top],
    "playlists": [{"uri": p.get("uri"), "name": p.get("name", ""), "image": image(p.get("images"), 64),
                   "sub": f"{(p.get('tracks') or p.get('items') or {}).get('total', 0)} songs · {(p.get('owner') or {}).get('display_name', '')}"}
                  for p in playlists if p],
  })


def local_device_id():
  for d in call("GET", "/me/player/devices").get("devices", []):
    if d.get("name") == LOCAL_DEVICE:
      return d.get("id")
  return None


def ensure_device():
  """Return an active device id, starting spotifyd on this PC when nothing is active."""
  devices = call("GET", "/me/player/devices").get("devices", [])
  for d in devices:
    if d.get("is_active"):
      return d.get("id")
  dev = local_device_id()
  if not dev:
    subprocess.run(["systemctl", "--user", "start", "spotifyd.service"], capture_output=True)
    for _ in range(20):
      time.sleep(0.5)
      dev = local_device_id()
      if dev:
        break
  return dev


def action(name, arg):
  try:
    if name in ("play", "toggle", "pause"):
      state = call("GET", "/me/player") or {}
      playing = state.get("is_playing", False)
      if name == "pause" or (name == "toggle" and playing):
        call("PUT", "/me/player/pause")
      else:
        dev = None if state.get("device") else ensure_device()
        call("PUT", "/me/player/play", {"device_id": dev} if dev else None)
    elif name == "next":
      call("POST", "/me/player/next")
    elif name == "previous":
      call("POST", "/me/player/previous")
    elif name == "seek":
      call("PUT", "/me/player/seek", {"position_ms": int(float(arg))})
    elif name == "volume":
      call("PUT", "/me/player/volume", {"volume_percent": max(0, min(100, int(float(arg))))})
    elif name == "shuffle":
      call("PUT", "/me/player/shuffle", {"state": arg})
    elif name == "repeat":
      call("PUT", "/me/player/repeat", {"state": arg})
    elif name in ("play-uri", "play-context"):
      dev = ensure_device()
      body = {"uris": [arg]} if name == "play-uri" else {"context_uri": arg}
      call("PUT", "/me/player/play", {"device_id": dev} if dev else None, body)
    elif name == "device":
      if arg == "local":
        subprocess.run(["systemctl", "--user", "start", "spotifyd.service"], capture_output=True)
        for _ in range(20):
          arg = local_device_id()
          if arg:
            break
          time.sleep(0.5)
      call("PUT", "/me/player", body={"device_ids": [arg], "play": True})
    else:
      out({"ok": False, "error": "unknown action"})
      return 2
  except NotAuthorized:
    out({"ok": False, "error": "not authorized"})
    return 1
  except Exception as exc:
    out({"ok": False, "error": str(exc)})
    return 1
  out({"ok": True})
  return 0


# ------------------------------------------------------------------ spotifyd, no Web API

LAST_URI = CACHE / "last-uri"


def bus_names(prefix):
  out = subprocess.run(["busctl", "--user", "list", "--no-pager", "--no-legend"], capture_output=True, text=True).stdout
  return [line.split()[0] for line in out.splitlines() if line.split() and line.split()[0].startswith(prefix)]


def mpris_status(name):
  out = subprocess.run(["busctl", "--user", "get-property", name, "/org/mpris/MediaPlayer2",
                        "org.mpris.MediaPlayer2.Player", "PlaybackStatus"], capture_output=True, text=True).stdout
  return out.strip().split()[-1].strip('"') if out.strip() else ""


def local_play():
  """Play on this PC through spotifyd without the Spotify window or the Web API.

  TransferPlayback makes spotifyd the active device (its MPRIS player appears
  and it resumes the account's current session). With nothing to resume,
  OpenUri plays the last track any Spotify player reported.
  """
  if not bus_names("rs.spotifyd.instance"):
    subprocess.run(["systemctl", "--user", "start", "spotifyd.service"], capture_output=True)
    for _ in range(20):
      if bus_names("rs.spotifyd.instance"):
        break
      time.sleep(0.5)
  ctl = bus_names("rs.spotifyd.instance")
  if not ctl:
    out({"ok": False, "error": "spotifyd is not running (systemctl --user status spotifyd)"})
    return 1
  subprocess.run(["busctl", "--user", "call", ctl[0], "/rs/spotifyd/Controls", "rs.spotifyd.Controls",
                  "TransferPlayback"], capture_output=True)
  player = None
  for _ in range(16):
    names = bus_names("org.mpris.MediaPlayer2.spotifyd")
    if names:
      player = names[0]
      if mpris_status(player) == "Playing":
        out({"ok": True, "via": "transfer"})
        return 0
    time.sleep(0.25)
  if not player:
    out({"ok": False, "error": "spotifyd did not become the active device"})
    return 1
  try:
    uri = LAST_URI.read_text().strip()
  except OSError:
    uri = ""
  method = ["OpenUri", "s", uri] if uri.startswith("spotify:") else ["Play"]
  subprocess.run(["busctl", "--user", "call", player, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2.Player",
                  *method], capture_output=True)
  time.sleep(1.5)
  if mpris_status(player) == "Playing":
    out({"ok": True, "via": method[0]})
    return 0
  out({"ok": False, "error": "Nothing to resume yet: play something once (or connect your account)."})
  return 1


def remember(uri):
  m = re.search(r"(?:spotify[:/])?(track|episode)[:/]([A-Za-z0-9]{22})", uri or "")
  if m:
    CACHE.mkdir(parents=True, exist_ok=True)
    LAST_URI.write_text(f"spotify:{m.group(1)}:{m.group(2)}\n")
  return 0


def main(argv):
  cmd = argv[1] if len(argv) > 1 else "status"
  arg = argv[2] if len(argv) > 2 else ""
  if cmd == "auth":
    return auth(arg) if arg else 2
  if cmd == "local-play":
    return local_play()
  if cmd == "remember":
    return remember(arg)
  if cmd == "status":
    status()
  elif cmd == "library":
    library()
  else:
    return action(cmd, arg)
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
