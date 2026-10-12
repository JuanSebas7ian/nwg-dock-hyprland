//! VRAM through libnvidia-ml (dlopen), without spawning nvidia-smi every 2 s.

use std::collections::BTreeMap;
use std::ffi::{c_char, c_int, c_uint, c_ulonglong, c_void, CString};

#[derive(Debug, Clone, Default)]
pub struct GpuProc {
    pub pid: i32,
    pub mem: i64,
    pub typ: String, // "C" compute, "G" graphics, or both
    pub sm: u32,     // % of the GPU's compute used
}

#[derive(Debug, Clone, Default)]
pub struct Vram {
    pub total: i64,
    pub free: i64,
    pub used: i64,
    pub procs: Vec<GpuProc>,
}

#[repr(C)]
struct Mem {
    total: c_ulonglong,
    free: c_ulonglong,
    used: c_ulonglong,
}

#[repr(C)]
#[derive(Clone, Copy)]
struct Proc {
    pid: c_uint,
    used: c_ulonglong,
    gi: c_uint,
    ci: c_uint,
}

#[repr(C)]
#[derive(Clone, Copy)]
struct PUtil {
    pid: c_uint,
    ts: c_ulonglong,
    sm: c_uint,
    mem: c_uint,
    enc: c_uint,
    dec: c_uint,
}

type FnHandle = unsafe extern "C" fn(c_uint, *mut *mut c_void) -> c_int;
type FnMem = unsafe extern "C" fn(*mut c_void, *mut Mem) -> c_int;
type FnProcs = unsafe extern "C" fn(*mut c_void, *mut c_uint, *mut Proc) -> c_int;
type FnUtil = unsafe extern "C" fn(*mut c_void, *mut PUtil, *mut c_uint, c_ulonglong) -> c_int;

pub struct Nvml {
    dev: *mut c_void,
    mem: FnMem,
    compute: Option<FnProcs>,
    graphics: Option<FnProcs>,
    util: Option<FnUtil>,
    last_ts: c_ulonglong,
}

unsafe fn sym(lib: *mut c_void, name: &str) -> *mut c_void {
    let c = CString::new(name).unwrap();
    libc::dlsym(lib, c.as_ptr() as *const c_char)
}

impl Nvml {
    pub fn open() -> Option<Nvml> {
        unsafe {
            let name = CString::new("libnvidia-ml.so.1").unwrap();
            let lib = libc::dlopen(name.as_ptr(), libc::RTLD_NOW);
            if lib.is_null() {
                return None;
            }
            let init = sym(lib, "nvmlInit_v2");
            let handle = sym(lib, "nvmlDeviceGetHandleByIndex_v2");
            let mem = sym(lib, "nvmlDeviceGetMemoryInfo");
            if init.is_null() || handle.is_null() || mem.is_null() {
                return None;
            }
            let init: unsafe extern "C" fn() -> c_int = std::mem::transmute(init);
            if init() != 0 {
                return None;
            }
            let handle: FnHandle = std::mem::transmute(handle);
            let mut dev = std::ptr::null_mut();
            if handle(0, &mut dev) != 0 {
                return None;
            }
            let opt = |n: &str| {
                let p = sym(lib, n);
                (!p.is_null()).then(|| p)
            };
            Some(Nvml {
                dev,
                mem: std::mem::transmute::<*mut c_void, FnMem>(mem),
                compute: opt("nvmlDeviceGetComputeRunningProcesses_v3").map(|p| std::mem::transmute::<*mut c_void, FnProcs>(p)),
                graphics: opt("nvmlDeviceGetGraphicsRunningProcesses_v3").map(|p| std::mem::transmute::<*mut c_void, FnProcs>(p)),
                util: opt("nvmlDeviceGetProcessUtilization").map(|p| std::mem::transmute::<*mut c_void, FnUtil>(p)),
                last_ts: 0,
            })
        }
    }

    pub fn sample(&mut self) -> Vram {
        let mut m = Mem { total: 0, free: 0, used: 0 };
        unsafe { (self.mem)(self.dev, &mut m) };
        let mut procs: BTreeMap<i32, GpuProc> = BTreeMap::new();
        for (f, kind) in [(self.compute, "C"), (self.graphics, "G")] {
            let Some(f) = f else { continue };
            let mut n: c_uint = 64;
            let mut arr = [Proc { pid: 0, used: 0, gi: 0, ci: 0 }; 64];
            if unsafe { f(self.dev, &mut n, arr.as_mut_ptr()) } == 0 {
                for p in &arr[..(n as usize).min(64)] {
                    let e = procs.entry(p.pid as i32).or_insert(GpuProc { pid: p.pid as i32, ..Default::default() });
                    let used = if p.used < (1u64 << 62) { p.used as i64 } else { 0 };
                    e.mem = e.mem.max(used);
                    e.typ.push_str(kind);
                }
            }
        }
        if let Some(f) = self.util {
            let mut n: c_uint = 64;
            let mut arr = [PUtil { pid: 0, ts: 0, sm: 0, mem: 0, enc: 0, dec: 0 }; 64];
            if unsafe { f(self.dev, arr.as_mut_ptr(), &mut n, self.last_ts) } == 0 {
                for u in &arr[..(n as usize).min(64)] {
                    if let Some(p) = procs.get_mut(&(u.pid as i32)) {
                        p.sm = p.sm.max(u.sm);
                    }
                    self.last_ts = self.last_ts.max(u.ts);
                }
            }
        }
        Vram { total: m.total as i64, free: m.free as i64, used: m.used as i64, procs: procs.into_values().collect() }
    }
}
