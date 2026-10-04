#!/usr/bin/env python3
"""Compatibility validator for driver updates (see PLAN.md). Python stdlib only, no root.

  compat.py preflight [--from-file F] [--json]   will the pending updates break drivers or apps?
  compat.py post [--json]                         does everything work right now?
  compat.py hook                                  pacman PreTransaction hook: targets on stdin, never fails

Output: "LEVEL ID text" per rule (OK, WARN, FAIL, SKIP). Exit 0 all OK, 1 warnings, 2 something would break.
Env: BOOT_PATH, ROOT_PATH, PROC_ROOT, STABLE_DIR (fakes for tests).
"""
import ctypes
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import urllib.request

OK, WARN, FAIL, SKIP = "OK", "WARN", "FAIL", "SKIP"
# Minimum NVIDIA driver (major branch) per CUDA major.
CUDA_MIN_DRIVER = {12: 525, 13: 580}
KERNEL_TRIGGERS = ("linux", "amd-ucode")
SESSION_PKGS = ("hyprland", "quickshell")
BOOT_MIN_MB = 200
ROOT_MIN_GB = 5
ELF_DIRS = ("/usr/bin/", "/usr/lib/", "/opt/")  # where C12 looks for binaries of AUR packages

FAMILIES = [  # id, level, members
    ("C01", FAIL, ["nvidia-utils", "lib32-nvidia-utils", "opencl-nvidia", "nvidia-open-dkms"]),
    ("C02", FAIL, ["linux", "linux-headers"]),
    ("C07", FAIL, ["mesa", "lib32-mesa", "vulkan-radeon", "lib32-vulkan-radeon"]),
    ("C08a", WARN, ["vulkan-icd-loader", "lib32-vulkan-icd-loader"]),
    ("C08b", WARN, ["gamemode", "lib32-gamemode"]),
    ("C08c", WARN, ["mangohud", "lib32-mangohud"]),
]


def upstream(v):
    """Version without the pkgrel: '1:26.2.2-1' -> '1:26.2.2'."""
    return v.rsplit("-", 1)[0] if v else v


def major_minor(v):
    m = re.match(r"(?:\d+:)?(\d+)\.(\d+)", v or "")
    return (int(m.group(1)), int(m.group(2))) if m else None


def major(v):
    m = re.match(r"(?:\d+:)?(\d+)", v or "")
    return int(m.group(1)) if m else None


def run(cmd, timeout=15, env=None):
    """(rc, stdout+stderr); rc 127 if the binary is missing, 124 on timeout."""
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout,
                           env={**os.environ, "LC_ALL": "C", **(env or {})})
        return p.returncode, p.stdout + p.stderr
    except FileNotFoundError:
        return 127, ""
    except subprocess.TimeoutExpired:
        return 124, ""


class Ctx:
    """System snapshot the rules work on; tests build it directly."""

    def __init__(self, installed, pending=None, kernel_pending=None):
        self.installed = dict(installed)
        self.pending = dict(pending or {})
        self.final = {**self.installed, **self.pending}
        self.boot_free_mb = None
        self.root_free_gb = None
        self.dkms_pkgs = sorted(n for n in self.installed if n.endswith("-dkms") and not n.startswith("nvidia"))
        # post-only collaborators (injectable)
        self.run = run
        self.cuinit = cuinit
        self.ollama_up = ollama_up
        self.aur = []
        self.pacnew = []
        self.proc_root = os.environ.get("PROC_ROOT", "/proc")
        self.kernel = os.uname().release
        self.has_session = bool(os.environ.get("HYPRLAND_INSTANCE_SIGNATURE"))

    def changed(self, name):
        return name in self.pending and upstream(self.pending[name]) != upstream(self.installed.get(name))


# ---------- preflight rules: ctx -> [(level, id, text)] ----------

def rule_families(ctx):
    out = []
    for rid, level, members in FAMILIES:
        have = [m for m in members if m in ctx.final]
        if len(have) < 2:
            continue
        vers = {m: upstream(ctx.final[m]) for m in have}
        if len(set(vers.values())) == 1:
            out.append((OK, rid, "%s: misma versión %s" % (", ".join(have), next(iter(vers.values())))))
        else:
            detail = ", ".join("%s %s" % (m, vers[m]) for m in have)
            out.append((level, rid, "versiones desparejas tras la actualización: %s" % detail))
    return out


def rule_c03(ctx):
    if not ctx.changed("linux"):
        return [(OK, "C03", "el kernel no cambia")]
    if "nvidia-open-dkms" not in ctx.final:
        return [(OK, "C03", "sin nvidia-open-dkms")]
    if ctx.changed("nvidia-open-dkms"):
        return [(OK, "C03", "kernel y nvidia-open-dkms cambian juntos; DKMS recompila")]
    old, new = major_minor(ctx.installed.get("linux")), major_minor(ctx.final.get("linux"))
    if old != new:
        return [(WARN, "C03", "salto de kernel %s -> %s sin nuevo nvidia-open-dkms %s: el módulo DKMS podría no compilar"
                 % (ctx.installed.get("linux"), ctx.final.get("linux"), ctx.final["nvidia-open-dkms"]))]
    return [(OK, "C03", "kernel con el mismo %d.%d; DKMS recompilará nvidia" % new)]


def rule_c04(ctx):
    if not ctx.dkms_pkgs:
        return [(OK, "C04", "sin otros módulos DKMS")]
    if ctx.changed("linux"):
        return [(WARN, "C04", "el kernel cambia: tras actualizar revisa `dkms status` (%s)" % ", ".join(ctx.dkms_pkgs))]
    return [(OK, "C04", "el kernel no cambia; módulos DKMS: %s" % ", ".join(ctx.dkms_pkgs))]


def rule_c05(ctx):
    cuda, drv = ctx.final.get("cuda"), ctx.final.get("nvidia-utils")
    if not cuda or not drv:
        return [(OK, "C05", "sin cuda o sin nvidia-utils")]
    cm, dm = major(cuda), major(drv)
    need = CUDA_MIN_DRIVER.get(cm)
    if need is None:
        return [(WARN, "C05", "CUDA %s: sin dato de driver mínimo en la tabla" % cuda)]
    if dm is None or dm < need:
        return [(FAIL, "C05", "CUDA %s exige driver >= %d y quedaría %s" % (cuda, need, drv))]
    return [(OK, "C05", "CUDA %s con driver %s (mínimo %d)" % (cuda, drv, need))]


def rule_c06(ctx):
    if "cuda" not in ctx.installed:
        return [(OK, "C06", "cuda no instalado")]
    if major(ctx.final.get("cuda")) == major(ctx.installed.get("cuda")):
        return [(OK, "C06", "cuda mantiene su versión mayor")]
    stale = [n for n in ("cudnn", "ollama-cuda") if n in ctx.installed and not ctx.changed(n)]
    if stale:
        return [(WARN, "C06", "cuda cambia a %s y no se actualizan: %s" % (ctx.final["cuda"], ", ".join(stale)))]
    return [(OK, "C06", "cuda cambia a %s y cudnn/ollama-cuda lo acompañan" % ctx.final["cuda"])]


def _kernelish(n):
    return n in KERNEL_TRIGGERS or n.startswith("linux-firmware")


def rule_c09(ctx):
    hit = sorted(n for n in ctx.pending if _kernelish(n) and ctx.changed(n))
    if not hit:
        return [(OK, "C09", "ninguna actualización toca /boot")]
    if ctx.boot_free_mb is None:
        return [(WARN, "C09", "no se pudo medir /boot")]
    if ctx.boot_free_mb < BOOT_MIN_MB:
        return [(FAIL, "C09", "/boot tiene %d MB libres (< %d) y cambian: %s" % (ctx.boot_free_mb, BOOT_MIN_MB, ", ".join(hit)))]
    return [(OK, "C09", "/boot con %d MB libres (>= %d)" % (ctx.boot_free_mb, BOOT_MIN_MB))]


def rule_c10(ctx):
    if not ctx.pending:
        return [(OK, "C10", "sin actualizaciones pendientes")]
    if ctx.root_free_gb is None:
        return [(WARN, "C10", "no se pudo medir /")]
    if ctx.root_free_gb < ROOT_MIN_GB:
        return [(WARN, "C10", "/ tiene %.1f GB libres (< %d)" % (ctx.root_free_gb, ROOT_MIN_GB))]
    return [(OK, "C10", "/ con %.0f GB libres (>= %d)" % (ctx.root_free_gb, ROOT_MIN_GB))]


def rule_c11(ctx):
    hit = sorted(n for n in ctx.pending if ctx.changed(n) and
                 (n in SESSION_PKGS or n == "omarchy" or n.startswith("omarchy-")))
    if not hit:
        return [(OK, "C11", "ni Hyprland, Quickshell ni Omarchy cambian")]
    return [(WARN, "C11", "cambian %s: reinicia la sesión y ejecuta `omarchy restart shell` (plugins propios de la barra)"
             % ", ".join(hit))]


PREFLIGHT_RULES = [rule_families, rule_c03, rule_c04, rule_c05, rule_c06, rule_c09, rule_c10, rule_c11]


# ---------- post rules ----------

def cuinit():
    """cuInit(0) via ctypes; returns the CUresult (0 = OK) or -1 if libcuda is missing."""
    try:
        return int(ctypes.CDLL("libcuda.so.1").cuInit(0))
    except OSError:
        return -1


def ollama_up():
    try:
        with urllib.request.urlopen("http://127.0.0.1:11434/", timeout=3) as r:
            return r.status == 200
    except Exception:
        return False


def rule_c12(ctx):
    if not ctx.aur:
        return [(OK, "C12", "sin paquetes de AUR")]
    bad, scanned = [], 0
    for pkg in ctx.aur:
        rc, out = ctx.run(["pacman", "-Ql", pkg])
        for line in out.splitlines():
            path = line.split(" ", 1)[-1]
            if not (path.startswith(ELF_DIRS) and os.path.isfile(path)):
                continue
            try:
                with open(path, "rb") as f:
                    if f.read(4) != b"\x7fELF":
                        continue
            except OSError:
                continue
            if scanned >= 300:
                break
            scanned += 1
            rc, ldd = ctx.run(["ldd", path], timeout=10)
            miss = [l.split("=>")[0].strip() for l in ldd.splitlines() if "not found" in l]
            if miss:
                bad.append("%s (%s): %s" % (pkg, os.path.basename(path), ", ".join(sorted(set(miss)))))
    if bad:
        return [(FAIL, "C12", "librerías sin resolver en AUR: " + "; ".join(sorted(bad)[:5]))]
    return [(OK, "C12", "%d binarios de %d paquetes de AUR resuelven sus librerías" % (scanned, len(ctx.aur)))]


def rule_c13(ctx):
    out = []
    # loaded driver vs package
    try:
        with open(os.path.join(ctx.proc_root, "driver/nvidia/version")) as f:
            text = f.read()
    except OSError:
        text = ""
    m = re.search(r"\b(\d+\.\d+(?:\.\d+)?)\b", text.splitlines()[0] if text else "")
    pkg = ctx.installed.get("nvidia-utils")
    if not m:
        out.append((FAIL, "C13a", "el driver nvidia no está cargado (/proc/driver/nvidia/version)"))
    elif pkg and upstream(pkg) != m.group(1):
        out.append((FAIL, "C13a", "driver cargado %s != nvidia-utils %s: reinicia" % (m.group(1), upstream(pkg))))
    else:
        out.append((OK, "C13a", "driver cargado %s = nvidia-utils" % m.group(1)))
    # DKMS for the running kernel
    rc, dk = ctx.run(["dkms", "status"])
    bad = []
    for line in dk.splitlines():
        mm = re.match(r"([^,]+), ([^,]+), [^:]+: (.*)", line.strip())
        if mm and mm.group(2) == ctx.kernel and not (mm.group(3).startswith("installed") and "WARNING" not in mm.group(3)):
            bad.append("%s: %s" % (mm.group(1), mm.group(3)))
    if rc == 127:
        out.append((WARN, "C13b", "dkms no está instalado"))
    elif bad:
        out.append((FAIL, "C13b", "DKMS no instalado para %s: %s" % (ctx.kernel, "; ".join(bad))))
    else:
        out.append((OK, "C13b", "todos los módulos DKMS instalados para %s" % ctx.kernel))
    rc, o = ctx.run(["nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader"], timeout=10)
    out.append((OK, "C13c", "nvidia-smi responde (%s)" % o.strip().splitlines()[0]) if rc == 0 and o.strip()
               else (FAIL, "C13c", "nvidia-smi no responde (rc=%d)" % rc))
    r = ctx.cuinit()
    out.append((OK, "C13d", "cuInit = 0") if r == 0 else (FAIL, "C13d", "cuInit devolvió %s" % r))
    rc, o = ctx.run(["vulkaninfo", "--summary"], timeout=20)
    low = o.lower()
    if rc == 0 and "nvidia" in low and "radv" in low:
        out.append((OK, "C13e", "vulkaninfo lista NVIDIA y RADV"))
    else:
        out.append((FAIL, "C13e", "vulkaninfo no lista NVIDIA y RADV (rc=%d)" % rc))
    rc, o = ctx.run(["vainfo"], timeout=20)
    low = o.lower()
    out.append((OK, "C13f", "vainfo con NVDEC") if "nvdec" in low or ("nvidia" in low and "vaprofile" in low)
               else (FAIL, "C13f", "vainfo sin NVDEC (rc=%d)" % rc))
    if "ollama" in ctx.installed:
        out.append((OK, "C13g", "ollama responde en 127.0.0.1:11434") if ctx.ollama_up()
                   else (FAIL, "C13g", "ollama no responde en 127.0.0.1:11434"))
    if ctx.has_session:
        rc, o = ctx.run(["hyprctl", "configerrors"])
        out.append((OK, "C13h", "hyprctl configerrors vacío") if rc == 0 and not o.strip()
                   else (FAIL, "C13h", "hyprctl configerrors: %s" % (o.strip().splitlines() or ["rc=%d" % rc])[0]))
    else:
        out.append((SKIP, "C13h", "sin sesión de Hyprland"))
    failed = []
    for cmd in (["systemctl", "--failed", "--no-legend", "--plain"], ["systemctl", "--user", "--failed", "--no-legend", "--plain"]):
        rc, o = ctx.run(cmd)
        failed += [l.split()[0] for l in o.splitlines() if l.split()]
    out.append((OK, "C13i", "sin servicios fallidos") if not failed
               else (FAIL, "C13i", "servicios fallidos: %s" % " ".join(sorted(failed))))
    return out


def rule_c14(ctx):
    if not ctx.pacnew:
        return [(OK, "C14", "sin .pacnew nuevos en /etc")]
    return [(WARN, "C14", ".pacnew nuevos: %s" % ", ".join(sorted(ctx.pacnew)[:8]))]


POST_RULES = [rule_c12, rule_c13, rule_c14]


# ---------- gathering ----------

def read_installed():
    rc, out = run(["pacman", "-Q"])
    inst = {}
    for line in out.splitlines():
        p = line.split()
        if len(p) == 2:
            inst[p[0]] = p[1]
    return inst


def parse_updates(text):
    pend = {}
    for line in text.splitlines():
        m = re.match(r"(\S+) (\S+) -> (\S+)", line.strip())
        if m:
            pend[m.group(1)] = m.group(3)
    return pend


def fill_space(ctx):
    for attr, env, default, div in (("boot_free_mb", "BOOT_PATH", "/boot", 1 << 20), ("root_free_gb", "ROOT_PATH", "/", 1 << 30)):
        try:
            setattr(ctx, attr, shutil.disk_usage(os.environ.get(env, default)).free / div)
        except OSError:
            setattr(ctx, attr, None)
    if ctx.boot_free_mb is not None:
        ctx.boot_free_mb = int(ctx.boot_free_mb)


def find_pacnew():
    stable = os.environ.get("STABLE_DIR", os.path.expanduser("~/.local/state/omarchy-stable"))
    ref = 0.0
    try:
        for d in sorted(os.listdir(stable)):
            p = os.path.join(stable, d, "snapshots.txt")
            if os.path.exists(p):
                ref = max(ref, os.path.getmtime(p))
    except OSError:
        pass
    found = []
    for root, dirs, files in os.walk(os.environ.get("ETC_PATH", "/etc")):
        for f in files:
            if f.endswith(".pacnew"):
                p = os.path.join(root, f)
                try:
                    if os.path.getmtime(p) > ref:
                        found.append(p)
                except OSError:
                    pass
    return sorted(found)


def evaluate(ctx, rules):
    res = []
    for r in rules:
        res.extend(r(ctx))
    return res


def report(res, as_json):
    if as_json:
        print(json.dumps({"checks": [{"id": i, "status": l, "text": t} for l, i, t in res]}, ensure_ascii=False, indent=1))
    else:
        for l, i, t in res:
            print("%s %s %s" % (l, i, t))
    if any(l == FAIL for l, _, _ in res):
        return 2
    return 1 if any(l == WARN for l, _, _ in res) else 0


def cmd_preflight(args):
    if "--from-file" in args:
        text = open(args[args.index("--from-file") + 1]).read()
    else:
        rc, text = run(["checkupdates"], timeout=120)
        if rc not in (0, 2):
            print("FAIL C00 checkupdates falló (rc=%d)" % rc)
            return 2
    ctx = Ctx(read_installed(), parse_updates(text))
    fill_space(ctx)
    if not ctx.pending:
        print("OK C00 sin actualizaciones pendientes", file=sys.stderr)
    return report(evaluate(ctx, PREFLIGHT_RULES), "--json" in args)


def cmd_post(args):
    inst = read_installed()
    ctx = Ctx(inst)
    rc, out = run(["pacman", "-Qqm"])
    ctx.aur = sorted(out.split()) if rc == 0 else []
    ctx.pacnew = find_pacnew()
    return report(evaluate(ctx, POST_RULES), "--json" in args)


def cmd_hook(_args):
    """Never raises, never returns non-zero, prints only warnings plus one summary line."""
    signal.signal(signal.SIGALRM, lambda *a: (_ for _ in ()).throw(TimeoutError()))
    signal.alarm(4)
    try:
        names = sorted({l.strip() for l in sys.stdin if l.strip()})
        if not names:
            return 0
        inst = read_installed()
        rc, out = run(["pacman", "-Si"] + names, timeout=3)
        pend, cur = {}, None
        for line in out.splitlines():
            m = re.match(r"(Name|Version)\s*:\s*(\S+)", line)
            if m and m.group(1) == "Name":
                cur = m.group(2)
            elif m and cur:
                if inst.get(cur) != m.group(2):
                    pend[cur] = m.group(2)
                cur = None
        ctx = Ctx(inst, pend)
        fill_space(ctx)
        res = [r for r in evaluate(ctx, [r for r in PREFLIGHT_RULES if r is not rule_c10]) if r[0] in (WARN, FAIL)]
        for l, i, t in res:
            print("[omarchy-compat] %s %s: %s" % ("ROMPERÍA" if l == FAIL else "aviso", i, t))
        print("[omarchy-compat] %d paquetes revisados, %d avisos (solo informativo; no detiene la transacción)"
              % (len(names), len(res)))
    except BaseException:
        pass
    finally:
        signal.alarm(0)
    return 0


def main(argv):
    if len(argv) < 2 or argv[1] not in ("preflight", "post", "hook"):
        print(__doc__, file=sys.stderr)
        return 3
    if argv[1] == "hook":
        try:
            return cmd_hook(argv[2:])
        except BaseException:
            return 0
    return {"preflight": cmd_preflight, "post": cmd_post}[argv[1]](argv[2:])


if __name__ == "__main__":
    sys.exit(main(sys.argv))
