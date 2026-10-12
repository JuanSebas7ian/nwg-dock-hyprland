package hw

// Live sensors, sampled every second: CPU (per thread), memory, temperatures,
// fans and pump (nct6775 Super I/O), DDR5, NVMe, disk and network throughput,
// the Radeon iGPU and the NVIDIA GPU (NVML).
//
// Fan names and roles: ~/.config/omarchy-sysmon/fans.json
//   {"2": "CPU cooler fan 1", "_roles": {"pump": ["7"], "cpu": ["2", "5"]}}

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	hwmonDir = "/sys/class/hwmon"
	cpuDir   = "/sys/devices/system/cpu"
)

var boardTemps = map[string]string{"SYSTIN": "Motherboard", "CPUTIN": "CPU socket"}

func hwmons() map[string][]string {
	out := map[string][]string{}
	for _, d := range ListDir(hwmonDir) {
		base := hwmonDir + "/" + d
		n := Read(base + "/name")
		out[n] = append(out[n], base)
	}
	return out
}

type fanCfg struct {
	names     map[string]string
	pump, cpu map[string]bool
}

func loadFans() fanCfg {
	f := fanCfg{names: map[string]string{}, pump: map[string]bool{}, cpu: map[string]bool{}}
	var raw map[string]any
	if !ReadJSON(filepath.Join(ConfigDir(), "omarchy-sysmon", "fans.json"), &raw) {
		return f
	}
	for k, v := range raw {
		if s, ok := v.(string); ok && !strings.HasPrefix(k, "_") {
			f.names[k] = s
		}
	}
	roles, _ := raw["_roles"].(map[string]any)
	for role, set := range map[string]map[string]bool{"pump": f.pump, "cpu": f.cpu} {
		list, _ := roles[role].([]any)
		for _, x := range list {
			set[fmt.Sprint(x)] = true
		}
	}
	return f
}

type cpuTime struct{ total, idle int64 }

func cpuTimes() []cpuTime {
	var per []cpuTime
	for _, l := range strings.Split(Read("/proc/stat"), "\n") {
		if !strings.HasPrefix(l, "cpu") {
			break
		}
		var t cpuTime
		for i, f := range strings.Fields(l)[1:] {
			v, _ := strconv.ParseInt(f, 10, 64)
			t.total += v
			if i == 3 || i == 4 {
				t.idle += v
			}
		}
		per = append(per, t)
	}
	return per // [total, cpu0, cpu1, ...]
}

func diskBytes() (int64, int64) {
	var r, w int64
	for _, l := range strings.Split(Read("/proc/diskstats"), "\n") {
		p := strings.Fields(l)
		if len(p) < 10 {
			continue
		}
		name := p[2]
		// whole NVMe/SATA devices only, not partitions, dm or loop
		last := name[len(name)-1]
		if (strings.HasPrefix(name, "nvme") && !strings.Contains(name[4:], "p")) || (strings.HasPrefix(name, "sd") && last >= 'a' && last <= 'z') {
			a, _ := strconv.ParseInt(p[5], 10, 64)
			b, _ := strconv.ParseInt(p[9], 10, 64)
			r += a * 512
			w += b * 512
		}
	}
	return r, w
}

func netBytes() (int64, int64) {
	var rx, tx int64
	lines := strings.Split(Read("/proc/net/dev"), "\n")
	if len(lines) < 2 {
		return 0, 0
	}
	for _, l := range lines[2:] {
		iface, data, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(iface) == "lo" {
			continue
		}
		d := strings.Fields(data)
		if len(d) < 9 {
			continue
		}
		a, _ := strconv.ParseInt(d[0], 10, 64)
		b, _ := strconv.ParseInt(d[8], 10, 64)
		rx += a
		tx += b
	}
	return rx, tx
}

func memory() M {
	info := map[string]int64{}
	for _, l := range strings.Split(Read("/proc/meminfo"), "\n") {
		k, v, _ := strings.Cut(l, ":")
		if f := strings.Fields(v); len(f) > 0 {
			if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
				info[k] = n * 1024
			}
		}
	}
	total := info["MemTotal"]
	return M{"total": total, "used": total - info["MemAvailable"], "cached": info["Cached"],
		"swapTotal": info["SwapTotal"], "swapUsed": info["SwapTotal"] - info["SwapFree"]}
}

func igpu() M {
	for _, card := range ListDir("/sys/class/drm") {
		dev := "/sys/class/drm/" + card + "/device"
		if strings.Contains(card, "-") || Read(dev+"/vendor") != "0x1002" {
			continue
		}
		return M{"util": Num(dev+"/gpu_busy_percent", 1), "vramUsed": Num(dev+"/mem_info_vram_used", 1), "vramTotal": Num(dev+"/mem_info_vram_total", 1)}
	}
	return nil
}

func loadavg() []float64 {
	f := strings.Fields(Read("/proc/loadavg"))
	out := []float64{0, 0, 0}
	for i := 0; i < 3 && i < len(f); i++ {
		v, _ := strconv.ParseFloat(f[i], 64)
		out[i] = round2(v)
	}
	return out
}

type sample struct {
	cpu    []cpuTime
	dr, dw int64
	rx, tx int64
	at     time.Time
}

// Sensors keeps the previous sample to turn counters into rates.
type Sensors struct {
	k10, sio, amdgpu string
	spd, nvme        []string
	model            string
	threads          int
	fans             fanCfg
	GPU              *Nvml
	prev             sample
	ticks            int
}

func take() sample {
	s := sample{cpu: cpuTimes(), at: time.Now()}
	s.dr, s.dw = diskBytes()
	s.rx, s.tx = netBytes()
	return s
}

func NewSensors(useNvml bool) *Sensors {
	mons := hwmons()
	first := func(k string) string {
		if v := mons[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	s := &Sensors{k10: first("k10temp"), amdgpu: first("amdgpu"), spd: mons["spd5118"], nvme: mons["nvme"], model: "CPU"}
	names := make([]string, 0, len(mons))
	for k := range mons {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if strings.HasPrefix(k, "nct6") {
			s.sio = mons[k][0]
			break
		}
	}
	for _, l := range strings.Split(Read("/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(l, "model name") {
			_, v, _ := strings.Cut(l, ":")
			s.model = strings.TrimSpace(v)
			break
		}
	}
	s.threads = len(cpuTimes()) - 1
	if s.threads <= 0 {
		s.threads = runtime.NumCPU()
	}
	s.fans = loadFans()
	if useNvml && os.Getenv("HW_NO_NVML") != "1" {
		s.GPU = OpenNvml()
	}
	s.prev = take()
	return s
}

func (s *Sensors) cpu(now []cpuTime) M {
	loads := []float64{}
	for i := 0; i < len(now) && i < len(s.prev.cpu); i++ {
		dt := now[i].total - s.prev.cpu[i].total
		if dt > 0 {
			loads = append(loads, round1(100*(1-float64(now[i].idle-s.prev.cpu[i].idle)/float64(dt))))
		} else {
			loads = append(loads, 0)
		}
	}
	freqs := make([]int64, s.threads)
	var sum, max float64
	valid := 0
	for i := 0; i < s.threads; i++ {
		if f := Num(fmt.Sprintf("%s/cpu%d/cpufreq/scaling_cur_freq", cpuDir, i), 1000); f != nil && *f != 0 {
			freqs[i] = iround(*f)
			sum += *f
			valid++
			max = math.Max(max, *f)
		}
	}
	temps := map[string]*float64{}
	if s.k10 != "" {
		for i := 1; i < 12; i++ {
			if label := Read(fmt.Sprintf("%s/temp%d_label", s.k10, i)); label != "" {
				temps[label] = Num(fmt.Sprintf("%s/temp%d_input", s.k10, i), 1000)
			}
		}
	}
	var ccdKeys []string
	for k := range temps {
		if strings.HasPrefix(k, "Tccd") {
			ccdKeys = append(ccdKeys, k)
		}
	}
	sort.Strings(ccdKeys)
	ccd := []*float64{}
	for _, k := range ccdKeys {
		ccd = append(ccd, temps[k])
	}
	var load any = 0.0
	threads := []float64{}
	if len(loads) > 0 {
		load, threads = loads[0], loads[1:]
	}
	var avg, fmax any
	if valid > 0 {
		avg, fmax = iround(sum/float64(valid)), max
	}
	return M{"model": s.model, "load": load, "threads": threads, "freqs": freqs, "freqAvg": avg, "freqMax": fmax,
		"tctl": temps["Tctl"], "ccd": ccd, "loadavg": loadavg(),
		"governor": Read(cpuDir + "/cpu0/cpufreq/scaling_governor"), "epp": Read(cpuDir + "/cpu0/cpufreq/energy_performance_preference")}
}

func (s *Sensors) cooling() ([]M, []M) {
	fans, board := []M{}, []M{}
	if s.sio == "" {
		return fans, board
	}
	for i := 1; i < 8; i++ {
		rpm := Num(fmt.Sprintf("%s/fan%d_input", s.sio, i), 1)
		if rpm == nil {
			continue
		}
		key := strconv.Itoa(i)
		role := "case"
		if s.fans.pump[key] {
			role = "pump"
		} else if s.fans.cpu[key] {
			role = "cpu"
		}
		if *rpm > 0 || role != "case" {
			name := s.fans.names[key]
			if name == "" {
				name = "Fan " + key
			}
			fans = append(fans, M{"id": key, "name": name, "rpm": int64(*rpm), "role": role})
		}
	}
	for i := 1; i < 14; i++ {
		if name, ok := boardTemps[Read(fmt.Sprintf("%s/temp%d_label", s.sio, i))]; ok {
			if t := Num(fmt.Sprintf("%s/temp%d_input", s.sio, i), 1000); t != nil && *t != 0 {
				board = append(board, M{"name": name, "temp": round1(*t)})
			}
		}
	}
	return fans, board
}

// Sample is one snapshot; gpu=false skips NVML (panel closed, not the 5 s tick).
func (s *Sensors) Sample(gpu, gpuProcs bool) M {
	s.ticks++
	if s.ticks%10 == 0 {
		s.fans = loadFans() // pick up renames without a restart
	}
	now := take()
	dt := math.Max(0.001, now.at.Sub(s.prev.at).Seconds())
	fans, board := s.cooling()
	nvme := []M{}
	for _, base := range s.nvme {
		t := Num(base+"/temp1_input", 1000)
		model := Read(base + "/device/model")
		if model == "" {
			model = "NVMe"
		}
		if t != nil {
			name := strings.Replace(strings.Replace(model, "WD_BLACK ", "", 1), "Samsung SSD ", "", 1)
			if len(name) > 18 {
				name = name[:18]
			}
			nvme = append(nvme, M{"name": name, "temp": round1(*t)})
		}
	}
	ram := []float64{}
	for _, d := range s.spd {
		v := 0.0
		if t := Num(d+"/temp1_input", 1000); t != nil {
			v = *t
		}
		ram = append(ram, round1(v))
	}
	var ig any
	if g := igpu(); s.amdgpu != "" {
		if g == nil {
			g = M{}
		}
		g["temp"] = Num(s.amdgpu+"/temp1_input", 1000)
		ig = g
	} else if g != nil {
		ig = g
	}
	snap := M{
		"t":     float64(now.at.UnixNano()) / 1e9,
		"cpu":   s.cpu(now.cpu),
		"fans":  fans,
		"board": board,
		"ram":   ram,
		"mem":   memory(),
		"nvme":  nvme,
		"disk":  M{"read": float64(now.dr-s.prev.dr) / dt, "write": float64(now.dw-s.prev.dw) / dt},
		"net":   M{"rx": float64(now.rx-s.prev.rx) / dt, "tx": float64(now.tx-s.prev.tx) / dt},
		"igpu":  ig,
	}
	if gpu && s.GPU != nil {
		snap["gpus"] = s.GPU.Snapshot(gpuProcs)
	}
	s.prev = now
	return snap
}
