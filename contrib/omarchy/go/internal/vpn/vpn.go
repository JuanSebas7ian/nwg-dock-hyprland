// Package vpn drives Surfshark and Proton VPN (Free) through NetworkManager
// for the VPN bar widget: WireGuard profiles without root, DNS exclusive to
// the tunnel, and an IPv6 blackhole when the provider has no IPv6.
package vpn

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SSAPI      = "https://api.surfshark.com/v3/server/clusters/generic"
	SSProfile  = "Surfshark"
	SSIface    = "ss-wg"
	SSAddress  = "10.14.0.2/16"
	SSDNS      = "162.252.172.57, 149.154.159.92"
	V6Blackhol = "fd00:5f5f:5f5f::2/128" // ULA: IPv6 into a tunnel without IPv6 is dropped, apps fall back to IPv4
	IPTTL      = 300
	ClustersTT = 6 * 3600
)

var RegionOrder = []string{"The Americas", "Europe", "Asia Pacific", "Middle East and Africa"}

// Countries Proton's free plan uses, for when the Surfshark list is not cached yet.
var fallbackCountries = map[string][2]string{
	"NL": {"Netherlands", "Europe"}, "JP": {"Japan", "Asia Pacific"}, "US": {"United States", "The Americas"},
	"RO": {"Romania", "Europe"}, "PL": {"Poland", "Europe"}, "CA": {"Canada", "The Americas"},
	"NO": {"Norway", "Europe"}, "SG": {"Singapore", "Asia Pacific"}, "CH": {"Switzerland", "Europe"},
	"MX": {"Mexico", "The Americas"}, "CO": {"Colombia", "The Americas"},
}

var providers = map[string]map[string]string{
	"surfshark": {"label": "Surfshark", "app": "surfshark", "web": "https://my.surfshark.com/vpn/manual-setup/main/wireguard"},
	"proton":    {"label": "Proton", "app": "protonvpn-app", "web": "https://account.protonvpn.com/downloads"},
}

// Paths of the files this package uses.
type Paths struct {
	ConfDir, Configs, SSKey, State, Cache, IPCache, Last, Recent, Session, HomeNet, ClustersFile, Downloads string
}

func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	conf := filepath.Join(env("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "omarchy-vpn")
	state := filepath.Join(env("XDG_STATE_HOME", filepath.Join(home, ".local/state")), "omarchy-vpn")
	cache := filepath.Join(env("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "omarchy-vpn")
	return Paths{ConfDir: conf, Configs: filepath.Join(conf, "configs"), SSKey: filepath.Join(conf, "surfshark.json"), State: state, Cache: cache,
		IPCache: filepath.Join(state, "ip.json"), Last: filepath.Join(state, "last.json"), Recent: filepath.Join(state, "recent.json"),
		Session: filepath.Join(state, "session.json"), HomeNet: filepath.Join(state, "home.json"), ClustersFile: filepath.Join(cache, "surfshark-clusters.json"),
		Downloads: filepath.Join(home, "Downloads")}
}

// M is a JSON object.
type M = map[string]any

func ReadJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

func WriteJSON(path string, v any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	tmp := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	_ = os.Chmod(tmp, mode)
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------- nmcli

func nmcli(check bool, args ...string) (string, error) {
	cmd := exec.Command("nmcli", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil && check {
		msg := strings.TrimSpace(string(out))
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		if msg == "" {
			msg = "nmcli " + args[0] + " failed"
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

// SplitTerse splits nmcli -t output (':' escaped as '\:').
func SplitTerse(line string) []string {
	var parts []string
	cur := strings.Builder{}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) && line[i+1] == ':' {
			cur.WriteByte(':')
			i++
			continue
		}
		if c == ':' {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	return append(parts, cur.String())
}

// ProviderOf guesses the provider from a connection name.
func ProviderOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "surfshark"):
		return "surfshark"
	case strings.Contains(n, "proton") || strings.HasPrefix(n, "pvpn"):
		return "proton"
	}
	return "other"
}

// Conn is a NetworkManager VPN connection.
type Conn struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	Type     string `json:"type"`
	Device   string `json:"device"`
	Active   bool   `json:"active"`
	LastUsed int64  `json:"lastUsed"`
	Provider string `json:"provider"`
	Imported bool   `json:"imported"`
}

func (c Conn) M() M {
	return M{"name": c.Name, "uuid": c.UUID, "type": c.Type, "device": c.Device, "active": c.Active, "lastUsed": c.LastUsed, "provider": c.Provider, "imported": c.Imported}
}

func Connections() []Conn {
	out, _ := nmcli(true, "-t", "-f", "NAME,UUID,TYPE,DEVICE,ACTIVE,TIMESTAMP", "connection", "show")
	var conns []Conn
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := append(SplitTerse(l), "", "", "", "", "", "")
		if t := f[2]; t != "wireguard" && t != "vpn" && t != "tun" {
			continue
		}
		var ts int64
		fmt.Sscan(f[5], &ts)
		conns = append(conns, Conn{Name: f[0], UUID: f[1], Type: f[2], Device: f[3], Active: f[4] == "yes", LastUsed: ts,
			Provider: ProviderOf(f[0]), Imported: f[0] == SSProfile || strings.HasPrefix(f[0], "Proton ")})
	}
	sort.SliceStable(conns, func(i, j int) bool { return strings.ToLower(conns[i].Name) < strings.ToLower(conns[j].Name) })
	return conns
}

// ---------------------------------------------------------------- locations

// Cluster is one Surfshark location.
type Cluster struct {
	ID      string `json:"id"`
	CC      string `json:"cc"`
	Country string `json:"country"`
	City    string `json:"city"`
	Region  string `json:"region"`
	Load    any    `json:"load"`
	PubKey  string `json:"pubKey"`
	Virtual bool   `json:"virtual"`
}

func (c Cluster) M(withKey bool) M {
	m := M{"id": c.ID, "cc": c.CC, "country": c.Country, "city": c.City, "region": c.Region, "load": c.Load, "virtual": c.Virtual}
	if withKey {
		m["pubKey"] = c.PubKey
	}
	return m
}

func httpJSON(url string, timeout time.Duration, out any) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "omarchy-vpn")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Clusters is Surfshark's public location list, cached 6 h; a stale copy beats none offline.
func (p Paths) Clusters(refresh bool) []Cluster {
	var cached struct {
		TS    int64     `json:"ts"`
		Items []Cluster `json:"items"`
	}
	have := ReadJSON(p.ClustersFile, &cached)
	if have && !refresh && time.Now().Unix()-cached.TS < ClustersTT {
		return cached.Items
	}
	var raw []struct {
		ConnectionName string   `json:"connectionName"`
		CountryCode    string   `json:"countryCode"`
		Country        string   `json:"country"`
		Location       string   `json:"location"`
		Region         string   `json:"region"`
		Load           any      `json:"load"`
		PubKey         string   `json:"pubKey"`
		Tags           []string `json:"tags"`
	}
	if err := httpJSON(SSAPI, 8*time.Second, &raw); err != nil {
		return cached.Items
	}
	var items []Cluster
	for _, c := range raw {
		if c.ConnectionName == "" || c.PubKey == "" {
			continue
		}
		virtual := false
		for _, t := range c.Tags {
			virtual = virtual || t == "virtual"
		}
		items = append(items, Cluster{c.ConnectionName, c.CountryCode, c.Country, c.Location, c.Region, c.Load, c.PubKey, virtual})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Country != items[j].Country {
			return items[i].Country < items[j].Country
		}
		return items[i].City < items[j].City
	})
	cached.TS, cached.Items = time.Now().Unix(), items
	_ = WriteJSON(p.ClustersFile, cached, 0o644)
	return items
}

// Countries maps codes to (country, region), with Proton's free countries as fallback.
func Countries(items []Cluster) map[string][2]string {
	m := map[string][2]string{}
	for _, c := range items {
		m[c.CC] = [2]string{c.Country, c.Region}
	}
	for cc, v := range fallbackCountries {
		if _, ok := m[cc]; !ok {
			m[cc] = v
		}
	}
	return m
}

var protonRe = regexp.MustCompile(`^([A-Z]{2})(?:-([A-Z]+))?(?:[-#]?(\d+))?`)

// ProtonLocation: "Proton NL-FREE-7" -> Netherlands, Free #7.
func ProtonLocation(c Conn, names map[string][2]string) M {
	tail := c.Name
	if i := strings.Index(c.Name, " "); i >= 0 {
		tail = c.Name[i+1:]
	}
	m := protonRe.FindStringSubmatch(strings.ToUpper(tail))
	cc := ""
	if m != nil {
		cc = m[1]
	}
	country, region := tail, ""
	if v, ok := names[cc]; ok {
		country, region = v[0], v[1]
	}
	city := tail
	if m != nil && m[2] != "" && m[3] != "" {
		city = strings.ToUpper(m[2][:1]) + strings.ToLower(m[2][1:]) + " #" + m[3]
	}
	return M{"id": c.UUID, "cc": cc, "country": country, "city": city, "region": region, "load": nil, "provider": "proton", "name": c.Name}
}

func (p Paths) Locations(refresh bool) M {
	items := p.Clusters(refresh)
	names := Countries(items)
	proton := []M{}
	for _, c := range Connections() {
		if c.Provider == "proton" && c.Imported {
			proton = append(proton, ProtonLocation(c, names))
		}
	}
	ss := []M{}
	for _, c := range items {
		m := c.M(false)
		m["provider"] = "surfshark"
		ss = append(ss, m)
	}
	var recent []any
	ReadJSON(p.Recent, &recent)
	if recent == nil {
		recent = []any{}
	}
	_, err := os.Stat(p.SSKey)
	return M{"surfshark": ss, "proton": proton, "regions": RegionOrder, "recent": recent, "surfsharkReady": err == nil}
}

// ---------------------------------------------------------------- status

func Traffic(dev string) (any, any) {
	read := func(f string) any {
		b, err := os.ReadFile(filepath.Join("/sys/class/net", dev, "statistics", f))
		if err != nil {
			return nil
		}
		var n int64
		if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &n); err != nil {
			return nil
		}
		return n
	}
	return read("rx_bytes"), read("tx_bytes")
}

var endpointRe = regexp.MustCompile(`endpoint=([^,\s]+)`)

func details(c Conn) M {
	d := M{}
	out, _ := nmcli(false, "-t", "-f", "IP4.ADDRESS,IP4.DNS,IP6.ADDRESS", "connection", "show", c.UUID)
	for _, l := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(l, ":")
		k = strings.SplitN(k, "[", 2)[0]
		if v == "" || k == "" {
			continue
		}
		key := strings.ToLower(k)
		key = strings.Replace(strings.Replace(key, "ip4.", "ipv4_", 1), "ip6.", "ipv6_", 1)
		list, _ := d[key].([]string)
		d[key] = append(list, v)
	}
	if c.Type == "wireguard" {
		ep, _ := nmcli(false, "-t", "-f", "wireguard.peers", "connection", "show", c.UUID)
		if m := endpointRe.FindStringSubmatch(ep); m != nil {
			d["endpoint"] = m[1]
		}
	}
	if c.Device != "" {
		d["rx"], d["tx"] = Traffic(c.Device)
	}
	return d
}

func apps() M {
	res := M{}
	pats := map[string]string{"surfshark": "/opt/Surfshark/surfshark", "proton": "protonvpn-app"}
	for id, p := range providers {
		_, err := exec.LookPath(p["app"])
		running := exec.Command("pgrep", "-f", "-u", fmt.Sprint(os.Getuid()), pats[id]).Run() == nil
		res[id] = M{"installed": err == nil, "running": running}
	}
	return res
}

// PublicIP via am.i.mullvad.net (no account), cached per VPN state.
func (p Paths) PublicIP(refresh bool, key string) M {
	cached := M{}
	ReadJSON(p.IPCache, &cached)
	ts, _ := cached["ts"].(float64)
	if !refresh && cached["key"] == key && float64(time.Now().Unix())-ts < IPTTL {
		return cached
	}
	res := M{"key": key, "ts": time.Now().Unix()}
	var j M
	if err := httpJSON("https://ipv4.am.i.mullvad.net/json", 4*time.Second, &j); err == nil {
		res["ipv4"] = M{"ip": j["ip"], "country": j["country"], "city": j["city"], "organization": j["organization"]}
	} else {
		e := err.Error()
		if len(e) > 120 {
			e = e[:120]
		}
		res["ipv4Error"] = e
	}
	j = nil
	if err := httpJSON("https://ipv6.am.i.mullvad.net/json", 3*time.Second, &j); err == nil {
		res["ipv6"] = M{"ip": j["ip"], "country": j["country"], "organization": j["organization"]}
	} else {
		res["ipv6"] = nil // no IPv6 route = nothing can leak over IPv6
	}
	_ = WriteJSON(p.IPCache, res, 0o644)
	if key == "" && res["ipv4"] != nil {
		_ = WriteJSON(p.HomeNet, M{"ipv4": res["ipv4"], "ipv6": res["ipv6"], "ts": res["ts"]}, 0o644)
	}
	return res
}

func (p Paths) locationFor(c Conn, items []Cluster) M {
	if c.Provider == "surfshark" && c.Name == SSProfile {
		sess := M{}
		ReadJSON(p.Session, &sess)
		host, _ := sess["id"].(string)
		for _, cl := range items {
			if cl.ID == host {
				return M{"id": cl.ID, "cc": cl.CC, "country": cl.Country, "city": cl.City, "region": cl.Region, "load": cl.Load, "provider": "surfshark"}
			}
		}
		country := host
		if country == "" {
			country = "Surfshark"
		}
		return M{"id": sess["id"], "cc": "", "country": country, "city": "", "region": "", "load": nil, "provider": "surfshark"}
	}
	if c.Provider == "proton" && c.Imported {
		return ProtonLocation(c, Countries(items))
	}
	return M{"id": c.UUID, "cc": "", "country": c.Name, "city": "", "region": "", "load": nil, "provider": c.Provider}
}

func (p Paths) Status(refreshIP bool) M {
	conns := Connections()
	var active []Conn
	for _, c := range conns {
		if c.Active {
			active = append(active, c)
		}
	}
	items := p.Clusters(false)
	var cur M
	if len(active) > 0 {
		cur = active[0].M()
		for k, v := range details(active[0]) {
			cur[k] = v
		}
		cur["location"] = p.locationFor(active[0], items)
		sess := M{}
		ReadJSON(p.Session, &sess)
		cur["since"] = nil
		if sess["uuid"] == active[0].UUID {
			cur["since"] = sess["since"]
		}
	}
	var uuids []string
	for _, c := range active {
		uuids = append(uuids, c.UUID)
	}
	ip := p.PublicIP(refreshIP, strings.Join(uuids, ","))
	v6, _ := ip["ipv6"].(M)
	v4, _ := ip["ipv4"].(M)
	if v6 == nil {
		if mm, ok := ip["ipv6"].(map[string]any); ok {
			v6 = mm
		}
	}
	// IPv6 leak: a tunnel is up but IPv6 still reaches the internet through another network.
	leak := len(active) > 0 && v6 != nil && v6["ip"] != nil && v6["ip"] != "" && v4 != nil && v4["organization"] != nil && v4["organization"] != "" && v6["organization"] != v4["organization"]
	last := M{}
	ReadJSON(p.Last, &last)
	switch last["provider"] {
	case "surfshark":
		last["location"] = nil
		for _, cl := range items {
			if cl.ID == last["id"] {
				m := cl.M(false)
				m["provider"] = "surfshark"
				last["location"] = m
			}
		}
	case "proton":
		last["location"] = nil
		for _, c := range conns {
			if c.UUID == last["id"] {
				last["location"] = ProtonLocation(c, Countries(items))
			}
		}
	}
	proton, other := []M{}, []M{}
	for _, c := range conns {
		if c.Provider == "proton" && c.Imported {
			proton = append(proton, ProtonLocation(c, Countries(items)))
		}
		if c.Provider == "other" || !c.Imported {
			other = append(other, c.M())
		}
	}
	inbox := []string{}
	for _, f := range p.DownloadsCandidates() {
		inbox = append(inbox, f)
	}
	_, keyErr := os.Stat(p.SSKey)
	web := M{}
	for id, v := range providers {
		web[id] = v["web"]
	}
	return M{"connected": len(active) > 0, "current": cur, "ip": ip, "ipv6Leak": leak, "last": last,
		"setup": M{"surfshark": keyErr == nil, "surfsharkLocations": len(items), "surfsharkKey": p.KeyInfo(), "proton": proton},
		"other": other, "apps": apps(), "inbox": inbox, "web": web}
}

// ---------------------------------------------------------------- connect

func Down() {
	for _, c := range Connections() {
		if c.Active {
			_, _ = nmcli(false, "connection", "down", "uuid", c.UUID)
		}
	}
}

func (p Paths) remember(provider, id, uuid, label string) {
	entry := M{"provider": provider, "id": id}
	_ = WriteJSON(p.Last, entry, 0o644)
	_ = WriteJSON(p.Session, M{"uuid": uuid, "id": id, "provider": provider, "since": time.Now().Unix()}, 0o644)
	var recent []M
	ReadJSON(p.Recent, &recent)
	out := []M{{"provider": provider, "id": id, "label": label}}
	for _, r := range recent {
		if r["provider"] != provider || r["id"] != id {
			out = append(out, r)
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	_ = WriteJSON(p.Recent, out, 0o644)
}

// dnsProbe sends one tiny DNS query so WireGuard handshakes and something comes back.
func dnsProbe(server string) bool {
	id := make([]byte, 2)
	_, _ = rand.Read(id)
	q := append(id, []byte("\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x09surfshark\x03com\x00\x00\x01\x00\x01")...)
	c, err := net.DialTimeout("udp", net.JoinHostPort(server, "53"), time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if _, err := c.Write(q); err != nil {
		return false
	}
	buf := make([]byte, 512)
	_, err = c.Read(buf)
	return err == nil
}

// verifyTunnel: WireGuard "connects" locally even with a key the server does
// not know; only bytes received through the tunnel prove the handshake.
func verifyTunnel(uuid, dns string, timeout time.Duration) bool {
	dev := ""
	for _, c := range Connections() {
		if c.UUID == uuid {
			dev = c.Device
		}
	}
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		dnsProbe(dns)
		if dev != "" {
			if rx, _ := Traffic(dev); rx != nil && rx.(int64) > 0 {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func firstDNS(uuid, def string) string {
	out, _ := nmcli(false, "-g", "ipv4.dns", "connection", "show", uuid)
	out = strings.TrimSpace(out)
	if out == "" {
		return def
	}
	return strings.TrimSpace(strings.Split(out, ",")[0])
}

func bringUp(uuid, dns, failMsg string) error {
	if _, err := nmcli(true, "--wait", "20", "connection", "up", "uuid", uuid); err != nil {
		return err
	}
	if !verifyTunnel(uuid, dns, 8*time.Second) {
		_, _ = nmcli(false, "connection", "down", "uuid", uuid)
		return fmt.Errorf("%s", failMsg)
	}
	return nil
}

// SSKey is ~/.config/omarchy-vpn/surfshark.json.
type SSKey struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey,omitempty"`
	Address    string `json:"address"`
	DNS        string `json:"dns"`
	Source     string `json:"source"`
	Verified   bool   `json:"verified"`
	Created    int64  `json:"created"`
}

// SurfsharkConf writes the profile of one location with the account's key.
func SurfsharkConf(host, pubKey string, k SSKey) string {
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s\nDNS = %s\n\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0\nEndpoint = %s:51820\n", k.PrivateKey, k.Address, k.DNS, pubKey, host)
}

func (p Paths) connectSurfshark(host string) (M, error) {
	var key SSKey
	if !ReadJSON(p.SSKey, &key) {
		return nil, fmt.Errorf("Surfshark is not set up: create the key in Accounts & servers first")
	}
	var loc *Cluster
	find := func(items []Cluster) {
		for i := range items {
			if items[i].ID == host {
				loc = &items[i]
			}
		}
	}
	find(p.Clusters(false))
	if loc == nil {
		find(p.Clusters(true))
	}
	if loc == nil {
		return nil, fmt.Errorf("unknown Surfshark location %s", host)
	}
	Down()
	for _, c := range Connections() {
		if c.Name == SSProfile {
			if _, err := nmcli(true, "connection", "delete", "uuid", c.UUID); err != nil {
				return nil, err
			}
		}
	}
	uuid, err := NMImport(SurfsharkConf(loc.ID, loc.PubKey, key), SSIface, SSProfile)
	if err != nil {
		return nil, err
	}
	msg := loc.City + " did not answer in 8 s; try another location."
	if !key.Verified {
		msg = "Surfshark did not answer: the key is not registered yet. Paste the public key at my.surfshark.com → VPN → Manual setup → WireGuard → I have a key pair, Save, and connect again."
	}
	if err := bringUp(uuid, strings.TrimSpace(strings.Split(key.DNS, ",")[0]), msg); err != nil {
		return nil, err
	}
	if !key.Verified {
		key.Verified = true
		_ = WriteJSON(p.SSKey, key, 0o600)
	}
	name := loc.City + ", " + loc.Country
	p.remember("surfshark", host, uuid, name)
	return M{"ok": true, "name": name}, nil
}

func (p Paths) connectProton(uuid string) (M, error) {
	var conn *Conn
	conns := Connections()
	for i := range conns {
		if conns[i].UUID == uuid {
			conn = &conns[i]
		}
	}
	if conn == nil {
		return nil, fmt.Errorf("that Proton server is not imported any more")
	}
	for _, o := range conns {
		if o.Active && o.UUID != uuid {
			_, _ = nmcli(false, "connection", "down", "uuid", o.UUID)
		}
	}
	if err := bringUp(uuid, firstDNS(uuid, "10.2.0.1"), "The Proton server did not answer in 8 s (free servers get busy; or the config was revoked). Try another one."); err != nil {
		return nil, err
	}
	loc := ProtonLocation(*conn, Countries(p.Clusters(false)))
	p.remember("proton", uuid, uuid, fmt.Sprintf("%v %v", loc["country"], loc["city"]))
	return M{"ok": true, "name": conn.Name}, nil
}

func (p Paths) Connect(provider, id string) (M, error) {
	switch provider {
	case "surfshark":
		return p.connectSurfshark(id)
	case "proton":
		return p.connectProton(id)
	}
	return nil, fmt.Errorf("unknown provider %s", provider)
}

func (p Paths) Toggle() (M, error) {
	for _, c := range Connections() {
		if c.Active {
			Down()
			return M{"ok": true, "action": "down"}, nil
		}
	}
	last := M{}
	if !ReadJSON(p.Last, &last) || last["provider"] == nil {
		return nil, fmt.Errorf("no VPN used yet: pick a location in the panel")
	}
	r, err := p.Connect(fmt.Sprint(last["provider"]), fmt.Sprint(last["id"]))
	if err != nil {
		return nil, err
	}
	r["action"] = "up"
	return r, nil
}

// ---------------------------------------------------------------- import

// Conf is a parsed WireGuard config.
type Conf struct {
	Interface, Peer map[string]string
}

func ParseConf(text string) (Conf, []string) {
	c := Conf{Interface: map[string]string{}, Peer: map[string]string{}}
	var comments []string
	sec := ""
	for _, raw := range strings.Split(text, "\n") {
		l := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(l, "#"):
			comments = append(comments, l)
		case strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]"):
			sec = l[1 : len(l)-1]
		case strings.Contains(l, "="):
			k, v, _ := strings.Cut(l, "=")
			switch sec {
			case "Interface":
				c.Interface[strings.TrimSpace(k)] = strings.TrimSpace(v)
			case "Peer":
				c.Peer[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	return c, comments
}

func LooksLikeWG(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.Size() > 20000 {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	c, _ := ParseConf(string(b))
	return c.Interface["PrivateKey"] != "" && c.Peer["PublicKey"] != "" && c.Peer["Endpoint"] != ""
}

// DetectProvider from the file name, endpoint, DNS or comments ("" = unknown).
func DetectProvider(path string, c Conf, comments []string) string {
	name := strings.ToLower(filepath.Base(path))
	ep := strings.ToLower(c.Peer["Endpoint"])
	if strings.Contains(name, "surfshark") || strings.Contains(ep, "surfshark") {
		return "surfshark"
	}
	if strings.HasPrefix(name, "wg-") || strings.HasPrefix(name, "proton") || strings.Contains(c.Interface["DNS"], "10.2.0.1") {
		return "proton"
	}
	for _, cm := range comments {
		l := strings.ToLower(cm)
		if strings.Contains(l, "netshield") || strings.Contains(l, "proton") {
			return "proton"
		}
	}
	return ""
}

var locPrefix = regexp.MustCompile(`(?i)^(wg|proton|protonvpn)[-_]`)

// LocationOf: wg-US-FREE-12.conf -> US-FREE-12.
func LocationOf(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.ReplaceAll(strings.ToUpper(locPrefix.ReplaceAllString(stem, "")), "_", "-")
}

var addrLine = regexp.MustCompile(`(?i)^address\s*=`)
var allowedLine = regexp.MustCompile(`(?i)^allowedips\s*=`)

// AddV6Blackhole routes IPv6 into the tunnel when the provider has none, so it can't bypass it.
func AddV6Blackhole(text string, c Conf) (string, bool) {
	if strings.Contains(c.Interface["Address"], ":") || !strings.Contains(c.Peer["AllowedIPs"], "0.0.0.0/0") {
		return text, false
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		s := strings.TrimSpace(l)
		switch {
		case addrLine.MatchString(s):
			l = strings.TrimRight(l, " \t") + ", " + V6Blackhol
		case allowedLine.MatchString(s) && !strings.Contains(s, "::/0"):
			l = strings.TrimRight(l, " \t") + ", ::/0"
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n") + "\n", true
}

var notAlnum = regexp.MustCompile(`[^a-z0-9]`)

// IfaceName: a valid (<= 15 chars), unused interface name.
func IfaceName(prefix, location string, taken map[string]bool) string {
	base := prefix + "-" + notAlnum.ReplaceAllString(strings.ToLower(location), "")
	if len(base) > 13 {
		base = base[:13]
	}
	name, n := base, 2
	for taken[name] {
		b := base
		if len(b) > 12 {
			b = b[:12]
		}
		name = fmt.Sprintf("%s%d", b, n)
		n++
	}
	return name
}

var uuidRe = regexp.MustCompile(`\(([0-9a-f-]{36})\)`)

// NMImport imports a WireGuard config with the leak protections; returns its uuid.
func NMImport(text, iface, name string) (string, error) {
	c, _ := ParseConf(text)
	text, _ = AddV6Blackhole(text, c)
	dir, err := os.MkdirTemp("", "omarchy-vpn")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, iface+".conf") // nmcli names the interface after the file
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return "", err
	}
	out, err := nmcli(true, "connection", "import", "type", "wireguard", "file", tmp)
	if err != nil {
		return "", err
	}
	m := uuidRe.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("nmcli import gave no uuid: %s", strings.TrimSpace(out))
	}
	_, err = nmcli(true, "connection", "modify", "uuid", m[1], "connection.id", name, "connection.autoconnect", "no",
		"ipv4.dns-priority", "-50", "ipv6.dns-priority", "-50", "ipv4.dns-search", "~.")
	return m[1], err
}

func (p Paths) keepConf(path string) error {
	if err := os.MkdirAll(p.Configs, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(p.ConfDir, 0o700)
	_ = os.Chmod(p.Configs, 0o700)
	dest := filepath.Join(p.Configs, filepath.Base(path))
	a, _ := filepath.Abs(path)
	if a != dest {
		if err := os.Rename(path, dest); err != nil {
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return err
			}
			if err := os.WriteFile(dest, b, 0o600); err != nil {
				return err
			}
			_ = os.Remove(path)
		}
	}
	return os.Chmod(dest, 0o600)
}

func (p Paths) importOne(path, provider string) (M, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(b)
	c, comments := ParseConf(text)
	base := filepath.Base(path)
	if c.Interface["PrivateKey"] == "" || c.Peer["Endpoint"] == "" {
		return nil, fmt.Errorf("%s: not a WireGuard config", base)
	}
	if provider == "" {
		provider = DetectProvider(path, c, comments)
	}
	switch provider {
	case "surfshark":
		// One key pair serves every location: keep the key, the profile is written per location.
		addr := c.Interface["Address"]
		if addr == "" {
			addr = SSAddress
		}
		dns := c.Interface["DNS"]
		if dns == "" {
			dns = SSDNS
		}
		k := SSKey{PrivateKey: c.Interface["PrivateKey"], Address: strings.TrimSpace(strings.Split(addr, ",")[0]), DNS: dns, Source: "imported", Created: time.Now().Unix()}
		if err := WriteJSON(p.SSKey, k, 0o600); err != nil {
			return nil, err
		}
		_ = os.Chmod(p.ConfDir, 0o700)
		return M{"name": "Surfshark key", "provider": "surfshark"}, p.keepConf(path)
	case "proton":
	default:
		return nil, fmt.Errorf("%s: provider not recognised (use --provider surfshark|proton)", base)
	}
	loc := LocationOf(path)
	name := "Proton " + loc
	for _, cn := range Connections() {
		if cn.Name == name {
			if _, err := nmcli(true, "connection", "delete", "uuid", cn.UUID); err != nil {
				return nil, err
			}
		}
	}
	taken := map[string]bool{}
	if ents, err := os.ReadDir("/sys/class/net"); err == nil {
		for _, e := range ents {
			taken[e.Name()] = true
		}
	}
	names, _ := nmcli(false, "-t", "-f", "connection.interface-name", "connection", "show")
	for _, n := range strings.Split(names, "\n") {
		taken[n] = true
	}
	uuid, err := NMImport(text, IfaceName("pv", loc, taken), name)
	if err != nil {
		return nil, err
	}
	return M{"name": name, "uuid": uuid, "provider": "proton"}, p.keepConf(path)
}

func (p Paths) DownloadsCandidates() []string {
	files, _ := filepath.Glob(filepath.Join(p.Downloads, "*.conf"))
	sort.Strings(files)
	var out []string
	for _, f := range files {
		if LooksLikeWG(f) {
			out = append(out, f)
		}
	}
	return out
}

func (p Paths) Import(paths []string, provider string) M {
	if len(paths) == 0 {
		paths = p.DownloadsCandidates()
	}
	done, errs := []M{}, []string{}
	for _, f := range paths {
		r, err := p.importOne(f, provider)
		if err != nil {
			errs = append(errs, filepath.Base(f)+": "+err.Error()) // one bad file must not stop the rest
			continue
		}
		done = append(done, r)
	}
	errText := strings.Join(errs, "; ")
	if errText == "" && len(done) == 0 {
		errText = "no WireGuard .conf in ~/Downloads"
	}
	return M{"ok": len(errs) == 0 && len(done) > 0, "imported": done, "errors": errs, "error": errText}
}

func Delete(ref string) (M, error) {
	for _, c := range Connections() {
		if c.UUID == ref || c.Name == ref {
			if !c.Imported {
				break
			}
			if _, err := nmcli(true, "connection", "delete", "uuid", c.UUID); err != nil {
				return nil, err
			}
			return M{"ok": true}, nil
		}
	}
	return nil, fmt.Errorf("only servers imported here can be removed from the panel")
}

func (p Paths) ForgetSurfshark() M {
	for _, c := range Connections() {
		if c.Name == SSProfile {
			_, _ = nmcli(false, "connection", "delete", "uuid", c.UUID)
		}
	}
	_ = os.Remove(p.SSKey)
	return M{"ok": true}
}

func wg(stdin string, args ...string) (string, error) {
	cmd := exec.Command("wg", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		msg := "wg failed (is wireguard-tools installed?)"
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func (p Paths) KeyInfo() any {
	var k SSKey
	if !ReadJSON(p.SSKey, &k) {
		return nil
	}
	pub := k.PublicKey
	if pub == "" {
		pub, _ = wg(k.PrivateKey+"\n", "pubkey")
	}
	src := k.Source
	if src == "" {
		src = "imported"
	}
	return M{"publicKey": pub, "source": src, "verified": k.Verified, "created": k.Created}
}

// SurfsharkKeygen makes the key pair here: the private key never leaves the PC.
func (p Paths) SurfsharkKeygen(force bool) (M, error) {
	if _, err := os.Stat(p.SSKey); err == nil && !force {
		return nil, fmt.Errorf("a Surfshark key is already set up (use --force to replace it)")
	}
	priv, err := wg("", "genkey")
	if err != nil {
		return nil, err
	}
	pub, err := wg(priv+"\n", "pubkey")
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(p.ConfDir, 0o700)
	_ = os.Chmod(p.ConfDir, 0o700)
	if err := WriteJSON(p.SSKey, SSKey{PrivateKey: priv, PublicKey: pub, Address: SSAddress, DNS: SSDNS, Source: "generated", Created: time.Now().Unix()}, 0o600); err != nil {
		return nil, err
	}
	copied := false
	if _, err := exec.LookPath("wl-copy"); err == nil && os.Getenv("OMARCHY_VPN_NO_CLIPBOARD") == "" {
		// wl-copy leaves a child serving the clipboard: never wait on its output pipes.
		cmd := exec.Command("wl-copy", pub)
		copied = cmd.Run() == nil
	}
	return M{"ok": true, "publicKey": pub, "copied": copied}, nil
}

var identRe = regexp.MustCompile(`^[a-z0-9]{8,40}$`)

// Leaktest asks bash.ws which resolvers answered random names under a fresh id.
func (p Paths) Leaktest() (M, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("https://bash.ws/id")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 128)
	n, _ := resp.Body.Read(b)
	resp.Body.Close()
	ident := strings.TrimSpace(string(b[:n]))
	if !identRe.MatchString(ident) {
		return nil, fmt.Errorf("the leak test service gave an unexpected answer")
	}
	done := make(chan struct{}, 10)
	for i := 1; i <= 10; i++ {
		go func(i int) { _, _ = net.LookupHost(fmt.Sprintf("%d.%s.bash.ws", i, ident)); done <- struct{}{} }(i)
	}
	timeout := time.After(5 * time.Second)
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-timeout:
			i = 10
		}
	}
	var rows []M
	if err := httpJSON("https://bash.ws/dnsleak/test/"+ident+"?json", 8*time.Second, &rows); err != nil {
		return nil, err
	}
	var ips, dns, leaks []M
	connected := false
	for _, c := range Connections() {
		connected = connected || c.Active
	}
	home := M{}
	ReadJSON(p.HomeNet, &home)
	isp := map[string]bool{}
	for _, k := range []string{"ipv4", "ipv6"} {
		if m, ok := home[k].(map[string]any); ok {
			if org, _ := m["organization"].(string); org != "" {
				isp[strings.ToLower(strings.Fields(org)[0])] = true
			}
		}
	}
	for _, r := range rows {
		t, _ := r["type"].(string)
		if t != "ip" && t != "dns" {
			continue
		}
		org := r["org"]
		if org == nil || org == "" {
			org = r["asn"]
		}
		row := M{"ip": r["ip"], "country": r["country_name"], "org": org, "type": t}
		if t == "ip" {
			ips = append(ips, row)
		} else {
			dns = append(dns, row)
		}
		o := strings.ToLower(fmt.Sprint(org))
		if f := strings.Fields(o); connected && len(isp) > 0 && len(f) > 0 && isp[f[0]] {
			leaks = append(leaks, row)
		}
	}
	ispName := ""
	for k := range isp {
		ispName = k
	}
	verdict := "no leaks"
	switch {
	case !connected:
		verdict = "no VPN: everything shows your ISP"
	case len(leaks) > 0:
		verdict = "leak: your ISP still sees traffic"
	}
	nz := func(x []M) []M {
		if x == nil {
			return []M{}
		}
		return x
	}
	return M{"ok": true, "connected": connected, "ips": nz(ips), "dns": nz(dns), "leaks": nz(leaks), "isp": ispName, "verdict": verdict, "ts": time.Now().Unix()}, nil
}
