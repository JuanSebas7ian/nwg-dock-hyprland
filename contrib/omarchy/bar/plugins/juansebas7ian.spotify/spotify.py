#!/usr/bin/python3
"""Spotify backend for the bar panel, on top of spotify-player. Prints JSON.

Playback on this PC is spotifyd (Spotify Connect device "Omarchy", MPRIS): it
needs no Web API, so playing, pausing, skipping and opening any playlist, album
or track (MPRIS OpenUri) keep working even when the API is rate limited.
spotify-player runs as an API-only daemon (spotify-player.service, no streaming)
and its CLI lists playlists, albums, top tracks and devices.

  spotify.py setup-state            is spotify-player installed, authenticated, using its own client ID
  spotify.py library [--force]      playlists, top tracks, saved albums (cached 10 min)
  spotify.py devices                Spotify Connect devices
  spotify.py local-play             play on this PC with nothing running (daemon, connect, resume)
  spotify.py play-uri spotify:<playlist|album|artist|track>:<id>   on this PC (spotifyd)
  spotify.py device <name>          move playback to another device (Web API)
  spotify.py remember <uri>         last track seen (cold-start fallback)

The shared default client ID is often rate limited (HTTP 429); with the user's
own client ID (spotify-bar-setup) requests use a dedicated quota.
"""

import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

HOME = Path.home()
CONF = HOME / ".config" / "spotify-player" / "app.toml"
SP_CACHE = HOME / ".cache" / "spotify-player"
CACHE = Path(os.environ.get("XDG_CACHE_HOME") or HOME / ".cache") / "spotify-bar"
LAST_URI = CACHE / "last-uri"
DEVICE = "Omarchy"
SERVICE = "spotify-player.service"


def out(obj):
  print(json.dumps(obj, separators=(",", ":")), flush=True)


def sp(*args, timeout=25):
  """Run a spotify_player CLI command; returns (ok, stdout or error text)."""
  try:
    p = subprocess.run(["spotify_player", *args], capture_output=True, text=True, timeout=timeout, cwd=str(HOME))
  except FileNotFoundError:
    return False, "spotify-player is not installed (omarchy pkg add spotify-player)"
  except subprocess.TimeoutExpired:
    return False, "spotify-player did not answer"
  text = (p.stdout or "").strip()
  err = (p.stderr or "").strip()
  if p.returncode != 0 or text.startswith(("Bad request", "Error", "error")):
    msg = text or err or f"exit {p.returncode}"
    if "429" in msg:
      msg = "Spotify is rate limiting the shared client ID (429). Run Connect to use your own."
    return False, msg
  return True, text


def sp_json(*args):
  ok, text = sp(*args)
  if not ok:
    return None, text
  try:
    return json.loads(text), ""
  except ValueError:
    return None, text[:200]


def daemon_running():
  return subprocess.run(["systemctl", "--user", "is-active", "-q", SERVICE]).returncode == 0


def ensure_daemon():
  if daemon_running():
    return True
  subprocess.run(["systemctl", "--user", "start", SERVICE], capture_output=True)
  for _ in range(20):
    if daemon_running():
      time.sleep(1.5)  # let it register the Connect device
      return True
    time.sleep(0.5)
  return False


def own_client_id():
  try:
    return bool(re.search(r'^\s*client_id\s*=\s*"[0-9a-f]{32}"', CONF.read_text(), re.M))
  except OSError:
    return False


# ------------------------------------------------------------------ data

def image(images, want=64):
  if not images:
    return ""
  return min(images, key=lambda i: abs((i.get("width") or want) - want)).get("url", "")


def library(force):
  path = CACHE / "library.json"
  if not force:
    try:
      if time.time() - path.stat().st_mtime < 600:
        print(path.read_text())
        return
    except OSError:
      pass
  if not ensure_daemon():
    out({"error": "spotify-player daemon is not running (systemctl --user status spotify-player)"})
    return
  playlists, err1 = sp_json("get", "key", "user-playlists")
  top, err2 = sp_json("get", "key", "user-top-tracks")
  albums, err3 = sp_json("get", "key", "user-saved-albums")
  error = next((e for e in (err1, err2, err3) if e), "")
  if playlists is None and top is None and albums is None:
    try:
      cached = json.loads(path.read_text())
      cached["stale"] = error
      out(cached)
    except (OSError, ValueError):
      out({"error": error})
    return

  def track(t):
    t = t or {}
    return {"id": t.get("id", ""), "name": t.get("name", ""),
            "artist": ", ".join(a.get("name", "") for a in t.get("artists") or []),
            "image": image((t.get("album") or {}).get("images")), "duration": t.get("duration_ms", 0),
            "uri": f"spotify:track:{t.get('id', '')}"}

  data = {
    "playlists": [{"id": p.get("id"), "uri": f"spotify:playlist:{p.get('id')}", "name": p.get("name", ""), "image": image(p.get("images")),
                   "sub": f"{(p.get('tracks') or p.get('items') or {}).get('total', '')} songs · "
                          f"{(p.get('owner') or {}).get('display_name', '')}".strip(" ·")}
                  for p in (playlists or []) if p],
    "top": [track(t) for t in (top or [])][:30],
    "albums": [{"id": (a.get("album") or a).get("id"), "uri": f"spotify:album:{(a.get('album') or a).get('id')}",
                "name": (a.get("album") or a).get("name", ""),
                "image": image((a.get("album") or a).get("images")),
                "sub": ", ".join(x.get("name", "") for x in (a.get("album") or a).get("artists") or [])}
               for a in (albums or [])][:40],
    "error": error,
    "updatedAt": int(time.time()),
  }
  CACHE.mkdir(parents=True, exist_ok=True)
  path.write_text(json.dumps(data))
  out(data)


def devices():
  if not ensure_daemon():
    out({"devices": [], "error": "spotify-player daemon is not running"})
    return
  devs, err = sp_json("get", "key", "devices")
  out({"devices": [{"id": d.get("id"), "name": d.get("name"), "type": d.get("_type") or d.get("type"),
                    "active": d.get("is_active")} for d in (devs or [])], "error": err})


# ------------------------------------------------------------------ playback

def bus_names(prefix):
  out = subprocess.run(["busctl", "--user", "list", "--no-pager", "--no-legend"], capture_output=True, text=True).stdout
  return [l.split()[0] for l in out.splitlines() if l.split() and l.split()[0].startswith(prefix)]


def mpris(player, method, *args):
  return subprocess.run(["busctl", "--user", "call", player, "/org/mpris/MediaPlayer2",
                         "org.mpris.MediaPlayer2.Player", method, *args], capture_output=True, text=True)


def status_of(player):
  o = subprocess.run(["busctl", "--user", "get-property", player, "/org/mpris/MediaPlayer2",
                      "org.mpris.MediaPlayer2.Player", "PlaybackStatus"], capture_output=True, text=True).stdout
  return o.strip().split()[-1].strip('"') if o.strip() else ""


def spotifyd_player():
  """MPRIS name of spotifyd as the active device, starting and activating it if needed."""
  if not bus_names("rs.spotifyd.instance"):
    subprocess.run(["systemctl", "--user", "start", "spotifyd.service"], capture_output=True)
    for _ in range(20):
      if bus_names("rs.spotifyd.instance"):
        break
      time.sleep(0.5)
  ctl = bus_names("rs.spotifyd.instance")
  if not ctl:
    return None
  player = bus_names("org.mpris.MediaPlayer2.spotifyd")
  if player:
    return player[0]
  # TransferPlayback makes spotifyd the active device; its MPRIS player appears.
  subprocess.run(["busctl", "--user", "call", ctl[0], "/rs/spotifyd/Controls", "rs.spotifyd.Controls",
                  "TransferPlayback"], capture_output=True)
  for _ in range(16):
    player = bus_names("org.mpris.MediaPlayer2.spotifyd")
    if player:
      return player[0]
    time.sleep(0.25)
  return None


def wait_playing(player, seconds=4):
  end = time.time() + seconds
  while time.time() < end:
    if status_of(player) == "Playing":
      return True
    time.sleep(0.3)
  return False


def play_uri(uri):
  if not re.match(r"^spotify:(playlist|album|artist|track|episode|show):[A-Za-z0-9]{22}$", uri):
    out({"ok": False, "error": "not a Spotify URI"})
    return 2
  player = spotifyd_player()
  if not player:
    out({"ok": False, "error": "spotifyd is not running (systemctl --user status spotifyd)"})
    return 1
  r = mpris(player, "OpenUri", "s", uri)
  ok = r.returncode == 0 and wait_playing(player)
  out({"ok": ok, "error": "" if ok else (r.stderr.strip() or "Spotify did not start that")})
  return 0 if ok else 1


def local_play():
  """Play on this PC with nothing running: spotifyd resumes the account's session, else the last song."""
  player = spotifyd_player()
  if not player:
    out({"ok": False, "error": "spotifyd is not running (systemctl --user status spotifyd)"})
    return 1
  if wait_playing(player, 2):
    out({"ok": True, "via": "transfer"})
    return 0
  mpris(player, "Play")
  if wait_playing(player, 2):
    out({"ok": True, "via": "resume"})
    return 0
  try:
    uri = LAST_URI.read_text().strip()
  except OSError:
    uri = ""
  if uri:
    return play_uri(uri)
  out({"ok": False, "error": "Nothing to resume yet: pick a playlist or album below."})
  return 1


def act(*args):
  if not ensure_daemon():
    out({"ok": False, "error": "spotify-player is not running (systemctl --user status spotify-player)"})
    return 1
  ok, msg = sp(*args)
  out({"ok": ok, "error": "" if ok else msg})
  return 0 if ok else 1


def remember(uri):
  m = re.search(r"(track|episode)[:/]([A-Za-z0-9]{22})", uri or "")
  if m:
    CACHE.mkdir(parents=True, exist_ok=True)
    LAST_URI.write_text(f"spotify:{m.group(1)}:{m.group(2)}\n")
  return 0


def setup_state():
  installed = subprocess.run(["which", "spotify_player"], capture_output=True).returncode == 0
  out({"installed": installed,
       "authenticated": (SP_CACHE / "credentials.json").exists() and any(SP_CACHE.glob("*token.json")),
       "ownClientId": own_client_id(), "daemon": daemon_running(), "device": DEVICE})


def main(argv):
  cmd = argv[1] if len(argv) > 1 else "setup-state"
  a = argv[2:]
  if cmd == "setup-state":
    setup_state()
  elif cmd == "library":
    library("--force" in a)
  elif cmd == "devices":
    devices()
  elif cmd == "local-play":
    return local_play()
  elif cmd == "play-uri" and a:
    return play_uri(a[0])
  elif cmd == "device" and a:
    return act("connect", "--name", a[0])
  elif cmd == "remember":
    return remember(a[0] if a else "")
  else:
    out({"error": "unknown command"})
    return 2
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
