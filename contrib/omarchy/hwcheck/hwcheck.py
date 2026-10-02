#!/usr/bin/env python3
"""omarchy-hwcheck: validador determinista de drivers, actualizaciones y conflictos para Omarchy.

Solo lee el sistema: no necesita root y no cambia nada (salvo guardar la línea base con `baseline`).
El mismo estado del sistema produce siempre el mismo informe: todo se ordena, no hay marcas de tiempo
y los comandos se ejecutan con LC_ALL=C. La única entrada que depende de la red es `scan --online`.

Uso:
  omarchy-hwcheck [scan] [--online] [--json] [--only hw,updates,conflicts]
  omarchy-hwcheck baseline [--force]    guarda el estado actual como referencia
  omarchy-hwcheck diff [--json]         compara el estado actual con la referencia
  omarchy-hwcheck state [--json]        muestra el estado normalizado que se compara

Códigos de salida: 0 todo bien (o solo informativo), 1 hay avisos, 2 hay fallos o regresiones,
3 error de uso.
"""

import json
import os
import re
import shlex
import subprocess
import sys

VERSION = "1.0.0"

OK, INFO, WARN, FAIL = "ok", "info", "warn", "fail"
LEVEL_RANK = {OK: 0, INFO: 0, WARN: 1, FAIL: 2}
LEVEL_TAG = {OK: "[ OK ]", INFO: "[INFO]", WARN: "[WARN]", FAIL: "[FAIL]"}
CATEGORIES = [("hw", "Hardware y firmware"), ("updates", "Actualizaciones"), ("conflicts", "Conflictos")]

# Clases PCI que normalmente no tienen driver: puentes de host, puente ISA, IOMMU.
PCI_CLASSES_WITHOUT_DRIVER = ("0x0600", "0x0601", "0x0806")

# Módulo cargado → paquete de firmware que necesita (los firmwares de Arch están divididos).
FIRMWARE_PACKAGES = [
    (r"amdgpu", "linux-firmware-amdgpu"),
    (r"radeon", "linux-firmware-radeon"),
    (r"mt76.*|mt79.*|btmtk", "linux-firmware-mediatek"),
    (r"iwlwifi|iwlmvm|btintel|i915|xe", "linux-firmware-intel"),
    (r"r8169|rtw8.*|rtw88_.*|rtw89_.*|btrtl", "linux-firmware-realtek"),
    (r"ath1[012]k.*|ath9k_htc", "linux-firmware-atheros"),
    (r"brcmfmac|brcmsmac|btbcm", "linux-firmware-broadcom"),
    (r"nouveau", "linux-firmware-nvidia"),
    (r"snd_sof.*", "sof-firmware"),
]

# Paquetes que no deben estar instalados a la vez.
CONFLICTING_PACKAGES = [
    ("nvidia", "nvidia-open"),
    ("nvidia", "nvidia-open-dkms"),
    ("nvidia-dkms", "nvidia-open-dkms"),
    ("nvidia-open", "nvidia-open-dkms"),
    ("nvidia-dkms", "nvidia"),
    ("pulseaudio", "pipewire-pulse"),
    ("jack2", "pipewire-jack"),
]

# Servicios que compiten por el mismo recurso si están habilitados a la vez.
CONFLICTING_SERVICES = [
    ("tlp.service", "power-profiles-daemon.service"),
    ("iwd.service", "wpa_supplicant.service"),
    ("NetworkManager.service", "systemd-networkd.service"),
]

# Paquetes cuya versión forma parte del estado de drivers.
DRIVER_PACKAGE_RE = re.compile(
    r"^(linux(-lts|-zen|-hardened|-omarchy|-t2)?(-headers)?|linux-firmware.*|nvidia.*|lib32-nvidia.*|"
    r".*-dkms|dkms|mesa|lib32-mesa|vulkan-.*|lib32-vulkan-.*|bluez.*|amd-ucode|intel-ucode|"
    r"sof-firmware|alsa-firmware|pipewire|wireplumber|fwupd)$"
)

MODPROBE_DIRS = ["/etc/modprobe.d", "/run/modprobe.d", "/usr/local/lib/modprobe.d", "/usr/lib/modprobe.d"]
LIMINE_CMDLINE_FILES = ["/etc/default/limine"]
LIMINE_DROPIN_DIR = "/etc/limine-entry-tool.d"
UPDATE_LOG = "/tmp/omarchy-update.log"
REPLACED_DIR = "/var/lib/omarchy/replaced"
BT_REFERENCE = "~/.claude/skills/omarchy-hardware/references/bluetooth-mt7925.md"


# --------------------------------------------------------------------------------------------------
# Acceso al sistema. Las comprobaciones solo usan esta clase, así las pruebas pueden darle datos fijos.


class System:
    def __init__(self, home=None):
        self.home = home or os.path.expanduser("~")
        self._cache = {}

    def read(self, path):
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                return f.read()
        except OSError:
            return None

    def listdir(self, path):
        try:
            return sorted(os.listdir(path))
        except OSError:
            return []

    def exists(self, path):
        return os.path.lexists(path)

    def readlink(self, path):
        try:
            return os.readlink(path)
        except OSError:
            return None

    def walk_files(self, top, suffixes):
        found = []
        for root, dirs, files in os.walk(top, onerror=lambda e: None):
            dirs[:] = [d for d in dirs if d != ".git"]
            found.extend(os.path.join(root, f) for f in files if f.endswith(suffixes))
        return sorted(found)

    def run(self, *cmd, timeout=60):
        """Devuelve (código, salida estándar). Código 127 si el comando no existe."""
        key = cmd
        if key not in self._cache:
            env = dict(os.environ, LC_ALL="C", LANG="C", SYSTEMD_COLORS="0", SYSTEMD_PAGER="")
            try:
                p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=env,
                                   errors="replace")
                self._cache[key] = (p.returncode, p.stdout)
            except FileNotFoundError:
                self._cache[key] = (127, "")
            except subprocess.TimeoutExpired:
                self._cache[key] = (124, "")
        return self._cache[key]


# --------------------------------------------------------------------------------------------------
# Lectura y normalización (funciones puras sobre el texto que devuelve System)


def norm_mod(name):
    return name.replace("-", "_")


def upstream_version(version):
    """'1:26.2.3-1' → '26.2.3'; '610.57.04-1' → '610.57.04'."""
    v = version.split(":", 1)[-1]
    return v.rsplit("-", 1)[0] if "-" in v else v


def parse_pacman_q(text):
    pkgs = {}
    for line in text.splitlines():
        parts = line.split()
        if len(parts) == 2:
            pkgs[parts[0]] = parts[1]
    return pkgs


def parse_cmdline(text):
    return (text or "").split()


def cmdline_params(tokens):
    """Tokens 'clave=valor' → {clave: valor} (el último gana, como en el kernel)."""
    params = {}
    for t in tokens:
        if "=" in t:
            k, v = t.split("=", 1)
            params[k] = v
    return params


_KCMD_RE = re.compile(r'^\s*KERNEL_CMDLINE\[default\]\s*(\+?=)\s*"([^"]*)"')


def parse_limine_cmdline(text, tokens=None):
    """Tokens de KERNEL_CMDLINE[default]: `+=` agrega, `=` reemplaza lo anterior (como en bash)."""
    tokens = list(tokens or [])  # lo que ya traían los archivos anteriores
    for line in (text or "").splitlines():
        m = _KCMD_RE.match(line)
        if m:
            if m.group(1) == "=":
                tokens = []
            tokens.extend(m.group(2).split())
    return tokens


def parse_modprobe(text):
    """Devuelve ([(módulo, parámetro, valor)], [módulos en lista negra])."""
    options, blacklist = [], []
    joined = re.sub(r"\\\n", " ", text or "")
    for raw in joined.splitlines():
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        words = line.split()
        if words[0] == "options" and len(words) >= 3:
            mod = norm_mod(words[1])
            for w in words[2:]:
                k, _, v = w.partition("=")
                options.append((mod, k, v))
        elif words[0] == "blacklist" and len(words) == 2:
            blacklist.append(norm_mod(words[1]))
    return options, blacklist


def parse_dkms_status(text):
    """'mod/ver, kernel, arch: status' → [(mod, ver, kernel o '', status)]."""
    rows = []
    for line in (text or "").splitlines():
        m = re.match(r"^([^/,]+)/([^,:]+)(?:,\s*([^,]+),\s*[^:]+)?:\s*(.+)$", line.strip())
        if m:
            rows.append((m.group(1), m.group(2), m.group(3) or "", m.group(4).strip()))
    return sorted(rows)


def parse_lspci_mm(text):
    names = {}
    for line in (text or "").splitlines():
        try:
            f = shlex.split(line)
        except ValueError:
            continue
        if len(f) >= 4:
            names[f[0]] = f"{f[1]}: {f[2]} {f[3]}"
    return names


# "usb 6-11:" (dispositivo), "usb usb6-port11:" (puerto raíz) y "usb 3-2-port4:" (puerto de un hub) → "6-11"/"3-2.4"
_USB_KEY_RE = re.compile(r"^usb (?:usb(\d+)-port(\d+)|(\d+-[\d.]+)-port(\d+)|(\d+-[\d.]+)):")
_USB_ERR_RE = re.compile(
    r"Timeout while waiting for setup device|device descriptor read|unable to enumerate|"
    r"device not accepting address|Cannot enable\. Maybe the USB cable is bad|error -(71|110|62)\b"
)


def usb_enum_errors(kernel_log):
    """Errores de enumeración USB agrupados por dispositivo ('6-11')."""
    counts = {}
    for line in kernel_log.splitlines():
        m = _USB_KEY_RE.match(line)
        if m and _USB_ERR_RE.search(line):
            if m.group(5):
                key = m.group(5)
            elif m.group(3):
                key = f"{m.group(3)}.{m.group(4)}"
            else:
                key = f"{m.group(1)}-{m.group(2)}"
            counts[key] = counts.get(key, 0) + 1
    return dict(sorted(counts.items()))


_FW_RE = re.compile(r"Direct firmware load for (\S+) failed|failed to load firmware[: ]+(\S+)?", re.I)


# Fallos que no indican un problema: la base regulatoria (cfg80211 usa la integrada) y los drivers que
# prueban varias versiones de firmware hasta encontrar una (iwlwifi).
_FW_IGNORED_RE = re.compile(r"^regulatory\.db|^iwlwifi-.*\.ucode$")


def firmware_failures(kernel_log):
    files = set()
    for line in kernel_log.splitlines():
        m = _FW_RE.search(line)
        if m:
            name = m.group(1) or m.group(2) or line.strip()
            if not _FW_IGNORED_RE.search(name):
                files.add(name)
    return sorted(files)


def bool_norm(value):
    v = value.strip().lower()
    if v in ("y", "1", "true", "on", "yes"):
        return "Y"
    if v in ("n", "0", "false", "off", "no"):
        return "N"
    return value.strip()


def same_param_value(expected, actual):
    a = actual.strip()
    if a in ("Y", "N"):
        return bool_norm(expected) == a
    try:
        return int(expected, 0) == int(a, 0)
    except ValueError:
        return expected.strip() == a


# --------------------------------------------------------------------------------------------------
# Recolección del estado


class Facts:
    """Todo lo que leen las comprobaciones, leído una sola vez."""

    def __init__(self, sys_):
        self.sys = sys_
        s = sys_
        self.release = (s.read("/proc/sys/kernel/osrelease") or "").strip()
        self.cmdline = parse_cmdline(s.read("/proc/cmdline"))
        self.loaded = sorted({norm_mod(l.split()[0]) for l in (s.read("/proc/modules") or "").splitlines() if l})
        self.builtin = sorted({
            norm_mod(os.path.basename(l.strip())[:-3])
            for l in (s.read(f"/usr/lib/modules/{self.release}/modules.builtin") or "").splitlines()
            if l.strip().endswith(".ko")
        })
        rc, out = s.run("pacman", "-Q")
        self.packages = parse_pacman_q(out) if rc == 0 else {}
        rc, out = s.run("journalctl", "-k", "-b", "-o", "cat", "--no-pager", timeout=120)
        self.kernel_log = out if rc == 0 else ""
        self.kernel_log_ok = rc == 0 and bool(out.strip())
        self.pci = self._pci()
        self.usb = self._usb()
        self.net = self._net()
        self.bt_adapters = [d for d in s.listdir("/sys/class/bluetooth") if d.startswith("hci")]
        self.rfkill = self._rfkill()
        self.kernels = self._kernels()
        rc, out = s.run("dkms", "status")
        self.dkms = parse_dkms_status(out) if rc == 0 else []

    def _pci(self):
        s = self.sys
        rc, out = s.run("lspci", "-mm")
        names = parse_lspci_mm(out) if rc == 0 else {}
        devices = {}
        for slot in s.listdir("/sys/bus/pci/devices"):
            base = f"/sys/bus/pci/devices/{slot}"
            cls = (s.read(f"{base}/class") or "").strip()
            link = s.readlink(f"{base}/driver")
            short = slot.split(":", 1)[1] if slot.count(":") == 2 else slot
            devices[slot] = {
                "id": f"{(s.read(base + '/vendor') or '').strip()[2:]}:{(s.read(base + '/device') or '').strip()[2:]}",
                "class": cls[:6],
                "driver": os.path.basename(link) if link else "",
                "name": names.get(short, ""),
            }
        return devices

    def _usb(self):
        s = self.sys
        devices = {}
        for d in s.listdir("/sys/bus/usb/devices"):
            if ":" in d:
                continue
            base = f"/sys/bus/usb/devices/{d}"
            vid = (s.read(f"{base}/idVendor") or "").strip()
            if not vid:
                continue
            pid = (s.read(f"{base}/idProduct") or "").strip()
            devices[d] = {"id": f"{vid}:{pid}", "name": (s.read(f"{base}/product") or "").strip()}
        return devices

    def _net(self):
        s = self.sys
        net = {}
        for iface in s.listdir("/sys/class/net"):
            link = s.readlink(f"/sys/class/net/{iface}/device/driver")
            if link:
                net[iface] = os.path.basename(link)
        return net

    def _rfkill(self):
        s = self.sys
        rows = []
        for r in s.listdir("/sys/class/rfkill"):
            base = f"/sys/class/rfkill/{r}"
            rows.append({
                "type": (s.read(f"{base}/type") or "").strip(),
                "name": (s.read(f"{base}/name") or "").strip(),
                "hard": (s.read(f"{base}/hard") or "0").strip() == "1",
                "soft": (s.read(f"{base}/soft") or "0").strip() == "1",
            })
        return sorted(rows, key=lambda r: (r["type"], r["name"]))

    def _kernels(self):
        """{release: paquete} de los kernels instalados (los que tienen vmlinuz de un paquete)."""
        s = self.sys
        kernels = {}
        for rel in s.listdir("/usr/lib/modules"):
            vmlinuz = f"/usr/lib/modules/{rel}/vmlinuz"
            if s.exists(vmlinuz):
                rc, out = s.run("pacman", "-Qqo", vmlinuz)
                if rc == 0 and out.strip():
                    kernels[rel] = out.strip()
        return kernels

    def modprobe_config(self):
        """Archivos efectivos de modprobe.d (un nombre en /etc tapa al mismo nombre en /usr/lib)."""
        chosen = {}
        for d in MODPROBE_DIRS:
            for f in self.sys.listdir(d):
                if f.endswith(".conf") and f not in chosen:
                    chosen[f] = f"{d}/{f}"
        options, blacklist = [], []
        for name in sorted(chosen):
            o, b = parse_modprobe(self.sys.read(chosen[name]))
            options += [(m, k, v, chosen[name]) for m, k, v in o]
            blacklist += [(m, chosen[name]) for m in b]
        return options, blacklist

    def limine_tokens(self):
        """{token: archivo} de la línea de arranque configurada (default + drop-ins)."""
        files = list(LIMINE_CMDLINE_FILES)
        files += [f"{LIMINE_DROPIN_DIR}/{f}" for f in self.sys.listdir(LIMINE_DROPIN_DIR) if f.endswith(".conf")]
        tokens = {}
        for path in files:
            for line in (self.sys.read(path) or "").splitlines():
                m = _KCMD_RE.match(line)
                if not m:
                    continue
                if m.group(1) == "=":  # `=` reemplaza todo lo anterior
                    tokens = {}
                for t in m.group(2).split():
                    tokens.setdefault(t, path)
        return dict(sorted(tokens.items()))


# --------------------------------------------------------------------------------------------------
# Comprobaciones. Cada una devuelve un Finding; el id es estable para poder buscarlo y compararlo.


class Finding:
    def __init__(self, id_, category, level, title, details=(), hint=""):
        self.id, self.category, self.level, self.title = id_, category, level, title
        self.details, self.hint = list(details), hint

    def to_dict(self):
        return {"id": self.id, "category": self.category, "level": self.level, "title": self.title,
                "details": self.details, "hint": self.hint}


def check_pci_drivers(f):
    missing = [f"{slot} [{d['id']}] {d['name']}".rstrip() for slot, d in sorted(f.pci.items())
               if not d["driver"] and not d["class"].startswith(PCI_CLASSES_WITHOUT_DRIVER)]
    if missing:
        return Finding("HW01", "hw", WARN, f"{len(missing)} dispositivo(s) PCI sin driver", missing,
                       "Busca el id [vendor:device] en https://linux-hardware.org o con `modinfo`/`lspci -k`.")
    return Finding("HW01", "hw", OK, f"Los {len(f.pci)} dispositivos PCI tienen driver (salvo puentes)")


def check_usb_errors(f):
    if not f.kernel_log_ok:
        return Finding("HW02", "hw", INFO, "No se pudo leer el registro del kernel (journalctl -k)")
    errors = usb_enum_errors(f.kernel_log)
    if errors:
        return Finding("HW02", "hw", WARN, f"{len(errors)} dispositivo(s) USB no lograron conectarse en este arranque",
                       [f"usb {k}" for k in errors],  # sin conteo: crece con el tiempo y rompería la comparación
                       "Un dispositivo que no responde retrasa el arranque y no tiene driver que lo arregle: "
                       "revisa el cable/puerto, o corte total de energía si es interno (Bluetooth 6-11).")
    return Finding("HW02", "hw", OK, "Sin errores de enumeración USB en este arranque")


def check_firmware_load(f):
    if not f.kernel_log_ok:
        return Finding("HW03", "hw", INFO, "No se pudo leer el registro del kernel: firmware sin verificar")
    failed = firmware_failures(f.kernel_log)
    if failed:
        return Finding("HW03", "hw", FAIL, f"{len(failed)} firmware(s) no se pudieron cargar", failed,
                       "Instala el paquete que lo contiene: `pacman -F <archivo>` y `omarchy pkg add <paquete>`.")
    return Finding("HW03", "hw", OK, "Ningún firmware falló al cargar en este arranque")


def check_firmware_packages(f):
    needed = {}
    for mod in f.loaded:
        for pattern, pkg in FIRMWARE_PACKAGES:
            if re.fullmatch(pattern, mod):
                needed.setdefault(pkg, []).append(mod)
    missing = [f"{pkg} (lo usa {', '.join(mods)})" for pkg, mods in sorted(needed.items())
               if pkg not in f.packages and not _provided(f, pkg)]
    if missing:
        return Finding("HW04", "hw", FAIL, "Faltan paquetes de firmware para drivers cargados", missing,
                       "`omarchy pkg add <paquete>` y reinicia.")
    return Finding("HW04", "hw", OK, "Paquetes de firmware presentes para los drivers cargados",
                   [f"{pkg}: {', '.join(mods)}" for pkg, mods in sorted(needed.items())])


def _provided(f, pkg):
    rc, out = f.sys.run("pacman", "-T", pkg)
    return rc == 0


def nvidia_versions(f):
    """(cargado, nvidia-utils, {kernel instalado: versión del módulo en disco})."""
    loaded = (f.sys.read("/sys/module/nvidia/version") or "").strip()
    utils = upstream_version(f.packages["nvidia-utils"]) if "nvidia-utils" in f.packages else ""
    on_disk = {}
    for rel in sorted(f.kernels):
        rc, out = f.sys.run("modinfo", "-k", rel, "-F", "version", "nvidia")
        on_disk[rel] = out.strip() if rc == 0 else ""
    return loaded, utils, on_disk


def check_nvidia_firmware(f):
    if "nvidia" not in f.loaded:
        return None
    loaded, utils, _ = nvidia_versions(f)
    # El firmware lo instala nvidia-utils: tras actualizarlo, la carpeta de la versión cargada desaparece y la
    # que importa es la de la versión nueva, que se usará al reiniciar.
    version = utils or loaded
    path = f"/usr/lib/firmware/nvidia/{version}"
    if version and not f.sys.exists(path):
        return Finding("HW05", "hw", FAIL, f"Falta el firmware GSP de NVIDIA {version}", [path],
                       "Lo instala nvidia-utils: reinstálalo con la misma versión del módulo.")
    if utils and loaded and utils != loaded:
        return Finding("HW05", "hw", WARN, f"Firmware GSP de NVIDIA {utils} listo; el módulo cargado es {loaded}",
                       [path], "NVIDIA se actualizó: reinicia para usarlo.")
    return Finding("HW05", "hw", OK, f"Firmware GSP de NVIDIA {version} presente")


def check_bluetooth(f):
    enabled = f.sys.run("systemctl", "is-enabled", "bluetooth.service")[1].strip() == "enabled"
    if f.bt_adapters:
        blocked = [r["name"] for r in f.rfkill if r["type"] == "bluetooth" and r["soft"]]
        details = [f"adaptadores: {', '.join(f.bt_adapters)}"]
        if blocked:
            return Finding("HW06", "hw", INFO, "Bluetooth presente pero apagado (rfkill soft)", details,
                           "Encenderlo: `omarchy bluetooth power on` o SUPER+CTRL+B.")
        return Finding("HW06", "hw", OK, "Adaptador Bluetooth presente", details)
    if "bluez" in f.packages and enabled:
        stuck = [k for k in usb_enum_errors(f.kernel_log)]
        return Finding("HW06", "hw", WARN, "No hay adaptador Bluetooth (bluetooth.service habilitado)",
                       [f"btusb cargado: {'sí' if 'btusb' in f.loaded else 'no'}",
                        f"USB que no enumeran (uno puede ser el chip Bluetooth): {', '.join(stuck) or 'ninguno'}"],
                       f"Si el chip no enumera no es un driver: corte total de energía. Ver {BT_REFERENCE}")
    return Finding("HW06", "hw", INFO, "Sin Bluetooth (ni adaptador ni servicio habilitado)")


def check_rfkill(f):
    hard = [f"{r['type']} {r['name']}" for r in f.rfkill if r["hard"]]
    if hard:
        return Finding("HW07", "hw", WARN, "Radios bloqueadas por hardware (rfkill hard)", hard,
                       "Interruptor físico, tecla de modo avión o ajuste de BIOS.")
    return Finding("HW07", "hw", OK, "Ninguna radio bloqueada por hardware")


def check_running_kernel(f):
    if not f.sys.exists(f"/usr/lib/modules/{f.release}"):
        return Finding("UP01", "updates", WARN, f"El kernel en uso ({f.release}) ya no está instalado",
                       [f"instalados: {', '.join(sorted(f.kernels)) or '?'}"],
                       "Reinicia: hasta entonces no se pueden cargar módulos nuevos (USB, Bluetooth...).")
    return Finding("UP01", "updates", OK, f"El kernel en uso ({f.release}) está instalado")


def check_headers(f):
    if "dkms" not in f.packages:
        return None
    bad = []
    for rel, pkg in sorted(f.kernels.items()):
        want, have = f.packages.get(pkg), f.packages.get(f"{pkg}-headers")
        if have != want:
            bad.append(f"{pkg} {want}: {pkg}-headers {have or 'NO instalado'}")
    if bad:
        return Finding("UP02", "updates", FAIL, "Headers del kernel no coinciden: DKMS no podrá compilar", bad,
                       "`omarchy pkg add <kernel>-headers` y actualiza ambos juntos.")
    return Finding("UP02", "updates", OK, "Headers instalados y a la par de cada kernel")


def check_dkms(f):
    if not f.dkms:
        return None
    built = {(m, k) for m, v, k, st in f.dkms if k and st.startswith("installed")}
    modules = sorted({m for m, v, k, st in f.dkms})
    missing = [f"{m} para {rel}" for rel in sorted(f.kernels) for m in modules if (m, rel) not in built]
    stale = sorted({f"{m}/{v} para {k}" for m, v, k, st in f.dkms if k and k not in f.kernels})
    warned = [f"{m}/{v} {k}: {st}" for m, v, k, st in f.dkms if "WARNING" in st or "Diff" in st]
    if missing:
        return Finding("UP03", "updates", FAIL, "Módulos DKMS sin compilar para un kernel instalado", missing + warned,
                       "NO reinicies hasta resolverlo (riesgo de pantalla negra con NVIDIA): "
                       "`sudo dkms autoinstall -k <kernel>` y revisa /var/lib/dkms/<mod>/<ver>/build/make.log.")
    if warned:
        return Finding("UP03", "updates", WARN, "DKMS avisa de módulos distintos a los compilados", warned)
    details = [f"{m}: {', '.join(sorted(f.kernels))}" for m in modules]
    if stale:
        details += [f"(sobrante) {s}" for s in stale]
    return Finding("UP03", "updates", OK, "Módulos DKMS compilados para todos los kernels", details)


def check_nvidia_version(f):
    if "nvidia" not in f.loaded or "nvidia-utils" not in f.packages:
        return None
    loaded, utils, on_disk = nvidia_versions(f)
    details = [f"cargado {loaded}, nvidia-utils {utils}"]
    details += [f"en disco para {rel}: {v or 'no está'}" for rel, v in on_disk.items()]
    lib32 = f.packages.get("lib32-nvidia-utils")
    if lib32 and upstream_version(lib32) != utils:
        return Finding("UP04", "updates", WARN, "lib32-nvidia-utils no coincide con nvidia-utils",
                       details + [f"lib32-nvidia-utils {upstream_version(lib32)}"],
                       "Actualiza ambos juntos (juegos de 32 bits/Steam fallarían).")
    if loaded == utils:
        return Finding("UP04", "updates", OK, f"NVIDIA {loaded}: módulo y nvidia-utils coinciden", details)
    if on_disk and all(v == utils for v in on_disk.values()):
        return Finding("UP04", "updates", WARN, "NVIDIA actualizado: falta reiniciar", details,
                       "Reinicia para cargar el módulo nuevo.")
    return Finding("UP04", "updates", FAIL, "El módulo NVIDIA en disco no coincide con nvidia-utils", details,
                   "Revisa UP03 (DKMS) y reinstala nvidia-open-dkms/nvidia-utils a la misma versión.")


def check_cmdline_pending(f):
    configured = f.limine_tokens()
    if not configured:
        return Finding("UP05", "updates", INFO, "No se pudo leer la configuración de Limine (/etc/default/limine)")
    running = set(f.cmdline)
    pending = [f"{t}  ({os.path.basename(src)})" for t, src in configured.items() if t not in running]
    if pending:
        return Finding("UP05", "updates", WARN, "Parámetros del kernel configurados que aún no están activos", pending,
                       "Reinicia. Si siguen faltando después, ejecuta `sudo limine-update`.")
    return Finding("UP05", "updates", OK, "La línea de arranque en uso coincide con la configurada")


def check_failed_units(f):
    failed = []
    for scope in ([], ["--user"]):
        rc, out = f.sys.run("systemctl", *scope, "--failed", "--no-legend", "--plain")
        for line in out.splitlines():
            if line.split():
                failed.append(("usuario: " if scope else "sistema: ") + line.split()[0])
    if failed:
        return Finding("UP06", "updates", WARN, f"{len(failed)} servicio(s) fallidos", sorted(failed),
                       "`systemctl [--user] status <unidad>` y `journalctl -b -u <unidad>`.")
    return Finding("UP06", "updates", OK, "Ningún servicio fallido")


def check_reboot_flags(f):
    reasons = []
    if f.sys.exists(f"{f.sys.home}/.local/state/omarchy/reboot-required"):
        reasons.append("Omarchy marcó que la actualización necesita reiniciar")
    rc, out = f.sys.run("pgrep", "-x", "Hyprland")
    for pid in out.split():
        if "(deleted)" in (f.sys.readlink(f"/proc/{pid}/exe") or ""):
            reasons.append("Hyprland se actualizó mientras corría")
    if reasons:
        return Finding("UP07", "updates", WARN, "Reinicio pendiente", sorted(set(reasons)), "Reinicia.")
    return Finding("UP07", "updates", OK, "Sin reinicio pendiente marcado por Omarchy")


_ANSI_RE = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")


def check_update_log(f):
    text = f.sys.read(UPDATE_LOG)
    if text is None:
        return Finding("UP08", "updates", INFO, "Sin registro de `omarchy update` desde que arrancó el equipo")
    lines = []
    for raw in text.replace("\r", "\n").splitlines():
        # Sin quitar la sangría: los detalles que imprime este mismo validador (sangrados) quedan en el
        # registro porque el hook corre dentro de `omarchy update`, y no deben contarse como errores.
        line = _ANSI_RE.sub("", raw).rstrip()
        if re.match(r"^(error:|ERROR|Hook failed|==> ERROR|Something went wrong)", line):
            if line.startswith("error: failed retrieving file"):  # un espejo falló; pacman usó otro
                continue
            lines.append(line[:160])
    if any(l.startswith("Something went wrong") for l in lines):
        return Finding("UP08", "updates", FAIL, "La última `omarchy update` terminó con error", lines[:12],
                       f"Lee {UPDATE_LOG} y vuelve a ejecutar `omarchy update`.")
    if lines:
        return Finding("UP08", "updates", WARN, "La última `omarchy update` registró errores", lines[:12],
                       f"Revisa {UPDATE_LOG}.")
    return Finding("UP08", "updates", OK, "La última `omarchy update` no registró errores")


def check_pending_updates(f):
    rc, out = f.sys.run("checkupdates", timeout=180)
    if rc == 127:
        return Finding("UP09", "updates", INFO, "checkupdates no está instalado (paquete pacman-contrib)")
    if rc not in (0, 2):
        return Finding("UP09", "updates", INFO, "No se pudo consultar las actualizaciones (¿sin red?)")
    rows = sorted(l.strip() for l in out.splitlines() if l.strip())
    drivers = [r for r in rows if DRIVER_PACKAGE_RE.match(r.split()[0])]
    if not rows:
        return Finding("UP09", "updates", OK, "El sistema está al día")
    hint = "Actualiza con `omarchy update`; el hook de omarchy-hwcheck valida DKMS antes de que reinicies."
    if any(r.split()[0] in f.kernels.values() for r in drivers) and f.dkms:
        hint = "Viene un kernel nuevo y hay módulos DKMS: tras `omarchy update` revisa UP02/UP03 ANTES de reiniciar."
    return Finding("UP09", "updates", INFO,
                   f"{len(rows)} actualización(es) pendientes, {len(drivers)} de drivers/kernel", drivers, hint)


def check_pacman_db(f):
    rc, out = f.sys.run("pacman", "-Dk")
    if rc == 127:
        return None
    if rc != 0:
        return Finding("CF01", "conflicts", FAIL, "La base de datos de pacman tiene dependencias rotas o conflictos",
                       [l for l in out.splitlines() if l.strip()][:15],
                       "Suele ser una actualización parcial: ejecuta `omarchy update` completo.")
    return Finding("CF01", "conflicts", OK, "Base de datos de pacman consistente (pacman -Dk)")


def check_pacnew(f):
    files = f.sys.walk_files("/etc", (".pacnew", ".pacsave"))
    if files:
        return Finding("CF02", "conflicts", WARN, f"{len(files)} archivo(s) .pacnew/.pacsave sin resolver", files,
                       "Compara y combina (`sudo pacdiff`, de pacman-contrib); luego borra el .pacnew.")
    return Finding("CF02", "conflicts", OK, "Sin .pacnew/.pacsave pendientes en /etc")


def check_replaced(f):
    entries = f.sys.listdir(REPLACED_DIR)
    if entries:
        files = f.sys.walk_files(REPLACED_DIR, ("",))
        return Finding("CF03", "conflicts", WARN,
                       "`omarchy update` apartó archivos para resolver un conflicto",
                       [p[len(REPLACED_DIR):] for p in files][:20],
                       f"Son archivos que pacman no tenía registrados. Revisa si necesitas algo de {REPLACED_DIR} "
                       "y bórralos cuando no.")
    return Finding("CF03", "conflicts", OK, "Ningún archivo apartado por conflictos de actualización")


def check_module_options(f):
    options, _ = f.modprobe_config()
    values = {}
    for mod, k, v, src in options:
        values.setdefault((mod, k), []).append((v, src))
    for k, v in cmdline_params(f.cmdline).items():
        if "." in k:
            mod, _, param = k.partition(".")
            values.setdefault((norm_mod(mod), param), []).append((v, "/proc/cmdline"))
    conflicts = []
    for (mod, k), vals in sorted(values.items()):
        if len({bool_norm(v) for v, _ in vals}) > 1:
            conflicts.append(f"{mod}.{k}: " + "; ".join(f"{v} en {src}" for v, src in vals))
    if conflicts:
        return Finding("CF04", "conflicts", WARN, "Opciones de módulo con valores contradictorios", conflicts,
                       "Gana la línea de arranque, y luego el último archivo de modprobe.d en orden alfabético.")
    return Finding("CF04", "conflicts", OK, "Sin opciones de módulo contradictorias")


def check_builtin_options(f):
    options, _ = f.modprobe_config()
    builtin = set(f.builtin)
    ignored = sorted({f"{mod}.{k}={v} en {src}" for mod, k, v, src in options if mod in builtin})
    if ignored:
        return Finding("CF05", "conflicts", WARN,
                       "Opciones en modprobe.d para módulos integrados en el kernel: no tienen efecto", ignored,
                       "Un módulo integrado solo lee la línea de arranque: pon `modulo.opcion=valor` como "
                       "parámetro del kernel (kernel-param.sh) si de verdad la necesitas.")
    return Finding("CF05", "conflicts", OK, "Ninguna opción de modprobe.d se pierde en un módulo integrado")


def check_effective_params(f):
    """El valor real en /sys/module coincide con lo pedido (línea de arranque o modprobe.d)."""
    expected = {}
    options, _ = f.modprobe_config()
    loaded = set(f.loaded)
    for mod, k, v, src in options:
        if mod in loaded:
            expected[(mod, k)] = (v, src)
    for k, v in cmdline_params(f.cmdline).items():
        if "." in k:
            mod, _, param = k.partition(".")
            expected[(norm_mod(mod), param)] = (v, "/proc/cmdline")
    wrong, checked = [], 0
    for (mod, k), (v, src) in sorted(expected.items()):
        actual = f.sys.read(f"/sys/module/{mod}/parameters/{k}")
        if actual is None:
            continue
        checked += 1
        if not same_param_value(v, actual):
            wrong.append(f"{mod}.{k}: pedido {v} ({src}), en uso {actual.strip()}")
    if wrong:
        return Finding("CF06", "conflicts", WARN, "Parámetros de módulo que no tomaron el valor pedido", wrong,
                       "Otro archivo o la línea de arranque lo pisa, o el módulo se cargó antes (initramfs).")
    return Finding("CF06", "conflicts", OK, f"{checked} parámetro(s) de módulo con el valor pedido")


def check_blacklisted_loaded(f):
    _, blacklist = f.modprobe_config()
    loaded = set(f.loaded)
    bad = sorted({f"{m} (lista negra en {src})" for m, src in blacklist if m in loaded})
    if bad:
        return Finding("CF07", "conflicts", FAIL, "Módulos en lista negra que igual están cargados", bad,
                       "Probablemente vienen del initramfs: `sudo mkinitcpio -P` (o `limine-update`) y reinicia.")
    return Finding("CF07", "conflicts", OK, "Ningún módulo en lista negra está cargado")


def check_package_conflicts(f):
    pkgs = f.packages
    bad = [f"{a} y {b}" for a, b in CONFLICTING_PACKAGES if a in pkgs and b in pkgs]
    for a, b in CONFLICTING_SERVICES:
        if all(f.sys.run("systemctl", "is-enabled", u)[1].strip() == "enabled" for u in (a, b)):
            bad.append(f"servicios {a} y {b} habilitados a la vez")
    if bad:
        return Finding("CF08", "conflicts", WARN, "Paquetes o servicios que compiten entre sí", bad,
                       "Deja uno solo (`omarchy pkg drop <paquete>` o `systemctl disable <servicio>`).")
    return Finding("CF08", "conflicts", OK, "Sin paquetes ni servicios que compitan entre sí")


def check_version_skew(f):
    pkgs = f.packages
    groups = {
        "linux-firmware": sorted(p for p in pkgs if p == "linux-firmware" or p.startswith("linux-firmware-")),
        "mesa": [p for p in ("mesa", "lib32-mesa", "vulkan-radeon", "lib32-vulkan-radeon",
                             "vulkan-intel", "lib32-vulkan-intel") if p in pkgs],
        "nvidia": [p for p in ("nvidia-utils", "lib32-nvidia-utils", "nvidia-open-dkms", "nvidia-open",
                               "nvidia-dkms", "nvidia", "opencl-nvidia") if p in pkgs],
    }
    skew = []
    for name, members in sorted(groups.items()):
        versions = {upstream_version(pkgs[p]) for p in members}
        if len(versions) > 1:
            skew.append(f"{name}: " + ", ".join(f"{p} {pkgs[p]}" for p in members))
    if skew:
        return Finding("CF09", "conflicts", WARN, "Paquetes que deberían ir a la par tienen versiones distintas",
                       skew, "Señal de actualización parcial: ejecuta `omarchy update` completo.")
    return Finding("CF09", "conflicts", OK, "Firmware, Mesa y NVIDIA a la par entre sus paquetes")


CHECKS = [
    check_pci_drivers, check_usb_errors, check_firmware_load, check_firmware_packages, check_nvidia_firmware,
    check_bluetooth, check_rfkill,
    check_running_kernel, check_headers, check_dkms, check_nvidia_version, check_cmdline_pending,
    check_failed_units, check_reboot_flags, check_update_log,
    check_pacman_db, check_pacnew, check_replaced, check_module_options, check_builtin_options,
    check_effective_params, check_blacklisted_loaded, check_package_conflicts, check_version_skew,
]


def run_checks(facts, online=False, only=None):
    checks = CHECKS + ([check_pending_updates] if online else [])
    findings = [r for r in (c(facts) for c in checks) if r is not None]
    if only:
        findings = [x for x in findings if x.category in only]
    return sorted(findings, key=lambda x: x.id)


# --------------------------------------------------------------------------------------------------
# Estado normalizado (para baseline/diff)


def collect_state(f):
    return {
        "kernel": f.release,
        "cmdline": sorted(f.cmdline),
        "pci": {slot: {"id": d["id"], "driver": d["driver"]} for slot, d in sorted(f.pci.items())},
        "usb": sorted({d["id"] for d in f.usb.values()}),
        "net": dict(sorted(f.net.items())),
        "bluetooth": sorted(f.bt_adapters),
        "packages": {p: v for p, v in sorted(f.packages.items()) if DRIVER_PACKAGE_RE.match(p)},
        "dkms": sorted({f"{m}/{v} {k}: {st}" for m, v, k, st in f.dkms}),
    }


def diff_states(old, new):
    """Devuelve [(nivel, texto)]: FAIL para regresiones, INFO para cambios esperables."""
    out = []
    for slot in sorted(set(old["pci"]) | set(new["pci"])):
        a, b = old["pci"].get(slot), new["pci"].get(slot)
        if a and not b:
            out.append((FAIL, f"PCI {slot} [{a['id']}] desapareció (tenía driver {a['driver'] or '-'})"))
        elif b and not a:
            out.append((INFO, f"PCI {slot} [{b['id']}] nuevo, driver {b['driver'] or '-'}"))
        elif a["driver"] != b["driver"]:
            lvl = FAIL if a["driver"] and not b["driver"] else WARN
            out.append((lvl, f"PCI {slot} [{b['id']}]: driver {a['driver'] or '-'} → {b['driver'] or '-'}"))
    for iface in sorted(set(old["net"]) | set(new["net"])):
        a, b = old["net"].get(iface), new["net"].get(iface)
        if a and not b:
            out.append((FAIL, f"interfaz de red {iface} ({a}) desapareció"))
        elif b and not a:
            out.append((INFO, f"interfaz de red {iface} ({b}) nueva"))
        elif a != b:
            out.append((WARN, f"interfaz de red {iface}: driver {a} → {b}"))
    for hci in sorted(set(old["bluetooth"]) - set(new["bluetooth"])):
        out.append((FAIL, f"adaptador Bluetooth {hci} desapareció"))
    for hci in sorted(set(new["bluetooth"]) - set(old["bluetooth"])):
        out.append((INFO, f"adaptador Bluetooth {hci} nuevo"))
    for dev in sorted(set(old["usb"]) - set(new["usb"])):
        out.append((INFO, f"USB {dev} ya no está conectado"))
    for dev in sorted(set(new["usb"]) - set(old["usb"])):
        out.append((INFO, f"USB {dev} nuevo"))
    if old["kernel"] != new["kernel"]:
        out.append((INFO, f"kernel {old['kernel']} → {new['kernel']}"))
    for t in sorted(set(old["cmdline"]) - set(new["cmdline"])):
        out.append((INFO, f"línea de arranque: quitado {t}"))
    for t in sorted(set(new["cmdline"]) - set(old["cmdline"])):
        out.append((INFO, f"línea de arranque: agregado {t}"))
    for p in sorted(set(old["packages"]) | set(new["packages"])):
        a, b = old["packages"].get(p), new["packages"].get(p)
        if a != b:
            lvl = WARN if a and not b else INFO
            out.append((lvl, f"paquete {p}: {a or '-'} → {b or '-'}"))
    for d in sorted(set(old["dkms"]) - set(new["dkms"])):
        out.append((INFO, f"dkms quitado: {d}"))
    for d in sorted(set(new["dkms"]) - set(old["dkms"])):
        out.append((INFO, f"dkms nuevo: {d}"))
    return out


# --------------------------------------------------------------------------------------------------
# Salida


def state_dir(home):
    base = os.environ.get("XDG_STATE_HOME") or os.path.join(home, ".local/state")
    return os.path.join(base, "omarchy-hwcheck")


def exit_code(levels):
    return max((LEVEL_RANK[l] for l in levels), default=0)


def render_scan(findings, release):
    lines = [f"omarchy-hwcheck {VERSION} · kernel {release}"]
    for cat, title in CATEGORIES:
        items = [x for x in findings if x.category == cat]
        if not items:
            continue
        lines.append(f"\n== {title}")
        for x in items:
            lines.append(f"{LEVEL_TAG[x.level]} {x.id} {x.title}")
            if x.level != OK or len(x.details) <= 4:
                lines += [f"       {d}" for d in x.details]
            if x.hint and x.level in (WARN, FAIL, INFO):
                lines.append(f"       → {x.hint}")
    counts = {lvl: sum(1 for x in findings if x.level == lvl) for lvl in (OK, INFO, WARN, FAIL)}
    lines.append(f"\nResultado: {counts[OK]} bien, {counts[INFO]} informativos, {counts[WARN]} avisos, "
                 f"{counts[FAIL]} fallos")
    return "\n".join(lines) + "\n"


def render_diff(changes, baseline_path):
    lines = [f"omarchy-hwcheck {VERSION} · comparación con {baseline_path}"]
    if not changes:
        lines.append("Sin cambios de hardware, drivers ni paquetes de drivers.")
    for lvl, text in changes:
        lines.append(f"{LEVEL_TAG[lvl]} {text}")
    regress = sum(1 for l, _ in changes if l == FAIL)
    lines.append(f"\nResultado: {len(changes)} cambio(s), {regress} regresión(es)")
    return "\n".join(lines) + "\n"


def main(argv=None, system=None):
    args = list(sys.argv[1:] if argv is None else argv)
    cmd = args.pop(0) if args and not args[0].startswith("-") else "scan"
    as_json = "--json" in args
    online = "--online" in args
    force = "--force" in args
    only = None
    if "--only" in args:
        i = args.index("--only")
        if i + 1 >= len(args):
            print("--only necesita hw, updates o conflicts", file=sys.stderr)
            return 3
        only = set(args[i + 1].split(","))
    if cmd in ("-h", "--help", "help"):
        print(__doc__)
        return 0
    if cmd == "--version":
        print(VERSION)
        return 0
    if cmd not in ("scan", "baseline", "diff", "state"):
        print(f"comando desconocido: {cmd}\n{__doc__}", file=sys.stderr)
        return 3

    s = system or System()
    facts = Facts(s)

    if cmd == "scan":
        findings = run_checks(facts, online=online, only=only)
        if as_json:
            print(json.dumps({"version": VERSION, "kernel": facts.release,
                              "findings": [x.to_dict() for x in findings]}, ensure_ascii=False, indent=2,
                             sort_keys=True))
        else:
            sys.stdout.write(render_scan(findings, facts.release))
        return exit_code(x.level for x in findings)

    state = collect_state(facts)
    if cmd == "state":
        print(json.dumps(state, ensure_ascii=False, indent=2, sort_keys=True))
        return 0

    path = os.path.join(state_dir(s.home), "baseline.json")
    if cmd == "baseline":
        if os.path.exists(path) and not force:
            print(f"Ya existe {path}. Usa --force para reemplazarla (revisa antes `omarchy-hwcheck diff`).")
            return 1
        os.makedirs(os.path.dirname(path), exist_ok=True)
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(state, fh, ensure_ascii=False, indent=2, sort_keys=True)
            fh.write("\n")
        os.replace(tmp, path)
        print(f"Línea base guardada en {path}")
        return 0

    # diff
    try:
        with open(path, encoding="utf-8") as fh:
            old = json.load(fh)
    except (OSError, ValueError):
        print(f"No hay línea base válida en {path}: créala con `omarchy-hwcheck baseline`.", file=sys.stderr)
        return 3
    changes = diff_states(old, state)
    if as_json:
        print(json.dumps({"baseline": path, "changes": [{"level": l, "text": t} for l, t in changes]},
                         ensure_ascii=False, indent=2, sort_keys=True))
    else:
        sys.stdout.write(render_diff(changes, path))
    return exit_code(l for l, _ in changes)


if __name__ == "__main__":
    sys.exit(main())
