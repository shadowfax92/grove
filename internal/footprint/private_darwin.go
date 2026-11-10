//go:build darwin && cgo

package footprint

/*
#include <sys/attr.h>
#include <unistd.h>
#include <errno.h>
#include <stdint.h>
#include <string.h>
#include <stdlib.h>

// Darwin packs attribute results on four-byte boundaries, including off_t.
// Copy rather than casting an unaligned pointer, and check the returned mask:
// unsupported filesystems must not silently report a plausible zero.
static int grove_private_size(int fd, const char *name, int64_t *size) {
 struct attrlist attrs = {0};
 attrs.bitmapcount = ATTR_BIT_MAP_COUNT;
 attrs.commonattr = ATTR_CMN_RETURNED_ATTRS;
 attrs.forkattr = ATTR_CMNEXT_PRIVATESIZE;
 unsigned char buffer[32] = {0};
 if (getattrlistat(fd, name, &attrs, buffer, sizeof(buffer), FSOPT_NOFOLLOW | FSOPT_ATTR_CMN_EXTENDED) != 0) return errno;
 uint32_t length; attribute_set_t returned;
 memcpy(&length, buffer, sizeof(length));
 memcpy(&returned, buffer + 4, sizeof(returned));
 if (length < 32 || !(returned.forkattr & ATTR_CMNEXT_PRIVATESIZE)) return ENOTSUP;
 memcpy(size, buffer + 24, sizeof(*size));
 return 0;
}
*/
import "C"

import (
	"syscall"
	"unsafe"
)

func PrivateSizeSupported() bool { return true }
func privateSizeAt(fd int, name string) (int64, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var size C.int64_t
	if code := C.grove_private_size(C.int(fd), cName, &size); code != 0 {
		return 0, syscall.Errno(code)
	}
	return int64(size), nil
}
