package hw

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef struct { unsigned gpu, memory; } nv_util;
typedef struct { unsigned long long total, free, used; } nv_mem;
typedef struct { unsigned pid; unsigned long long used; unsigned gi, ci; } nv_proc;
typedef struct { unsigned pid; unsigned long long ts; unsigned sm, mem, enc, dec; } nv_putil;

static void *nv;

static int nv_open(void) {
	nv = dlopen("libnvidia-ml.so.1", RTLD_NOW);
	if (!nv) return -1;
	int (*init)(void) = dlsym(nv, "nvmlInit_v2");
	return init ? init() : -1;
}
static int nv_has(const char *n) { return nv && dlsym(nv, n) != NULL; }
static int nv_count(unsigned *n) { int (*f)(unsigned*) = dlsym(nv, "nvmlDeviceGetCount_v2"); return f ? f(n) : -1; }
static int nv_handle(unsigned i, void **h) { int (*f)(unsigned, void**) = dlsym(nv, "nvmlDeviceGetHandleByIndex_v2"); return f ? f(i, h) : -1; }
static int nv_u(const char *n, void *h, unsigned *o) { int (*f)(void*, unsigned*) = dlsym(nv, n); return f ? f(h, o) : -1; }
static int nv_iu(const char *n, void *h, int a, unsigned *o) { int (*f)(void*, int, unsigned*) = dlsym(nv, n); return f ? f(h, a, o) : -1; }
static int nv_uu(const char *n, void *h, unsigned *a, unsigned *b) { int (*f)(void*, unsigned*, unsigned*) = dlsym(nv, n); return f ? f(h, a, b) : -1; }
static int nv_util_rates(void *h, nv_util *o) { int (*f)(void*, nv_util*) = dlsym(nv, "nvmlDeviceGetUtilizationRates"); return f ? f(h, o) : -1; }
static int nv_meminfo(void *h, nv_mem *o) { int (*f)(void*, nv_mem*) = dlsym(nv, "nvmlDeviceGetMemoryInfo"); return f ? f(h, o) : -1; }
static int nv_name(void *h, char *b, unsigned n) { int (*f)(void*, char*, unsigned) = dlsym(nv, "nvmlDeviceGetName"); return f ? f(h, b, n) : -1; }
static int nv_procs(const char *n, void *h, unsigned *c, nv_proc *a) { int (*f)(void*, unsigned*, nv_proc*) = dlsym(nv, n); return f ? f(h, c, a) : -1; }
static int nv_putils(void *h, nv_putil *a, unsigned *c, unsigned long long since) {
	int (*f)(void*, nv_putil*, unsigned*, unsigned long long) = dlsym(nv, "nvmlDeviceGetProcessUtilization");
	return f ? f(h, a, c, since) : -1;
}

*/
import "C"

import (
	"fmt"

	cudalib "omarchy/internal/cuda"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"
)

// Nvml reads the NVIDIA GPUs through libnvidia-ml without spawning nvidia-smi.
type Nvml struct {
	handles []unsafe.Pointer
	last    map[int]uint64 // per GPU: newest process-utilization sample seen
}

func OpenNvml() *Nvml {
	if C.nv_open() != 0 {
		return nil
	}
	var n C.uint
	C.nv_count(&n)
	g := &Nvml{last: map[int]uint64{}}
	for i := C.uint(0); i < n; i++ {
		var h unsafe.Pointer
		if C.nv_handle(i, &h) == 0 {
			g.handles = append(g.handles, h)
		}
	}
	return g
}

func cs(s string) *C.char { return C.CString(s) }

func (g *Nvml) u(fn string, h unsafe.Pointer) any {
	n := cs(fn)
	defer C.free(unsafe.Pointer(n))
	var v C.uint
	if C.nv_u(n, h, &v) != 0 {
		return nil
	}
	return uint32(v)
}

func (g *Nvml) iu(fn string, h unsafe.Pointer, a int) any {
	n := cs(fn)
	defer C.free(unsafe.Pointer(n))
	var v C.uint
	if C.nv_iu(n, h, C.int(a), &v) != 0 {
		return nil
	}
	return uint32(v)
}

func (g *Nvml) processes(idx int, h unsafe.Pointer) []M {
	type pr struct {
		pid      uint32
		mem      uint64
		typ      string
		sm       *uint32
		enc, dec uint32
	}
	procs := map[uint32]*pr{}
	var order []uint32
	for _, fn := range []string{"nvmlDeviceGetComputeRunningProcesses_v3", "nvmlDeviceGetGraphicsRunningProcesses_v3"} {
		n := cs(fn)
		if C.nv_has(n) == 0 {
			C.free(unsafe.Pointer(n))
			continue
		}
		var arr [64]C.nv_proc
		cnt := C.uint(64)
		rc := C.nv_procs(n, h, &cnt, &arr[0])
		C.free(unsafe.Pointer(n))
		if rc != 0 {
			continue
		}
		for _, p := range arr[:cnt] {
			mem := uint64(p.used)
			if mem >= 1<<62 {
				mem = 0
			}
			e, ok := procs[uint32(p.pid)]
			if !ok {
				e = &pr{pid: uint32(p.pid)}
				procs[e.pid] = e
				order = append(order, e.pid)
			}
			if mem > e.mem {
				e.mem = mem
			}
			if strings.Contains(fn, "Compute") {
				e.typ += "C"
			} else {
				e.typ += "G"
			}
		}
	}
	// Per-process SM / encoder / decoder share since the previous call.
	var ua [64]C.nv_putil
	cnt := C.uint(64)
	if C.nv_putils(h, &ua[0], &cnt, C.ulonglong(g.last[idx])) == 0 {
		for _, u := range ua[:cnt] {
			if e, ok := procs[uint32(u.pid)]; ok {
				sm := uint32(u.sm)
				e.sm, e.enc, e.dec = &sm, uint32(u.enc), uint32(u.dec)
			}
			if uint64(u.ts) > g.last[idx] {
				g.last[idx] = uint64(u.ts)
			}
		}
	}
	out := []M{}
	list := make([]*pr, 0, len(order))
	for _, pid := range order {
		list = append(list, procs[pid])
	}
	smOf := func(p *pr) uint32 {
		if p.sm == nil {
			return 0
		}
		return *p.sm
	}
	sort.SliceStable(list, func(i, j int) bool {
		if smOf(list[i]) != smOf(list[j]) {
			return smOf(list[i]) > smOf(list[j])
		}
		return list[i].mem > list[j].mem
	})
	for _, p := range list {
		name := Read(fmt.Sprintf("/proc/%d/comm", p.pid))
		if name == "" {
			name = fmt.Sprint(p.pid)
		}
		m := M{"pid": p.pid, "mem": p.mem, "type": p.typ, "name": filepath.Base(name)}
		if p.sm != nil {
			m["sm"], m["enc"], m["dec"] = *p.sm, p.enc, p.dec
		}
		out = append(out, m)
	}
	return out
}

func milli(v any) any {
	if x, ok := v.(uint32); ok {
		return float64(x) / 1000
	}
	return nil
}

// Snapshot of every GPU (processes only when asked: they cost more).
func (g *Nvml) Snapshot(withProcs bool) []M {
	gpus := []M{}
	for i, h := range g.handles {
		var ut C.nv_util
		var mem C.nv_mem
		C.nv_util_rates(h, &ut)
		C.nv_meminfo(h, &mem)
		var enc, dec, period C.uint
		ne, nd := cs("nvmlDeviceGetEncoderUtilization"), cs("nvmlDeviceGetDecoderUtilization")
		C.nv_uu(ne, h, &enc, &period)
		C.nv_uu(nd, h, &dec, &period)
		C.free(unsafe.Pointer(ne))
		C.free(unsafe.Pointer(nd))
		var buf [96]C.char
		C.nv_name(h, &buf[0], 96)
		gpu := M{
			"index": i, "name": strings.Replace(C.GoString(&buf[0]), "NVIDIA GeForce ", "", 1),
			"util": uint32(ut.gpu), "memUtil": uint32(ut.memory), "vramUsed": uint64(mem.used), "vramTotal": uint64(mem.total),
			"temp":       g.iu("nvmlDeviceGetTemperature", h, 0),
			"power":      milli(g.u("nvmlDeviceGetPowerUsage", h)),
			"powerLimit": milli(g.u("nvmlDeviceGetEnforcedPowerLimit", h)),
			"fan":        g.u("nvmlDeviceGetFanSpeed", h),
			"clockCore":  g.iu("nvmlDeviceGetClockInfo", h, 0),
			"clockMem":   g.iu("nvmlDeviceGetClockInfo", h, 2),
			"pcieGen":    g.u("nvmlDeviceGetCurrPcieLinkGeneration", h),
			"pcieGenMax": g.u("nvmlDeviceGetMaxPcieLinkGeneration", h),
			"pcieWidth":  g.u("nvmlDeviceGetCurrPcieLinkWidth", h),
			"pcieRx":     g.iu("nvmlDeviceGetPcieThroughput", h, 1), // KB/s, 20 ms window
			"pcieTx":     g.iu("nvmlDeviceGetPcieThroughput", h, 0),
			"enc":        uint32(enc), "dec": uint32(dec),
			"pstate": g.u("nvmlDeviceGetPerformanceState", h),
		}
		if withProcs {
			gpu["processes"] = g.processes(i, h)
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

// CudaProbe checks CUDA through libcuda. Run it in a short-lived process
// (omarchy-hardwared cuda-probe) so the daemon never keeps a CUDA context.
func CudaProbe() M { return cudalib.Probe() }
