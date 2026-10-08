"""Tests for bin/omarchy-vpn without NetworkManager: config parsing, provider
detection, interface names and the IPv6 blackhole that stops leaks.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_vpn
"""

import tempfile
import unittest
from importlib.machinery import SourceFileLoader
from importlib.util import module_from_spec, spec_from_loader
from pathlib import Path

PATH = Path(__file__).resolve().parent.parent / "bin" / "omarchy-vpn"
_loader = SourceFileLoader("omarchy_vpn", str(PATH))
vpn = module_from_spec(spec_from_loader(_loader.name, _loader))
_loader.exec_module(vpn)

KEY = "A" * 43 + "="
SURFSHARK = f"""[Interface]
Address = 10.14.0.2/16
PrivateKey = {KEY}
DNS = 162.252.172.57, 149.154.159.92

[Peer]
PublicKey = {KEY}
AllowedIPs = 0.0.0.0/0
Endpoint = us-nyc.prod.surfshark.com:51820
"""
PROTON = f"""[Interface]
# Key for laptop
# NetShield = 0
PrivateKey = {KEY}
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1

[Peer]
# JP-FREE#3
PublicKey = {KEY}
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 203.0.113.10:51820
"""


class Vpn(unittest.TestCase):
  def setUp(self):
    self.tmp = tempfile.TemporaryDirectory()
    self.dir = Path(self.tmp.name)

  def tearDown(self):
    self.tmp.cleanup()

  def write(self, name, text):
    p = self.dir / name
    p.write_text(text)
    return p

  def test_detects_providers_and_locations(self):
    s = self.write("us-nyc.prod.surfshark.com_wg.conf", SURFSHARK)
    p = self.write("wg-JP-FREE-3.conf", PROTON)
    for path, prov, loc in ((s, "surfshark", "US-NYC"), (p, "proton", "JP-FREE-3")):
      data, comments = vpn.parse_conf(path.read_text())
      self.assertEqual(vpn.detect_provider(path, data, comments), prov)
      self.assertEqual(vpn.location_of(path, prov), loc)

  def test_unknown_provider_is_none(self):
    p = self.write("home.conf", SURFSHARK.replace("surfshark.com", "example.org"))
    data, comments = vpn.parse_conf(p.read_text())
    self.assertIsNone(vpn.detect_provider(p, data, comments))

  def test_ipv6_blackhole_only_when_provider_has_no_ipv6(self):
    data, _ = vpn.parse_conf(SURFSHARK)
    text, added = vpn.add_v6_blackhole(SURFSHARK, data)
    self.assertTrue(added)
    d2, _ = vpn.parse_conf(text)
    self.assertIn(vpn.V6_BLACKHOLE, d2["Interface"]["Address"])
    self.assertIn("::/0", d2["Peer"]["AllowedIPs"])
    data, _ = vpn.parse_conf(PROTON)
    self.assertEqual(vpn.add_v6_blackhole(PROTON, data), (PROTON, False))

  def test_split_tunnel_is_left_alone(self):
    conf = SURFSHARK.replace("0.0.0.0/0", "10.0.0.0/8")
    data, _ = vpn.parse_conf(conf)
    self.assertFalse(vpn.add_v6_blackhole(conf, data)[1])

  def test_interface_names_are_valid_and_unique(self):
    a = vpn.iface_name("surfshark", "US-NYC-VERY-LONG-NAME", set())
    self.assertLessEqual(len(a), 15)
    self.assertRegex(a, r"^ss-[a-z0-9]+$")
    b = vpn.iface_name("surfshark", "US-NYC-VERY-LONG-NAME", {a})
    self.assertNotEqual(a, b)
    self.assertLessEqual(len(b), 15)

  def test_looks_like_wg(self):
    self.assertTrue(vpn.looks_like_wg(self.write("a.conf", PROTON)))
    self.assertFalse(vpn.looks_like_wg(self.write("b.conf", "[core]\nfoo = bar\n")))

  def test_terse_split_unescapes_colons(self):
    self.assertEqual(vpn.split_terse(r"Proton JP\:1:uuid:wireguard"), ["Proton JP:1", "uuid", "wireguard"])

  def test_provider_of_names(self):
    self.assertEqual(vpn.provider_of("Surfshark US-NYC"), "surfshark")
    self.assertEqual(vpn.provider_of("ProtonVPN JP#3"), "proton")
    self.assertEqual(vpn.provider_of("Office"), "other")


if __name__ == "__main__":
  unittest.main()
