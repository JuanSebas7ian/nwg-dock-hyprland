"""Pruebas de omarchy-hwcheck con un sistema simulado: python3 -m unittest -v test_hwcheck"""

import copy
import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stdout

import hwcheck as hc

REL = "7.2.3-arch1-3"


class FakeSystem:
    """Sistema simulado: archivos, enlaces y salidas de comandos fijos."""

    def __init__(self, files=None, links=None, commands=None, home="/home/test"):
        self.files = dict(files or {})
        self.links = dict(links or {})
        self.commands = dict(commands or {})
        self.home = home

    def read(self, path):
        return self.files.get(path)

    def exists(self, path):
        p = path.rstrip("/")
        return p in self.files or p in self.links or any(k.startswith(p + "/") for k in list(self.files) + list(self.links))

    def listdir(self, path):
        prefix = path.rstrip("/") + "/"
        names = {k[len(prefix):].split("/", 1)[0] for k in list(self.files) + list(self.links) if k.startswith(prefix)}
        return sorted(names)

    def readlink(self, path):
        return self.links.get(path)

    def walk_files(self, top, suffixes):
        prefix = top.rstrip("/") + "/"
        return sorted(k for k in self.files if k.startswith(prefix) and k.endswith(suffixes) and "/.git/" not in k)

    def run(self, *cmd, timeout=60):
        return self.commands.get(" ".join(cmd), (127, ""))


def healthy():
    """Equipo sano parecido al real: AMD + NVIDIA open (DKMS) + MT7925 con Bluetooth."""
    files = {
        "/proc/sys/kernel/osrelease": REL + "\n",
        "/proc/cmdline": "root=/dev/mapper/root rw quiet zswap.enabled=0 btusb.enable_autosuspend=0\n",
        "/proc/modules": "\n".join(f"{m} 16384 0 - Live 0x0" for m in
                                   ("nvidia", "nvidia_drm", "amdgpu", "mt7925e", "btusb", "btmtk", "igc")) + "\n",
        f"/usr/lib/modules/{REL}/modules.builtin": "kernel/drivers/usb/core/usbcore.ko\nkernel/mm/zswap.ko\n",
        f"/usr/lib/modules/{REL}/vmlinuz": "",
        "/sys/module/nvidia/version": "610.57.04\n",
        "/usr/lib/firmware/nvidia/610.57.04/gsp_ga10x.bin": "",
        "/sys/module/zswap/parameters/enabled": "N\n",
        "/sys/module/btusb/parameters/enable_autosuspend": "N\n",
        "/sys/module/btusb/parameters/reset": "Y\n",
        "/usr/lib/modprobe.d/bluetooth-usb.conf": "options btusb reset=1\n",
        "/usr/lib/modprobe.d/nvidia-utils.conf": "blacklist nouveau\n",
        "/etc/default/limine": 'KERNEL_CMDLINE[default]+="root=/dev/mapper/root rw quiet zswap.enabled=0"\n',
        "/etc/limine-entry-tool.d/claude-bt.conf": '# x\nKERNEL_CMDLINE[default]+=" btusb.enable_autosuspend=0"\n',
        # PCI: puente sin driver (normal), GPU NVIDIA, WiFi
        "/sys/bus/pci/devices/0000:00:00.0/class": "0x060000\n",
        "/sys/bus/pci/devices/0000:00:00.0/vendor": "0x1022\n",
        "/sys/bus/pci/devices/0000:00:00.0/device": "0x14d8\n",
        "/sys/bus/pci/devices/0000:01:00.0/class": "0x030000\n",
        "/sys/bus/pci/devices/0000:01:00.0/vendor": "0x10de\n",
        "/sys/bus/pci/devices/0000:01:00.0/device": "0x2504\n",
        "/sys/bus/pci/devices/0000:07:00.0/class": "0x028000\n",
        "/sys/bus/pci/devices/0000:07:00.0/vendor": "0x14c3\n",
        "/sys/bus/pci/devices/0000:07:00.0/device": "0x7925\n",
        "/sys/bus/usb/devices/1-1/idVendor": "046d\n",
        "/sys/bus/usb/devices/1-1/idProduct": "c52b\n",
        "/sys/bus/usb/devices/6-11/idVendor": "0489\n",
        "/sys/bus/usb/devices/6-11/idProduct": "e13a\n",
        "/sys/class/bluetooth/hci0/x": "",
        "/sys/class/rfkill/rfkill0/type": "wlan\n",
        "/sys/class/rfkill/rfkill0/name": "phy0\n",
        "/sys/class/rfkill/rfkill0/hard": "0\n",
        "/sys/class/rfkill/rfkill0/soft": "0\n",
        "/sys/class/net/wlp7s0/x": "",
        "/sys/class/net/lo/x": "",
    }
    links = {
        "/sys/bus/pci/devices/0000:01:00.0/driver": "../../../bus/pci/drivers/nvidia",
        "/sys/bus/pci/devices/0000:07:00.0/driver": "../../../bus/pci/drivers/mt7925e",
        "/sys/class/net/wlp7s0/device/driver": "../../../bus/pci/drivers/mt7925e",
    }
    packages = {
        "linux": "7.2.3.arch1-3", "linux-headers": "7.2.3.arch1-3", "dkms": "3.2.1-1",
        "linux-firmware": "20260810-2", "linux-firmware-amdgpu": "20260810-2",
        "linux-firmware-mediatek": "20260810-2", "nvidia-open-dkms": "610.57.04-1",
        "nvidia-utils": "610.57.04-1", "lib32-nvidia-utils": "610.57.04-1", "mesa": "1:26.2.3-1",
        "lib32-mesa": "1:26.2.3-1", "bluez": "5.84-1",
    }
    commands = {
        "pacman -Q": (0, "".join(f"{p} {v}\n" for p, v in sorted(packages.items()))),
        "journalctl -k -b -o cat --no-pager": (0, "Linux version 7.2.3\nusb 1-1: new full-speed USB device\n"),
        "lspci -mm": (0, '01:00.0 "VGA compatible controller" "NVIDIA Corporation" "GA106 [GeForce RTX 3060]"\n'),
        f"pacman -Qqo /usr/lib/modules/{REL}/vmlinuz": (0, "linux\n"),
        "dkms status": (0, f"nvidia/610.57.04, {REL}, x86_64: installed\n"),
        f"modinfo -k {REL} -F version nvidia": (0, "610.57.04\n"),
        "systemctl is-enabled bluetooth.service": (0, "enabled\n"),
        "systemctl --failed --no-legend --plain": (0, ""),
        "systemctl --user --failed --no-legend --plain": (0, ""),
        "pgrep -x Hyprland": (0, "1234\n"),
        "pacman -Dk": (0, "No database errors have been found!\n"),
    }
    return FakeSystem(files, links, commands)


def scan(system, **kw):
    return {x.id: x for x in hc.run_checks(hc.Facts(system), **kw)}


def set_package(system, name, version):
    pkgs = hc.parse_pacman_q(system.commands["pacman -Q"][1])
    if version is None:
        pkgs.pop(name, None)
    else:
        pkgs[name] = version
    system.commands["pacman -Q"] = (0, "".join(f"{p} {v}\n" for p, v in sorted(pkgs.items())))


class HealthySystem(unittest.TestCase):
    def test_everything_ok(self):
        findings = scan(healthy())
        bad = {i: (x.level, x.title, x.details) for i, x in findings.items() if x.level in (hc.WARN, hc.FAIL)}
        self.assertEqual(bad, {})

    def test_deterministic_output(self):
        outputs = set()
        for _ in range(3):
            buf = io.StringIO()
            with redirect_stdout(buf):
                hc.main(["scan", "--json"], system=healthy())
            outputs.add(buf.getvalue())
        self.assertEqual(len(outputs), 1)

    def test_exit_codes(self):
        self.assertEqual(hc.exit_code([hc.OK, hc.INFO]), 0)
        self.assertEqual(hc.exit_code([hc.OK, hc.WARN]), 1)
        self.assertEqual(hc.exit_code([hc.WARN, hc.FAIL]), 2)


class Hardware(unittest.TestCase):
    def test_pci_device_without_driver(self):
        s = healthy()
        del s.links["/sys/bus/pci/devices/0000:07:00.0/driver"]
        f = scan(s)["HW01"]
        self.assertEqual(f.level, hc.WARN)
        self.assertIn("0000:07:00.0 [14c3:7925]", f.details[0])

    def test_usb_enumeration_errors_grouped(self):
        log = ("usb 6-11: device descriptor read/64, error -110\n"
               "usb 6-11: device descriptor read/64, error -110\n"
               "usb usb6-port11: unable to enumerate USB device\n"
               "usb 3-2.4: device not accepting address 9, error -71\n"
               "usb 3-2-port4: unable to enumerate USB device\n"
               "usb 1-1: new full-speed USB device number 2\n")
        self.assertEqual(hc.usb_enum_errors(log), {"3-2.4": 2, "6-11": 3})

    def test_usb_details_have_no_counts(self):
        s = healthy()
        s.commands["journalctl -k -b -o cat --no-pager"] = (0, "usb 6-11: device descriptor read/64, error -110\n")
        self.assertEqual(scan(s)["HW02"].details, ["usb 6-11"])  # el conteo crece con el tiempo

    def test_firmware_load_failure(self):
        s = healthy()
        s.commands["journalctl -k -b -o cat --no-pager"] = (
            0, "bluetooth hci0: Direct firmware load for mediatek/mt7925/BT_RAM_CODE.bin failed with error -2\n")
        f = scan(s)["HW03"]
        self.assertEqual((f.level, f.details), (hc.FAIL, ["mediatek/mt7925/BT_RAM_CODE.bin"]))

    def test_harmless_firmware_failures_ignored(self):
        log = ("cfg80211: Direct firmware load for regulatory.db failed with error -2\n"
               "iwlwifi 0000:00:14.3: Direct firmware load for iwlwifi-so-a0-gf-a0-89.ucode failed with error -2\n")
        self.assertEqual(hc.firmware_failures(log), [])

    def test_missing_firmware_package(self):
        s = healthy()
        set_package(s, "linux-firmware-mediatek", None)
        s.commands["pacman -T linux-firmware-mediatek"] = (127, "linux-firmware-mediatek\n")
        f = scan(s)["HW04"]
        self.assertEqual(f.level, hc.FAIL)
        self.assertIn("linux-firmware-mediatek", f.details[0])

    def test_nvidia_gsp_firmware_missing(self):
        s = healthy()
        del s.files["/usr/lib/firmware/nvidia/610.57.04/gsp_ga10x.bin"]
        self.assertEqual(scan(s)["HW05"].level, hc.FAIL)

    def test_bluetooth_missing(self):
        s = healthy()
        del s.files["/sys/class/bluetooth/hci0/x"]
        self.assertEqual(scan(s)["HW06"].level, hc.WARN)

    def test_bt_connection_is_not_an_adapter(self):
        # /sys/class/bluetooth/hci0:50 is an ACL connection to a paired device, not a second adapter
        s = healthy()
        s.files["/sys/class/bluetooth/hci0:50/x"] = ""
        self.assertEqual(hc.Facts(s).bt_adapters, ["hci0"])
        self.assertEqual(hc.diff_states(hc.collect_state(hc.Facts(healthy())), hc.collect_state(hc.Facts(s))), [])

    def test_rfkill_hard_block(self):
        s = healthy()
        s.files["/sys/class/rfkill/rfkill0/hard"] = "1\n"
        self.assertEqual(scan(s)["HW07"].level, hc.WARN)


class Updates(unittest.TestCase):
    def test_running_kernel_removed(self):
        s = healthy()
        s.files["/proc/sys/kernel/osrelease"] = "7.1.0-arch1-1\n"
        self.assertEqual(scan(s)["UP01"].level, hc.WARN)

    def test_headers_out_of_sync(self):
        s = healthy()
        set_package(s, "linux-headers", "7.2.2.arch1-1")
        self.assertEqual(scan(s)["UP02"].level, hc.FAIL)

    def test_dkms_not_built_for_new_kernel(self):
        s = healthy()
        new = "7.2.4-arch1-1"
        s.files[f"/usr/lib/modules/{new}/vmlinuz"] = ""
        s.commands[f"pacman -Qqo /usr/lib/modules/{new}/vmlinuz"] = (0, "linux\n")
        f = scan(s)["UP03"]
        self.assertEqual(f.level, hc.FAIL)
        self.assertEqual(f.details, [f"nvidia para {new}"])

    def test_dkms_status_parsing(self):
        rows = hc.parse_dkms_status(
            "hid-xpadneo/v0.10.4, 7.2.3-arch1-3, x86_64: installed\nnvidia/610.57.04: added\n")
        self.assertEqual(rows, [("hid-xpadneo", "v0.10.4", "7.2.3-arch1-3", "installed"),
                                ("nvidia", "610.57.04", "", "added")])

    def test_nvidia_updated_needs_reboot(self):
        s = healthy()
        for p in ("nvidia-utils", "lib32-nvidia-utils", "nvidia-open-dkms"):
            set_package(s, p, "615.10-1")
        s.commands[f"modinfo -k {REL} -F version nvidia"] = (0, "615.10\n")
        self.assertEqual(scan(s)["UP04"].level, hc.WARN)

    def test_nvidia_module_mismatch_fails(self):
        s = healthy()
        set_package(s, "nvidia-utils", "615.10-1")
        set_package(s, "lib32-nvidia-utils", "615.10-1")
        self.assertEqual(scan(s)["UP04"].level, hc.FAIL)

    def test_kernel_and_nvidia_updated_together(self):
        """omarchy update trajo kernel y NVIDIA nuevos; aún corre el kernel viejo: avisos de reinicio, no fallos."""
        s = healthy()
        new = "7.2.4-arch1-1"
        del s.files[f"/usr/lib/modules/{REL}/vmlinuz"]
        del s.files[f"/usr/lib/modules/{REL}/modules.builtin"]
        del s.files["/usr/lib/firmware/nvidia/610.57.04/gsp_ga10x.bin"]
        s.files[f"/usr/lib/modules/{new}/vmlinuz"] = ""
        s.files["/usr/lib/firmware/nvidia/615.10/gsp_ga10x.bin"] = ""
        s.commands[f"pacman -Qqo /usr/lib/modules/{new}/vmlinuz"] = (0, "linux\n")
        s.commands["dkms status"] = (0, f"nvidia/615.10, {new}, x86_64: installed\n")
        s.commands.pop(f"modinfo -k {REL} -F version nvidia")
        s.commands[f"modinfo -k {new} -F version nvidia"] = (0, "615.10\n")
        for p, v in (("linux", "7.2.4.arch1-1"), ("linux-headers", "7.2.4.arch1-1"), ("nvidia-utils", "615.10-1"),
                     ("lib32-nvidia-utils", "615.10-1"), ("nvidia-open-dkms", "615.10-1")):
            set_package(s, p, v)
        f = scan(s)
        levels = {i: f[i].level for i in ("UP01", "UP02", "UP03", "UP04", "HW05")}
        self.assertEqual(levels, {"UP01": hc.WARN, "UP02": hc.OK, "UP03": hc.OK, "UP04": hc.WARN, "HW05": hc.WARN})
        self.assertFalse([x.id for x in f.values() if x.level == hc.FAIL])

    def test_nvidia_new_kernel_without_module_fails(self):
        s = healthy()
        new = "7.2.4-arch1-1"
        s.files[f"/usr/lib/modules/{new}/vmlinuz"] = ""
        s.commands[f"pacman -Qqo /usr/lib/modules/{new}/vmlinuz"] = (0, "linux\n")
        for p in ("nvidia-utils", "lib32-nvidia-utils", "nvidia-open-dkms"):
            set_package(s, p, "615.10-1")
        s.commands[f"modinfo -k {REL} -F version nvidia"] = (0, "615.10\n")
        self.assertEqual(scan(s)["UP04"].level, hc.FAIL)  # el kernel nuevo no tiene módulo NVIDIA

    def test_limine_assignment_resets_and_other_keys_ignored(self):
        s = healthy()
        s.files["/etc/limine-entry-tool.d/claude-bt.conf"] = (
            'KERNEL_CMDLINE[linux-lts]+=" foo=1"\n'
            'KERNEL_CMDLINE[default]="root=/dev/mapper/root rw"\n')
        self.assertEqual(scan(s)["UP05"].level, hc.OK)

    def test_cmdline_pending(self):
        s = healthy()
        s.files["/etc/limine-entry-tool.d/claude-usb.conf"] = \
            'KERNEL_CMDLINE[default]+=" usbcore.initial_descriptor_timeout=1000"\n'
        f = scan(s)["UP05"]
        self.assertEqual(f.level, hc.WARN)
        self.assertIn("usbcore.initial_descriptor_timeout=1000", f.details[0])

    def test_failed_units(self):
        s = healthy()
        s.commands["systemctl --failed --no-legend --plain"] = (0, "foo.service loaded failed failed Foo\n")
        f = scan(s)["UP06"]
        self.assertEqual((f.level, f.details), (hc.WARN, ["sistema: foo.service"]))

    def test_update_log_errors(self):
        s = healthy()
        s.files[hc.UPDATE_LOG] = "ok\n\x1b[0;31mSomething went wrong during the update!\x1b[0m\r\nerror: failed\n"
        self.assertEqual(scan(s)["UP08"].level, hc.FAIL)

    def test_update_log_ignores_mirror_errors_and_own_output(self):
        s = healthy()
        s.files[hc.UPDATE_LOG] = ("error: failed retrieving file 'mesa.pkg.tar.zst' from mirror.example\n"
                                  "       error: missing 'libfoo' dependency for 'bar'\n")
        self.assertEqual(scan(s)["UP08"].level, hc.OK)

    def test_pending_updates_online_only(self):
        s = healthy()
        s.commands["checkupdates"] = (0, "linux 7.2.3.arch1-3 -> 7.2.4.arch1-1\nfirefox 1 -> 2\n")
        self.assertNotIn("UP09", scan(s))
        f = scan(s, online=True)["UP09"]
        self.assertEqual(f.details, ["linux 7.2.3.arch1-3 -> 7.2.4.arch1-1"])
        self.assertIn("ANTES de reiniciar", f.hint)


class Conflicts(unittest.TestCase):
    def test_pacman_db_errors(self):
        s = healthy()
        s.commands["pacman -Dk"] = (1, "error: missing 'libfoo' dependency for 'bar'\n")
        self.assertEqual(scan(s)["CF01"].level, hc.FAIL)

    def test_pacnew(self):
        s = healthy()
        s.files["/etc/pacman.conf.pacnew"] = ""
        s.files["/etc/skel/.git/objects/pack/x.pacnew"] = ""
        f = scan(s)["CF02"]
        self.assertEqual(f.details, ["/etc/pacman.conf.pacnew"])

    def test_replaced_files(self):
        s = healthy()
        s.files[hc.REPLACED_DIR + "/etc/sddm.conf.d/x.conf"] = ""
        f = scan(s)["CF03"]
        self.assertEqual(f.details, ["/etc/sddm.conf.d/x.conf"])

    def test_contradicting_options(self):
        s = healthy()
        s.files["/etc/modprobe.d/zz.conf"] = "options btusb enable_autosuspend=1\n"
        f = scan(s)["CF04"]
        self.assertEqual(f.level, hc.WARN)
        self.assertIn("btusb.enable_autosuspend", f.details[0])

    def test_equivalent_values_are_not_a_conflict(self):
        s = healthy()
        s.files["/etc/modprobe.d/zz.conf"] = "options btusb enable_autosuspend=N\n"
        self.assertEqual(scan(s)["CF04"].level, hc.OK)

    def test_etc_overrides_usr_lib(self):
        s = healthy()
        s.files["/etc/modprobe.d/bluetooth-usb.conf"] = "# vacío: anula el de /usr/lib\n"
        s.files["/sys/module/btusb/parameters/reset"] = "N\n"
        self.assertEqual(scan(s)["CF06"].level, hc.OK)

    def test_builtin_module_option_ignored(self):
        s = healthy()
        s.files["/etc/modprobe.d/omarchy-usb-autosuspend.conf"] = "options usbcore autosuspend=-1\n"
        f = scan(s)["CF05"]
        self.assertEqual(f.level, hc.WARN)
        self.assertIn("usbcore.autosuspend=-1", f.details[0])

    def test_effective_param_mismatch(self):
        s = healthy()
        s.files["/sys/module/btusb/parameters/enable_autosuspend"] = "Y\n"
        self.assertEqual(scan(s)["CF06"].level, hc.WARN)

    def test_blacklisted_loaded(self):
        s = healthy()
        s.files["/proc/modules"] += "nouveau 16384 0 - Live 0x0\n"
        self.assertEqual(scan(s)["CF07"].level, hc.FAIL)

    def test_blacklisted_but_loaded_on_purpose(self):
        s = healthy()
        s.files["/etc/modprobe.d/blacklist-xpad.conf"] = "blacklist xpad\n"
        s.files["/proc/modules"] += "xpad 53248 0 - Live 0x0\n"
        self.assertEqual(scan(s)["CF07"].level, hc.FAIL)
        s.files["/etc/udev/rules.d/70-claude-xpad.rules"] = (
            '# a comment that says modprobe nouveau does not count\n'
            'ACTION=="add", SUBSYSTEM=="usb", RUN+="/usr/bin/modprobe xpad"\n')
        r = scan(s)["CF07"]
        self.assertEqual(r.level, hc.INFO)
        s.files["/proc/modules"] += "nouveau 16384 0 - Live 0x0\n"
        s.files["/etc/modprobe.d/blacklist-nouveau.conf"] = "blacklist nouveau\n"
        self.assertEqual(scan(s)["CF07"].level, hc.FAIL)

    def test_conflicting_packages(self):
        s = healthy()
        set_package(s, "nvidia-dkms", "610.57.04-1")
        self.assertEqual(scan(s)["CF08"].level, hc.WARN)

    def test_version_skew(self):
        s = healthy()
        set_package(s, "lib32-mesa", "1:26.1.0-1")
        f = scan(s)["CF09"]
        self.assertEqual(f.level, hc.WARN)
        self.assertTrue(f.details[0].startswith("mesa:"))


class BaselineDiff(unittest.TestCase):
    def test_no_changes(self):
        st = hc.collect_state(hc.Facts(healthy()))
        self.assertEqual(hc.diff_states(st, copy.deepcopy(st)), [])

    def test_regressions_and_changes(self):
        old = hc.collect_state(hc.Facts(healthy()))
        s = healthy()
        del s.links["/sys/bus/pci/devices/0000:07:00.0/driver"]
        del s.links["/sys/class/net/wlp7s0/device/driver"]
        del s.files["/sys/class/bluetooth/hci0/x"]
        set_package(s, "mesa", "1:26.3.0-1")
        changes = hc.diff_states(old, hc.collect_state(hc.Facts(s)))
        self.assertIn((hc.FAIL, "PCI 0000:07:00.0 [14c3:7925]: driver mt7925e → -"), changes)
        self.assertIn((hc.FAIL, "interfaz de red wlp7s0 (mt7925e) desapareció"), changes)
        self.assertIn((hc.FAIL, "adaptador Bluetooth hci0 desapareció"), changes)
        self.assertIn((hc.INFO, "paquete mesa: 1:26.2.3-1 → 1:26.3.0-1"), changes)
        self.assertEqual(hc.exit_code(l for l, _ in changes), 2)

    def test_baseline_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            os.environ["XDG_STATE_HOME"] = tmp
            try:
                out = io.StringIO()
                with redirect_stdout(out):
                    self.assertEqual(hc.main(["baseline"], system=healthy()), 0)
                    self.assertEqual(hc.main(["baseline"], system=healthy()), 1)  # no pisa sin --force
                    self.assertEqual(hc.main(["diff"], system=healthy()), 0)
                with open(os.path.join(tmp, "omarchy-hwcheck", "baseline.json")) as fh:
                    self.assertEqual(json.load(fh)["kernel"], REL)
            finally:
                del os.environ["XDG_STATE_HOME"]


if __name__ == "__main__":
    unittest.main()
