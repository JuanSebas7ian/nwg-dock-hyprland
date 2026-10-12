package hwcheck

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// splitLines is Python's str.splitlines for the separators that matter here.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func normMod(name string) string { return strings.ReplaceAll(name, "-", "_") }

// UpstreamVersion: "1:26.2.3-1" -> "26.2.3"; "610.57.04-1" -> "610.57.04".
func UpstreamVersion(v string) string {
	if i := strings.Index(v, ":"); i >= 0 {
		v = v[i+1:]
	}
	if i := strings.LastIndex(v, "-"); i >= 0 {
		return v[:i]
	}
	return v
}

func ParsePacmanQ(text string) map[string]string {
	pkgs := map[string]string{}
	for _, line := range splitLines(text) {
		if p := strings.Fields(line); len(p) == 2 {
			pkgs[p[0]] = p[1]
		}
	}
	return pkgs
}

// kv keeps the first position of a key and its last value (a Python dict).
type kv struct {
	keys []string
	vals map[string]string
}

func newKV() *kv { return &kv{vals: map[string]string{}} }
func (o *kv) set(k, v string) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// cmdlineParams: "key=value" tokens; the last one wins, as in the kernel.
func cmdlineParams(tokens []string) *kv {
	p := newKV()
	for _, t := range tokens {
		if k, v, ok := strings.Cut(t, "="); ok {
			p.set(k, v)
		}
	}
	return p
}

var kcmdRe = regexp.MustCompile(`^\s*KERNEL_CMDLINE\[default\]\s*(\+?=)\s*"([^"]*)"`)

type modOpt struct{ mod, key, val, src string }

// ParseModprobe returns the options (module, key, value) and the blacklisted modules.
func ParseModprobe(text string) ([][3]string, []string) {
	var opts [][3]string
	var black []string
	joined := regexp.MustCompile(`\\\n`).ReplaceAllString(text, " ")
	for _, raw := range splitLines(joined) {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		w := strings.Fields(line)
		if w[0] == "options" && len(w) >= 3 {
			mod := normMod(w[1])
			for _, x := range w[2:] {
				k, v, _ := strings.Cut(x, "=")
				opts = append(opts, [3]string{mod, k, v})
			}
		} else if w[0] == "blacklist" && len(w) == 2 {
			black = append(black, normMod(w[1]))
		}
	}
	return opts, black
}

// DkmsRow is one `dkms status` line.
type DkmsRow struct{ Mod, Ver, Kernel, Status string }

var dkmsRe = regexp.MustCompile(`^([^/,]+)/([^,:]+)(?:,\s*([^,]+),\s*[^:]+)?:\s*(.+)$`)

func ParseDkmsStatus(text string) []DkmsRow {
	var rows []DkmsRow
	for _, line := range splitLines(text) {
		if m := dkmsRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			rows = append(rows, DkmsRow{m[1], m[2], m[3], strings.TrimSpace(m[4])})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Mod != b.Mod {
			return a.Mod < b.Mod
		}
		if a.Ver != b.Ver {
			return a.Ver < b.Ver
		}
		if a.Kernel != b.Kernel {
			return a.Kernel < b.Kernel
		}
		return a.Status < b.Status
	})
	return rows
}

// shlexSplit is a POSIX-mode shlex.split (ok=false on an unclosed quote).
func shlexSplit(s string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
			inWord = true
		case c == '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			i++
			cur.WriteByte(s[i])
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, true
}

func ParseLspciMM(text string) map[string]string {
	names := map[string]string{}
	for _, line := range splitLines(text) {
		f, ok := shlexSplit(line)
		if ok && len(f) >= 4 {
			names[f[0]] = fmt.Sprintf("%s: %s %s", f[1], f[2], f[3])
		}
	}
	return names
}

// "usb 6-11:" (device), "usb usb6-port11:" (root port) and "usb 3-2-port4:" (hub port) -> "6-11"/"3-2.4"
var (
	usbKeyRe = regexp.MustCompile(`^usb (?:usb(\d+)-port(\d+)|(\d+-[\d.]+)-port(\d+)|(\d+-[\d.]+)):`)
	usbErrRe = regexp.MustCompile(`Timeout while waiting for setup device|device descriptor read|unable to enumerate|` +
		`device not accepting address|Cannot enable\. Maybe the USB cable is bad|error -(71|110|62)\b`)
)

// UsbEnumErrors groups USB enumeration errors per device ("6-11").
func UsbEnumErrors(log string) map[string]int {
	counts := map[string]int{}
	for _, line := range splitLines(log) {
		m := usbKeyRe.FindStringSubmatch(line)
		if m == nil || !usbErrRe.MatchString(line) {
			continue
		}
		key := ""
		switch {
		case m[5] != "":
			key = m[5]
		case m[3] != "":
			key = m[3] + "." + m[4]
		default:
			key = m[1] + "-" + m[2]
		}
		counts[key]++
	}
	return counts
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	fwRe = regexp.MustCompile(`(?i)Direct firmware load for (\S+) failed|failed to load firmware[: ]+(\S+)?`)
	// Not a problem: the regulatory db (cfg80211 uses its built-in one) and drivers that try several firmware versions (iwlwifi).
	fwIgnoredRe = regexp.MustCompile(`^regulatory\.db|^iwlwifi-.*\.ucode$`)
)

func FirmwareFailures(log string) []string {
	set := map[string]bool{}
	for _, line := range splitLines(log) {
		m := fwRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" {
			name = strings.TrimSpace(line)
		}
		if !fwIgnoredRe.MatchString(name) {
			set[name] = true
		}
	}
	return sortedKeys(set)
}

func boolNorm(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "y", "1", "true", "on", "yes":
		return "Y"
	case "n", "0", "false", "off", "no":
		return "N"
	}
	return strings.TrimSpace(v)
}

var decRe = regexp.MustCompile(`^[+-]?(0|[1-9][0-9_]*)$`)

// pyInt0 is Python's int(s, 0).
func pyInt0(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	l := strings.ToLower(strings.TrimLeft(s, "+-"))
	if strings.HasPrefix(l, "0x") || strings.HasPrefix(l, "0o") || strings.HasPrefix(l, "0b") {
		v, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 0, 64)
		return v, err == nil
	}
	if !decRe.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	return v, err == nil
}

func sameParamValue(expected, actual string) bool {
	a := strings.TrimSpace(actual)
	if a == "Y" || a == "N" {
		return boolNorm(expected) == a
	}
	e, ok1 := pyInt0(expected)
	x, ok2 := pyInt0(a)
	if ok1 && ok2 {
		return e == x
	}
	return strings.TrimSpace(expected) == a
}

func base(p string) string { return path.Base(p) }

func sortStrings(s []string) { sort.Strings(s) }
