// Package cuda checks CUDA through libcuda (dlopen), without linking it.
// Call it from a short-lived process: cuInit leaves a driver context behind.
package cuda

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

static void *cu;
static int cu_init(void) {
	cu = dlopen("libcuda.so.1", RTLD_NOW);
	if (!cu) return -1000;
	int (*f)(unsigned) = dlsym(cu, "cuInit");
	return f ? f(0) : -1001;
}
static int cu_i(const char *n, int *o) { int (*f)(int*) = dlsym(cu, n); return f ? f(o) : -1; }
static int cu_dev(int *d, int i) { int (*f)(int*, int) = dlsym(cu, "cuDeviceGet"); return f ? f(d, i) : -1; }
static int cu_name(char *b, int n, int d) { int (*f)(char*, int, int) = dlsym(cu, "cuDeviceGetName"); return f ? f(b, n, d) : -1; }
static int cu_attr(int *o, int a, int d) { int (*f)(int*, int, int) = dlsym(cu, "cuDeviceGetAttribute"); return f ? f(o, a, d) : -1; }
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Missing is what Init returns when libcuda.so.1 cannot be loaded.
const Missing = -1

// Init is cuInit(0): the CUresult (0 = OK), or Missing.
func Init() int {
	rc := int(C.cu_init())
	if rc == -1000 || rc == -1001 {
		return Missing
	}
	return rc
}

// Probe reports the driver's CUDA version and the devices.
func Probe() map[string]any {
	info := map[string]any{"works": false}
	rc := Init()
	if rc == Missing {
		info["error"] = "libcuda not available (libcuda.so.1: cannot open shared object file)"
		return info
	}
	if rc != 0 {
		info["error"] = fmt.Sprintf("cuInit failed (%d)", rc)
		return info
	}
	var v, n C.int
	cv, cc := C.CString("cuDriverGetVersion"), C.CString("cuDeviceGetCount")
	C.cu_i(cv, &v)
	C.cu_i(cc, &n)
	C.free(unsafe.Pointer(cv))
	C.free(unsafe.Pointer(cc))
	info["driverCuda"] = fmt.Sprintf("%d.%d", int(v)/1000, (int(v)%1000)/10)
	devices := []map[string]any{}
	for i := 0; i < int(n); i++ {
		var d, ma, mi C.int
		C.cu_dev(&d, C.int(i))
		var buf [100]C.char
		C.cu_name(&buf[0], 100, d)
		C.cu_attr(&ma, 75, d)
		C.cu_attr(&mi, 76, d)
		devices = append(devices, map[string]any{"name": C.GoString(&buf[0]), "cc": fmt.Sprintf("%d.%d", int(ma), int(mi))})
	}
	info["devices"] = devices
	info["works"] = n > 0
	return info
}
