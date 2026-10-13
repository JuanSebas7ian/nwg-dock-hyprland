package iphone

// App sizes come from the device's installation_proxy service through libimobiledevice (cgo): the
// command-line tool that would print them (ideviceinstaller) is unmaintained and not in the repos.

/*
#cgo pkg-config: libimobiledevice-1.0 libplist-2.0
#include <stdlib.h>
#include <string.h>
#include <libimobiledevice/libimobiledevice.h>
#include <libimobiledevice/installation_proxy.h>
#include <plist/plist.h>

// browse_user_apps returns the user apps with their sizes as an XML plist (NULL + *err on failure).
static char *browse_user_apps(const char *udid, int *err) {
	idevice_t dev = NULL;
	instproxy_client_t ip = NULL;
	plist_t res = NULL;
	char *xml = NULL;
	uint32_t len = 0;
	*err = 0;
	if (idevice_new_with_options(&dev, udid, IDEVICE_LOOKUP_USBMUX | IDEVICE_LOOKUP_NETWORK) != IDEVICE_E_SUCCESS) {
		*err = 1;
		return NULL;
	}
	if (instproxy_client_start_service(dev, &ip, "omarchy-iphone") != INSTPROXY_E_SUCCESS) {
		*err = 2;
		idevice_free(dev);
		return NULL;
	}
	plist_t opts = plist_new_dict();
	plist_dict_set_item(opts, "ApplicationType", plist_new_string("User"));
	plist_t attrs = plist_new_array();
	const char *names[] = {"CFBundleIdentifier", "CFBundleDisplayName", "CFBundleName", "CFBundleShortVersionString",
		"StaticDiskUsage", "DynamicDiskUsage", NULL};
	for (int i = 0; names[i]; i++) plist_array_append_item(attrs, plist_new_string(names[i]));
	plist_dict_set_item(opts, "ReturnAttributes", attrs);
	if (instproxy_browse(ip, opts, &res) != INSTPROXY_E_SUCCESS || !res) *err = 3;
	else plist_to_xml(res, &xml, &len);
	plist_free(opts);
	if (res) plist_free(res);
	instproxy_client_free(ip);
	idevice_free(dev);
	return xml;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// browseApps asks the device for its user apps (XML plist). Tests replace it.
var browseApps = func(udid string) ([]byte, error) {
	cu := C.CString(udid)
	defer C.free(unsafe.Pointer(cu))
	var e C.int
	x := C.browse_user_apps(cu, &e)
	if x == nil {
		switch e {
		case 1:
			return nil, errors.New("device not found")
		case 2:
			return nil, errors.New("could not open the app list service (unlock the device and trust this PC)")
		}
		return nil, errors.New("the device did not return its apps")
	}
	defer C.plist_mem_free(unsafe.Pointer(x))
	return C.GoBytes(unsafe.Pointer(x), C.int(C.strlen(x))), nil
}
