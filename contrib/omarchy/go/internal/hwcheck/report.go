package hwcheck

import (
	"fmt"
	"strings"
)

const Version = "1.0.0"

var levelTag = map[string]string{OK: "[ OK ]", INFO: "[INFO]", WARN: "[WARN]", FAIL: "[FAIL]"}
var categories = [][2]string{{"hw", "Hardware y firmware"}, {"updates", "Actualizaciones"}, {"conflicts", "Conflictos"}}

// State is the normalized state that baseline/diff compare (map keys marshal sorted).
type State struct {
	Bluetooth []string                     `json:"bluetooth"`
	Cmdline   []string                     `json:"cmdline"`
	Dkms      []string                     `json:"dkms"`
	Kernel    string                       `json:"kernel"`
	Net       map[string]string            `json:"net"`
	Packages  map[string]string            `json:"packages"`
	PCI       map[string]map[string]string `json:"pci"`
	USB       []string                     `json:"usb"`
}

func CollectState(f *Facts) State {
	st := State{Bluetooth: uniqSorted(f.BTAdapters), Cmdline: uniqSortedKeepDup(f.Cmdline), Kernel: f.Release,
		Net: map[string]string{}, Packages: map[string]string{}, PCI: map[string]map[string]string{}}
	for k, v := range f.Net {
		st.Net[k] = v
	}
	for p, v := range f.Packages {
		if DriverPackageRe.MatchString(p) {
			st.Packages[p] = v
		}
	}
	for slot, d := range f.PCI {
		st.PCI[slot] = map[string]string{"id": d.ID, "driver": d.Driver}
	}
	var usb, dk []string
	for _, d := range f.USB {
		usb = append(usb, d.ID)
	}
	for _, r := range f.Dkms {
		dk = append(dk, fmt.Sprintf("%s/%s %s: %s", r.Mod, r.Ver, r.Kernel, r.Status))
	}
	st.USB, st.Dkms = uniqSorted(usb), uniqSorted(dk)
	if st.Bluetooth == nil {
		st.Bluetooth = []string{}
	}
	if st.USB == nil {
		st.USB = []string{}
	}
	if st.Dkms == nil {
		st.Dkms = []string{}
	}
	return st
}

// uniqSortedKeepDup is sorted() of a list (duplicates kept, like Python's sorted).
func uniqSortedKeepDup(in []string) []string {
	out := append([]string{}, in...)
	sortStrings(out)
	return out
}

// Change is one difference against the baseline.
type Change struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func setOf(l []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range l {
		m[x] = true
	}
	return m
}

func minus(a, b []string) []string {
	bs := setOf(b)
	r := map[string]bool{}
	for _, x := range a {
		if !bs[x] {
			r[x] = true
		}
	}
	return sortedKeys(r)
}

// DiffStates: FAIL for regressions, INFO for expected changes.
func DiffStates(old, nw State) []Change {
	var out []Change
	add := func(l, t string) { out = append(out, Change{l, t}) }
	slots := map[string]bool{}
	for k := range old.PCI {
		slots[k] = true
	}
	for k := range nw.PCI {
		slots[k] = true
	}
	for _, slot := range sortedKeys(slots) {
		a, aok := old.PCI[slot]
		b, bok := nw.PCI[slot]
		switch {
		case aok && !bok:
			add(FAIL, fmt.Sprintf("PCI %s [%s] desapareció (tenía driver %s)", slot, a["id"], dash(a["driver"])))
		case bok && !aok:
			add(INFO, fmt.Sprintf("PCI %s [%s] nuevo, driver %s", slot, b["id"], dash(b["driver"])))
		case a["driver"] != b["driver"]:
			l := WARN
			if a["driver"] != "" && b["driver"] == "" {
				l = FAIL
			}
			add(l, fmt.Sprintf("PCI %s [%s]: driver %s → %s", slot, b["id"], dash(a["driver"]), dash(b["driver"])))
		}
	}
	ifaces := map[string]bool{}
	for k := range old.Net {
		ifaces[k] = true
	}
	for k := range nw.Net {
		ifaces[k] = true
	}
	for _, i := range sortedKeys(ifaces) {
		a, aok := old.Net[i]
		b, bok := nw.Net[i]
		switch {
		case aok && !bok:
			add(FAIL, fmt.Sprintf("interfaz de red %s (%s) desapareció", i, a))
		case bok && !aok:
			add(INFO, fmt.Sprintf("interfaz de red %s (%s) nueva", i, b))
		case a != b:
			add(WARN, fmt.Sprintf("interfaz de red %s: driver %s → %s", i, a, b))
		}
	}
	for _, h := range minus(old.Bluetooth, nw.Bluetooth) {
		add(FAIL, "adaptador Bluetooth "+h+" desapareció")
	}
	for _, h := range minus(nw.Bluetooth, old.Bluetooth) {
		add(INFO, "adaptador Bluetooth "+h+" nuevo")
	}
	for _, d := range minus(old.USB, nw.USB) {
		add(INFO, "USB "+d+" ya no está conectado")
	}
	for _, d := range minus(nw.USB, old.USB) {
		add(INFO, "USB "+d+" nuevo")
	}
	if old.Kernel != nw.Kernel {
		add(INFO, fmt.Sprintf("kernel %s → %s", old.Kernel, nw.Kernel))
	}
	for _, t := range minus(old.Cmdline, nw.Cmdline) {
		add(INFO, "línea de arranque: quitado "+t)
	}
	for _, t := range minus(nw.Cmdline, old.Cmdline) {
		add(INFO, "línea de arranque: agregado "+t)
	}
	pk := map[string]bool{}
	for k := range old.Packages {
		pk[k] = true
	}
	for k := range nw.Packages {
		pk[k] = true
	}
	for _, p := range sortedKeys(pk) {
		a, b := old.Packages[p], nw.Packages[p]
		if a != b {
			l := INFO
			if a != "" && b == "" {
				l = WARN
			}
			add(l, fmt.Sprintf("paquete %s: %s → %s", p, dash(a), dash(b)))
		}
	}
	for _, d := range minus(old.Dkms, nw.Dkms) {
		add(INFO, "dkms quitado: "+d)
	}
	for _, d := range minus(nw.Dkms, old.Dkms) {
		add(INFO, "dkms nuevo: "+d)
	}
	return out
}

func RenderScan(fs []*Finding, release string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "omarchy-hwcheck %s · kernel %s\n", Version, release)
	for _, c := range categories {
		var items []*Finding
		for _, x := range fs {
			if x.Category == c[0] {
				items = append(items, x)
			}
		}
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n== %s\n", c[1])
		for _, x := range items {
			fmt.Fprintf(&b, "%s %s %s\n", levelTag[x.Level], x.ID, x.Title)
			if x.Level != OK || len(x.Details) <= 4 {
				for _, d := range x.Details {
					fmt.Fprintf(&b, "       %s\n", d)
				}
			}
			if x.Hint != "" && (x.Level == WARN || x.Level == FAIL || x.Level == INFO) {
				fmt.Fprintf(&b, "       → %s\n", x.Hint)
			}
		}
	}
	counts := map[string]int{}
	for _, x := range fs {
		counts[x.Level]++
	}
	fmt.Fprintf(&b, "\nResultado: %d bien, %d informativos, %d avisos, %d fallos\n", counts[OK], counts[INFO], counts[WARN], counts[FAIL])
	return b.String()
}

func RenderDiff(changes []Change, baseline string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "omarchy-hwcheck %s · comparación con %s\n", Version, baseline)
	if len(changes) == 0 {
		b.WriteString("Sin cambios de hardware, drivers ni paquetes de drivers.\n")
	}
	regress := 0
	for _, c := range changes {
		fmt.Fprintf(&b, "%s %s\n", levelTag[c.Level], c.Text)
		if c.Level == FAIL {
			regress++
		}
	}
	fmt.Fprintf(&b, "\nResultado: %d cambio(s), %d regresión(es)\n", len(changes), regress)
	return b.String()
}
