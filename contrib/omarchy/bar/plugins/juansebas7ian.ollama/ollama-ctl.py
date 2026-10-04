#!/usr/bin/python3
"""Backend for the Ollama bar panel. Every command prints JSON on stdout.

  status            installed and loaded models, disk use, free space
  search [query]    models from the ollama.com library (scraped, best effort)
  pull <model>      download; one JSON progress line per update, then {"done": true}
  rm <model>        delete a downloaded model
  unload <model>    free a loaded model's memory now

Talks to the local Ollama API ($OLLAMA_HOST, default 127.0.0.1:11434), so it
needs no root: the daemon itself writes the model files.
"""

import html
import json
import os
import re
import shutil
import sys
import time
import urllib.parse
import urllib.request

HOST = os.environ.get("OLLAMA_HOST", "http://127.0.0.1:11434")
if not HOST.startswith("http"):
  HOST = "http://" + HOST
MODELS_DIR = os.environ.get("OLLAMA_MODELS", "/var/lib/ollama")


def out(obj):
  print(json.dumps(obj, separators=(",", ":")), flush=True)


def api(path, body=None, method=None, timeout=5):
  data = json.dumps(body).encode() if body is not None else None
  req = urllib.request.Request(HOST + path, data=data, method=method or ("POST" if data else "GET"),
                               headers={"Content-Type": "application/json"})
  return urllib.request.urlopen(req, timeout=timeout)


def dir_size(path):
  total = 0
  for root, _dirs, files in os.walk(path):
    for name in files:
      try:
        total += os.lstat(os.path.join(root, name)).st_size
      except OSError:
        pass
  return total


def status():
  result = {"running": False, "version": "", "models": [], "loaded": [],
            "modelsDir": MODELS_DIR, "diskUsed": 0, "diskFree": 0, "diskTotal": 0}
  try:
    with api("/api/version") as r:
      result["version"] = json.loads(r.read()).get("version", "")
    result["running"] = True
    with api("/api/tags") as r:
      tags = json.loads(r.read()).get("models") or []
    with api("/api/ps") as r:
      loaded = json.loads(r.read()).get("models") or []
  except Exception as exc:
    result["error"] = str(exc)
    tags, loaded = [], []

  loaded_names = {m.get("name") for m in loaded}
  for m in sorted(tags, key=lambda m: m.get("name", "")):
    d = m.get("details") or {}
    result["models"].append({
      "name": m.get("name", ""), "size": m.get("size", 0),
      "family": d.get("family", ""), "params": d.get("parameter_size", ""),
      "quant": d.get("quantization_level", ""), "modified": (m.get("modified_at") or "")[:10],
      "loaded": m.get("name") in loaded_names,
    })
  for m in loaded:
    result["loaded"].append({"name": m.get("name", ""), "size": m.get("size", 0),
                             "sizeVram": m.get("size_vram", 0), "expires": m.get("expires_at", "")})

  # Shared layers are stored once, so the blobs directory is the real figure;
  # summing model sizes would count them twice.
  blobs = os.path.join(MODELS_DIR, "blobs")
  used = dir_size(blobs) if os.path.isdir(blobs) else 0
  result["diskUsed"] = used or sum(m["size"] for m in result["models"])
  try:
    usage = shutil.disk_usage(MODELS_DIR if os.path.isdir(MODELS_DIR) else os.path.expanduser("~"))
    result["diskFree"], result["diskTotal"] = usage.free, usage.total
  except OSError:
    pass
  out(result)


def search(query):
  url = "https://ollama.com/search?" + urllib.parse.urlencode({"q": query} if query else {})
  try:
    req = urllib.request.Request(url, headers={"User-Agent": "omarchy-ollama-panel"})
    with urllib.request.urlopen(req, timeout=10) as r:
      page = r.read().decode("utf-8", "replace")
  except Exception as exc:
    out({"results": [], "error": str(exc), "url": url})
    return
  results = []
  for block in page.split('href="/library/')[1:]:
    m = re.match(r'([^"]+)"\s+class="group', block)
    if not m:
      continue
    name = m.group(1)
    chunk = block[:4000]
    desc = re.search(r"<p[^>]*>([^<]*)</p>", chunk)
    tags = re.findall(r'<span[^>]*class="[^"]*text-(indigo|blue)-600[^"]*"[^>]*>\s*([^<]+?)\s*</span>', chunk)
    pulls = re.search(r"<span\s*>([\d.,]+[KMB]?)</span>\s*<span[^>]*>&nbsp;Pulls", chunk)
    results.append({
      "name": name,
      "description": html.unescape(desc.group(1).strip()) if desc else "",
      "capabilities": [t for c, t in tags if c == "indigo"],
      "sizes": [t for c, t in tags if c == "blue"],
      "pulls": pulls.group(1) if pulls else "",
    })
    if len(results) >= 30:
      break
  out({"results": results, "url": url})


def pull(model):
  last = 0.0
  try:
    with api("/api/pull", {"model": model, "stream": True}, timeout=None) as r:
      for raw in r:
        line = json.loads(raw)
        if line.get("error"):
          out({"error": line["error"], "model": model})
          return 1
        now = time.monotonic()
        total, done = line.get("total") or 0, line.get("completed") or 0
        if line.get("status") == "success":
          out({"done": True, "model": model})
          return 0
        if now - last >= 0.5:
          last = now
          out({"model": model, "status": line.get("status", ""), "total": total, "completed": done})
  except Exception as exc:
    out({"error": str(exc), "model": model})
    return 1
  out({"done": True, "model": model})
  return 0


def remove(model):
  try:
    api("/api/delete", {"model": model}, method="DELETE").close()
    out({"ok": True})
  except Exception as exc:
    out({"ok": False, "error": str(exc)})


def unload(model):
  try:
    api("/api/generate", {"model": model, "keep_alive": 0}, timeout=30).close()
    out({"ok": True})
  except Exception as exc:
    out({"ok": False, "error": str(exc)})


def main(argv):
  cmd = argv[1] if len(argv) > 1 else "status"
  arg = argv[2] if len(argv) > 2 else ""
  if cmd == "status":
    status()
  elif cmd == "search":
    search(arg)
  elif cmd == "pull" and arg:
    return pull(arg)
  elif cmd == "rm" and arg:
    remove(arg)
  elif cmd == "unload" and arg:
    unload(arg)
  else:
    out({"error": "usage: ollama-ctl.py status|search [q]|pull <m>|rm <m>|unload <m>"})
    return 2
  return 0


if __name__ == "__main__":
  sys.exit(main(sys.argv))
