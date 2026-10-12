package agents

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- schemaless protobuf

type pbField struct {
	num, wire int
	varint    uint64
	bytes     []byte
}

func pbVarint(b []byte, pos int) (uint64, int, error) {
	var v uint64
	for shift := 0; shift < 64; shift += 7 {
		if pos >= len(b) {
			return 0, pos, errors.New("truncated varint")
		}
		c := b[pos]
		pos++
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, pos, nil
		}
	}
	return 0, pos, errors.New("varint too long")
}

// pbFields splits one protobuf message (no schema).
func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	for pos := 0; pos < len(b); {
		key, p, err := pbVarint(b, pos)
		if err != nil {
			return out, err
		}
		pos = p
		f := pbField{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			if f.varint, pos, err = pbVarint(b, pos); err != nil {
				return out, err
			}
		case 1, 5:
			n := 8
			if f.wire == 5 {
				n = 4
			}
			if pos+n > len(b) {
				return out, errors.New("truncated")
			}
			f.bytes, pos = b[pos:pos+n], pos+n
		case 2:
			var size uint64
			if size, pos, err = pbVarint(b, pos); err != nil {
				return out, err
			}
			if pos+int(size) > len(b) || int(size) < 0 {
				return out, errors.New("truncated")
			}
			f.bytes, pos = b[pos:pos+int(size)], pos+int(size)
		default:
			return out, errors.New("unsupported wire type")
		}
		out = append(out, f)
	}
	return out, nil
}

func pbFirst(b []byte, num, wire int) (pbField, bool) {
	fs, _ := pbFields(b)
	for _, f := range fs {
		if f.num == num && f.wire == wire {
			return f, true
		}
	}
	return pbField{}, false
}

// Generation is one gen_metadata row.
type Generation struct {
	When                           time.Time
	Model                          string
	In, Out, CacheRead, CacheWrite int
}

// ParseGeneration reads a gen_metadata protobuf: field 1 -> 4 = ModelUsageStats
// (2 input, 3 output, 4 cache write, 5 cache read), 1 -> 19 = model, 1 -> 9 -> 4 -> 1 = time.
func ParseGeneration(blob []byte) (Generation, bool) {
	meta, ok := pbFirst(blob, 1, 2)
	if !ok {
		return Generation{}, false
	}
	usage, ok := pbFirst(meta.bytes, 4, 2)
	if !ok {
		return Generation{}, false
	}
	u := map[int]int{}
	fs, _ := pbFields(usage.bytes)
	for _, f := range fs {
		if f.wire == 0 {
			u[f.num] = int(f.varint)
		}
	}
	g := Generation{Model: "unknown", In: u[2], Out: u[3], CacheRead: u[5], CacheWrite: u[4]}
	if m, ok := pbFirst(meta.bytes, 19, 2); ok && len(m.bytes) > 0 {
		g.Model = string(m.bytes)
	}
	timing, ok := pbFirst(meta.bytes, 9, 2)
	if !ok {
		return g, false
	}
	stamp, ok := pbFirst(timing.bytes, 4, 2)
	if !ok {
		return g, false
	}
	sec, ok := pbFirst(stamp.bytes, 1, 0)
	if !ok {
		return g, false
	}
	g.When = time.Unix(int64(sec.varint), 0)
	return g, true
}

// AntigravityStats reads every conversation database.
func AntigravityStats(roots []string, now time.Time) *Stats {
	s := NewStats(now)
	for _, root := range roots {
		dbs, _ := filepath.Glob(filepath.Join(root, "conversations", "*.db"))
		sort.Strings(dbs)
		for _, db := range dbs {
			rows, err := SQLite(db, "SELECT hex(data) AS h FROM gen_metadata")
			if err != nil {
				continue
			}
			session := filepath.Base(root) + ":" + strings.TrimSuffix(filepath.Base(db), ".db")
			for _, r := range rows {
				h, _ := r["h"].(string)
				b, err := hex.DecodeString(h)
				if err != nil {
					continue
				}
				if g, ok := ParseGeneration(b); ok {
					s.Add(session, g.When, g.Model, g.In, g.Out, g.CacheRead, g.CacheWrite)
				}
			}
		}
	}
	return s
}

// ---------------------------------------------------------------- quotas

// Quota is what GetUserStatus says (cached while Antigravity is closed).
type Quota struct {
	Limits    []map[string]any `json:"limits"`
	TierLabel string           `json:"tierLabel"`
	FetchedAt string           `json:"fetchedAt,omitempty"`
}

// QuotaFromStatus turns the RPC answer into meters, the most used first.
func QuotaFromStatus(status map[string]any) Quota {
	q := Quota{Limits: []map[string]any{}}
	user, _ := status["userStatus"].(map[string]any)
	cfgData, _ := user["cascadeModelConfigData"].(map[string]any)
	configs, _ := cfgData["clientModelConfigs"].([]any)
	for _, c := range configs {
		cfg, _ := c.(map[string]any)
		quota, _ := cfg["quotaInfo"].(map[string]any)
		_, hasRem := quota["remainingFraction"]
		_, hasReset := quota["resetTime"]
		if !hasRem && !hasReset {
			continue
		}
		rem, _ := quota["remainingFraction"].(float64)
		label, _ := cfg["label"].(string)
		if label == "" {
			if mo, ok := cfg["modelOrAlias"].(map[string]any); ok {
				label, _ = mo["model"].(string)
			}
		}
		if label == "" {
			label = "Model"
		}
		reset, _ := quota["resetTime"].(string)
		pct := float64(int(min(1, max(0, 1-rem))*10000)) / 10000
		q.Limits = append(q.Limits, map[string]any{"label": label, "title": label, "percent": pct, "resetsAt": reset})
	}
	sort.SliceStable(q.Limits, func(i, j int) bool { return q.Limits[i]["percent"].(float64) > q.Limits[j]["percent"].(float64) })
	if plan, ok := user["planStatus"].(map[string]any); ok {
		if info, ok := plan["planInfo"].(map[string]any); ok {
			q.TierLabel, _ = info["planName"].(string)
		}
	}
	return q
}

type langServer struct {
	pid   int
	token string
}

func languageServers() []langServer {
	var out []langServer
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err != nil {
			continue
		}
		args := strings.Split(string(raw), "\x00")
		if len(args) == 0 || !strings.Contains(filepath.Base(args[0]), "language_server") {
			continue
		}
		token := ""
		for i, a := range args {
			if v, ok := strings.CutPrefix(a, "--csrf_token="); ok {
				token = v
			} else if a == "--csrf_token" && i+1 < len(args) {
				token = args[i+1]
			}
		}
		if token != "" {
			out = append(out, langServer{pid, token})
		}
	}
	return out
}

var portRe = regexp.MustCompile(`:(\d+)$`)

func listeningPorts(pid int) []int {
	out, err := exec.Command("ss", "-Htlnp").Output()
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var ports []int
	for _, l := range strings.Split(string(out), "\n") {
		if !strings.Contains(l, "pid="+strconv.Itoa(pid)+",") {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		if m := portRe.FindStringSubmatch(f[3]); m != nil {
			p, _ := strconv.Atoi(m[1])
			if !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
	}
	sort.Ints(ports)
	return ports
}

func callRPC(port int, token string) map[string]any {
	body := []byte(`{"metadata":{"ideName":"antigravity","extensionName":"antigravity","locale":"en"}}`)
	// the language server uses a self-signed certificate on 127.0.0.1
	hc := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	for _, scheme := range []string{"https", "http"} {
		req, _ := http.NewRequest(http.MethodPost, scheme+"://127.0.0.1:"+strconv.Itoa(port)+"/exa.language_server_pb.LanguageServerService/GetUserStatus", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set("X-Codeium-Csrf-Token", token)
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		var v map[string]any
		err = json.NewDecoder(resp.Body).Decode(&v)
		resp.Body.Close()
		if err == nil {
			return v
		}
	}
	return nil
}

// FetchQuota asks a running language server, else returns the cached answer.
func FetchQuota(cache string) (Quota, bool, bool) {
	for _, ls := range languageServers() {
		for _, port := range listeningPorts(ls.pid) {
			st := callRPC(port, ls.token)
			if st == nil || st["userStatus"] == nil {
				continue
			}
			q := QuotaFromStatus(st)
			q.FetchedAt = time.Now().UTC().Format(time.RFC3339)
			if b, err := json.Marshal(q); err == nil {
				_ = os.MkdirAll(filepath.Dir(cache), 0o755)
				_ = os.WriteFile(cache+".tmp", b, 0o644)
				_ = os.Rename(cache+".tmp", cache)
			}
			return q, true, true
		}
	}
	var q Quota
	b, err := os.ReadFile(cache)
	if err != nil || json.Unmarshal(b, &q) != nil {
		return q, false, false
	}
	return q, false, true
}

// NotReset is true while a cached window's reset time is still ahead.
func NotReset(resetAt string, now time.Time) bool {
	if resetAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, strings.Replace(resetAt, "Z", "+00:00", 1))
	if err != nil {
		t, err = time.Parse(time.RFC3339, resetAt)
	}
	return err != nil || t.After(now)
}
