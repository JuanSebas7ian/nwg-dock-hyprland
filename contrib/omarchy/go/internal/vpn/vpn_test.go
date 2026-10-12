package vpn

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var key = strings.Repeat("A", 43) + "="

var surfshark = `[Interface]
Address = 10.14.0.2/16
PrivateKey = ` + key + `
DNS = 162.252.172.57, 149.154.159.92

[Peer]
PublicKey = ` + key + `
AllowedIPs = 0.0.0.0/0
Endpoint = us-nyc.prod.surfshark.com:51820
`

var proton = `[Interface]
# Key for laptop
# NetShield = 0
PrivateKey = ` + key + `
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1

[Peer]
# JP-FREE#3
PublicKey = ` + key + `
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 203.0.113.10:51820
`

func write(t *testing.T, name, text string) string {
	p := filepath.Join(t.TempDir(), name)
	_ = os.WriteFile(p, []byte(text), 0o600)
	return p
}

func TestDetectsProvidersAndLocations(t *testing.T) {
	s := write(t, "us-nyc.prod.surfshark.com_wg.conf", surfshark)
	p := write(t, "wg-JP-FREE-3.conf", proton)
	for path, want := range map[string]string{s: "surfshark", p: "proton"} {
		b, _ := os.ReadFile(path)
		c, cm := ParseConf(string(b))
		if got := DetectProvider(path, c, cm); got != want {
			t.Errorf("%s: %s", path, got)
		}
	}
	if LocationOf(p) != "JP-FREE-3" {
		t.Fatal(LocationOf(p))
	}
}

func TestUnknownProvider(t *testing.T) {
	text := strings.ReplaceAll(surfshark, "surfshark.com", "example.org")
	c, cm := ParseConf(text)
	if got := DetectProvider("/x/home.conf", c, cm); got != "" {
		t.Fatal(got)
	}
}

func TestIPv6BlackholeOnlyWithoutProviderIPv6(t *testing.T) {
	c, _ := ParseConf(surfshark)
	text, added := AddV6Blackhole(surfshark, c)
	c2, _ := ParseConf(text)
	if !added || !strings.Contains(c2.Interface["Address"], V6Blackhol) || !strings.Contains(c2.Peer["AllowedIPs"], "::/0") {
		t.Fatal(text)
	}
	c, _ = ParseConf(proton)
	if out, added := AddV6Blackhole(proton, c); added || out != proton {
		t.Fatal("proton changed")
	}
}

func TestSplitTunnelLeftAlone(t *testing.T) {
	conf := strings.ReplaceAll(surfshark, "0.0.0.0/0", "10.0.0.0/8")
	c, _ := ParseConf(conf)
	if _, added := AddV6Blackhole(conf, c); added {
		t.Fatal("split tunnel changed")
	}
}

func TestIfaceNames(t *testing.T) {
	a := IfaceName("ss", "US-NYC-VERY-LONG-NAME", map[string]bool{})
	if len(a) > 15 || !regexp.MustCompile(`^ss-[a-z0-9]+$`).MatchString(a) {
		t.Fatal(a)
	}
	b := IfaceName("ss", "US-NYC-VERY-LONG-NAME", map[string]bool{a: true})
	if b == a || len(b) > 15 {
		t.Fatal(b)
	}
}

func TestLooksLikeWG(t *testing.T) {
	if !LooksLikeWG(write(t, "a.conf", proton)) || LooksLikeWG(write(t, "b.conf", "[core]\nfoo = bar\n")) {
		t.Fatal("looks_like_wg")
	}
}

func TestSplitTerse(t *testing.T) {
	got := SplitTerse(`Proton JP\:1:uuid:wireguard`)
	if strings.Join(got, "|") != "Proton JP:1|uuid|wireguard" {
		t.Fatal(got)
	}
}

func TestProtonLocation(t *testing.T) {
	loc := ProtonLocation(Conn{Name: "Proton NL-FREE-7", UUID: "u"}, Countries(nil))
	if loc["cc"] != "NL" || loc["country"] != "Netherlands" || loc["city"] != "Free #7" || loc["region"] != "Europe" {
		t.Fatal(loc)
	}
}

func TestSurfsharkConf(t *testing.T) {
	conf := SurfsharkConf("co-bog.prod.surfshark.com", "PUB=", SSKey{PrivateKey: key, Address: "10.14.0.2/16", DNS: "162.252.172.57"})
	c, _ := ParseConf(conf)
	if c.Peer["Endpoint"] != "co-bog.prod.surfshark.com:51820" || c.Peer["PublicKey"] != "PUB=" {
		t.Fatal(conf)
	}
	if _, added := AddV6Blackhole(conf, c); !added {
		t.Fatal("no blackhole")
	}
}

func TestProviderOf(t *testing.T) {
	if ProviderOf("Surfshark US-NYC") != "surfshark" || ProviderOf("ProtonVPN JP#3") != "proton" || ProviderOf("Office") != "other" {
		t.Fatal("provider_of")
	}
}

func TestKeyFileIsPrivate(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "k.json")
	if err := WriteJSON(p, SSKey{PrivateKey: key}, 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
}
