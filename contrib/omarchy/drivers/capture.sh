#!/usr/bin/env bash
# Regenerate manifest.json (structure preserved) and etc/ from this system. No root; writes only in the repo
# (plus --state <dir>).
# Usage: capture.sh [--state DIR] [--no-repo] [--quiet]
#   --state DIR  also write DIR/drivers-state.json (exact versions, DKMS, kernel, loaded driver, CUDA, cmdline, BIOS)
#   --no-repo    leave manifest.json and etc/ untouched (used by stable-snapshot.sh)
# Env (tests): ROOT (prefix of /etc, /usr paths), MANIFEST, ETC_DIR, PROC_ROOT, SYSFS_ROOT, KERNEL_RELEASE.
set -uo pipefail
export LC_ALL=C
HERE=$(cd "$(dirname "$0")" && pwd)
export MANIFEST=${MANIFEST:-$HERE/manifest.json} ETC_DIR=${ETC_DIR:-$HERE/etc} ROOT=${ROOT:-}
export PROC_ROOT=${PROC_ROOT:-/proc} SYSFS_ROOT=${SYSFS_ROOT:-/sys}
STATE=""; NOREPO=0; QUIET=0
while [ $# -gt 0 ]; do
    case "$1" in
        --state) [ $# -ge 2 ] || { echo "uso: $0 [--state DIR] [--no-repo]" >&2; exit 3; }; STATE=$2; shift ;;
        --no-repo) NOREPO=1 ;;
        --quiet) QUIET=1 ;;
        -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
        *) echo "uso: $0 [--state DIR] [--no-repo]" >&2; exit 3 ;;
    esac
    shift
done
export STATE NOREPO QUIET

python3 - <<'PY'
import difflib, json, os, re, subprocess, sys

MANIFEST, ETC_DIR, ROOT = os.environ["MANIFEST"], os.environ["ETC_DIR"], os.environ["ROOT"]
STATE, NOREPO, QUIET = os.environ["STATE"], os.environ["NOREPO"] == "1", os.environ["QUIET"] == "1"
SECRET = re.compile(r"(passw(or)?d|secret|token|api[_-]?key|private[_ -]?key)\s*[=:]\s*\S", re.I)


def run(cmd):
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        return p.returncode, p.stdout
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return 127, ""


def say(*a):
    if not QUIET:
        print(*a)


def src_of(dest):
    return "etc/" + dest[len("/etc/"):] if dest.startswith("/etc/") else "etc/_root/" + dest.lstrip("/")


with open(MANIFEST) as f:
    old_text = f.read()
man = json.loads(old_text)
problems = 0

if not NOREPO:
    rc, out = run(["pacman", "-Qqm"])
    foreign = set(out.split())
    rc, out = run(["pacman", "-Qq"])
    have = set(out.split())
    for g, d in man["groups"].items():
        pk = set(d.get("repo", [])) | set(d.get("aur", []))
        for p in sorted(pk - have):
            say("AVISO: %s (grupo %s) no está instalado; se conserva en el manifiesto" % (p, g))
        d["aur"] = sorted(p for p in pk if p in foreign or (p in d.get("aur", []) and p not in have))
        d["repo"] = sorted(p for p in pk if p not in d["aur"])
    for e in man["etc_files"]:
        e["src"] = src_of(e["dest"])
        e.setdefault("mode", "644")
        e.setdefault("owner", "root:root")
    man["etc_files"].sort(key=lambda e: e["dest"])
    changed = []
    for e in man["etc_files"]:
        sysp = ROOT + e["dest"]
        dst = os.path.join(ETC_DIR, e["src"][len("etc/"):])
        try:
            with open(sysp, "rb") as f:
                data = f.read()
        except OSError:
            say("AVISO: %s no es legible o no existe; se conserva la copia del repo" % e["dest"]); continue
        if SECRET.search(data.decode("utf-8", "replace")):
            say("AVISO: %s parece contener un secreto; NO se copia" % e["dest"]); problems += 1; continue
        e["mode"] = "%o" % (os.stat(sysp).st_mode & 0o777)
        old = open(dst, "rb").read() if os.path.exists(dst) else None
        if old != data:
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            with open(dst, "wb") as f:
                f.write(data)
            changed.append("%s: %s" % (e["src"], "nuevo" if old is None else "cambiado"))
    rc, out = run(["dkms", "status"])
    mods = sorted({l.split("/")[0].strip() for l in out.splitlines() if "/" in l})
    if rc == 0 and mods:
        man["dkms"] = mods
    for k in man["services"]:
        man["services"][k] = sorted(set(man["services"][k]))
    new_text = json.dumps(man, indent=2, ensure_ascii=False) + "\n"
    if new_text != old_text:
        with open(MANIFEST, "w") as f:
            f.write(new_text)
        say("manifest.json cambió:")
        for l in difflib.unified_diff(old_text.splitlines(), new_text.splitlines(), "antes", "ahora", lineterm="", n=0):
            say("  " + l)
    for c in changed:
        say("etc: " + c)
    if new_text == old_text and not changed:
        say("sin cambios: el repo ya refleja el sistema")

if STATE:
    pk = sorted({p for d in man["groups"].values() for p in d.get("repo", []) + d.get("aur", [])})
    rc, out = run(["pacman", "-Q"])
    vers = dict(l.split(None, 1) for l in out.splitlines() if len(l.split()) == 2)
    state = {"packages": {p: vers.get(p) for p in pk}}
    rc, out = run(["dkms", "status"])
    state["dkms"] = sorted(l.strip() for l in out.splitlines() if l.strip())
    state["kernel"] = os.environ.get("KERNEL_RELEASE") or os.uname().release

    def rd(path):
        try:
            with open(path) as f:
                return f.read()
        except OSError:
            return None
    nv = rd(os.path.join(os.environ["PROC_ROOT"], "driver/nvidia/version"))
    state["nvidia_loaded"] = nv.splitlines()[0].strip() if nv else None
    state["cuda"] = vers.get("cuda")
    rc, out = run(["nvidia-smi", "--query-gpu=name,driver_version", "--format=csv,noheader"])
    state["nvidia_smi"] = out.strip() if rc == 0 else None
    cl = rd(os.path.join(os.environ["PROC_ROOT"], "cmdline"))
    state["cmdline"] = cl.strip() if cl else None
    bios = rd(os.path.join(os.environ["SYSFS_ROOT"], "class/dmi/id/bios_version"))
    state["bios"] = bios.strip() if bios else None
    os.makedirs(STATE, exist_ok=True)
    with open(os.path.join(STATE, "drivers-state.json"), "w") as f:
        json.dump(state, f, indent=2, sort_keys=True, ensure_ascii=False)
        f.write("\n")
    say("estado escrito en %s/drivers-state.json" % STATE)
sys.exit(1 if problems else 0)
PY
