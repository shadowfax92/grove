//go:build darwin && cgo

package footprint

/*
#include <sys/attr.h>
#include <unistd.h>
#include <errno.h>

static int grove_read_attributes(int fd, void *buffer, size_t size, int private_size) {
 struct attrlist attrs = {0};
 attrs.bitmapcount = ATTR_BIT_MAP_COUNT;
 attrs.commonattr = ATTR_CMN_NAME | ATTR_CMN_DEVID | ATTR_CMN_OBJTYPE |
   ATTR_CMN_MODTIME | ATTR_CMN_FILEID | ATTR_CMN_ERROR | ATTR_CMN_RETURNED_ATTRS;
 attrs.dirattr = ATTR_DIR_ALLOCSIZE;
 attrs.fileattr = ATTR_FILE_LINKCOUNT | ATTR_FILE_ALLOCSIZE;
 if (private_size) attrs.forkattr = ATTR_CMNEXT_PRIVATESIZE;
 int n = getattrlistbulk(fd, &attrs, buffer, size, private_size ? FSOPT_ATTR_CMN_EXTENDED : 0);
 return n < 0 ? -errno : n;
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS supplies metadata for a whole directory batch in one syscall. Reading
// the returned attribute masks (rather than assuming all requested fields exist)
// handles mixed file types and filesystems without fabricating zero sizes.
func readEntries(ctx context.Context, directory *os.File, private bool, visit entryVisitor) error {
	buffer := make([]byte, 64*1024)
	flag := C.int(0)
	if private {
		flag = 1
	}
	fd := int(directory.Fd())
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := int(C.grove_read_attributes(C.int(fd), unsafe.Pointer(&buffer[0]), C.size_t(len(buffer)), flag))
		if n < 0 {
			err := unix.Errno(-n)
			if first && (err == unix.ENOTSUP || err == unix.EINVAL || err == unix.ENOSYS) {
				if _, seekErr := unix.Seek(fd, 0, 0); seekErr != nil {
					return seekErr
				}
				return readEntriesPortable(ctx, directory, visit)
			}
			return err
		}
		if n == 0 {
			return nil
		}
		first = false
		offset := 0
		for i := 0; i < n; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if offset+4 > len(buffer) {
				return fmt.Errorf("truncated directory attributes")
			}
			length := int(binary.LittleEndian.Uint32(buffer[offset:]))
			if length < 24 || length > len(buffer)-offset {
				return fmt.Errorf("invalid directory attribute length")
			}
			record := buffer[offset : offset+length]
			offset += length
			name, st, privateBytes, err := parseAttributes(record)
			if err != nil {
				return err
			}
			if st == nil {
				var fallback unix.Stat_t
				if err := unix.Fstatat(fd, name, &fallback, unix.AT_SYMLINK_NOFOLLOW); err == nil {
					st = &fallback
				}
			}
			visit(name, st, privateBytes)
		}
	}
}

// Darwin packs attributes on four-byte boundaries even for 64-bit values.
// Decode bytewise in ABI order, using returned masks to skip absent fields;
// a Go struct cast would add padding and misread both sizes and timestamps.
func parseAttributes(record []byte) (name string, st *unix.Stat_t, private *int64, err error) {
	common := binary.LittleEndian.Uint32(record[4:])
	dirs := binary.LittleEndian.Uint32(record[12:])
	files := binary.LittleEndian.Uint32(record[16:])
	extended := binary.LittleEndian.Uint32(record[20:])
	offset := 24
	take := func(n int) []byte {
		if offset+n > len(record) {
			err = fmt.Errorf("truncated directory attribute value")
			return make([]byte, n)
		}
		value := record[offset : offset+n]
		offset += n
		return value
	}
	u32 := func() uint32 { return binary.LittleEndian.Uint32(take(4)) }
	u64 := func() uint64 { return binary.LittleEndian.Uint64(take(8)) }
	var entryError uint32
	if common&uint32(C.ATTR_CMN_ERROR) != 0 {
		entryError = u32()
	}
	if common&uint32(C.ATTR_CMN_NAME) == 0 {
		return "", nil, nil, fmt.Errorf("directory attribute has no name")
	}
	refOffset := offset
	relative := int(int32(u32()))
	size := int(u32())
	start := refOffset + relative
	if start < 0 || size < 1 || start > len(record)-size {
		return "", nil, nil, fmt.Errorf("invalid directory attribute name")
	}
	name = string(record[start : start+size-1])
	data := &unix.Stat_t{Nlink: 1}
	if common&uint32(C.ATTR_CMN_DEVID) != 0 {
		data.Dev = int32(u32())
	}
	var kind uint32
	if common&uint32(C.ATTR_CMN_OBJTYPE) != 0 {
		kind = u32()
	}
	switch kind {
	case 1:
		data.Mode = unix.S_IFREG
	case 2:
		data.Mode = unix.S_IFDIR
	case 3:
		data.Mode = unix.S_IFBLK
	case 4:
		data.Mode = unix.S_IFCHR
	case 5:
		data.Mode = unix.S_IFLNK
	case 6:
		data.Mode = unix.S_IFSOCK
	case 7:
		data.Mode = unix.S_IFIFO
	}
	if common&uint32(C.ATTR_CMN_MODTIME) != 0 {
		data.Mtim.Sec = int64(u64())
		data.Mtim.Nsec = int64(u64())
	}
	if common&uint32(C.ATTR_CMN_FILEID) != 0 {
		data.Ino = u64()
	}
	if dirs&uint32(C.ATTR_DIR_ALLOCSIZE) != 0 {
		data.Blocks = int64(u64()) / 512
	}
	if files&uint32(C.ATTR_FILE_LINKCOUNT) != 0 {
		data.Nlink = uint16(u32())
	}
	if files&uint32(C.ATTR_FILE_ALLOCSIZE) != 0 {
		data.Blocks = int64(u64()) / 512
	}
	if extended&uint32(C.ATTR_CMNEXT_PRIVATESIZE) != 0 {
		size := int64(u64())
		private = &size
	}
	required := uint32(C.ATTR_CMN_DEVID | C.ATTR_CMN_OBJTYPE | C.ATTR_CMN_MODTIME | C.ATTR_CMN_FILEID)
	allocated := kind == 2 && dirs&uint32(C.ATTR_DIR_ALLOCSIZE) != 0 || kind != 2 && files&uint32(C.ATTR_FILE_ALLOCSIZE) != 0
	if entryError == 0 && common&required == required && allocated && (kind == 2 || files&uint32(C.ATTR_FILE_LINKCOUNT) != 0) {
		st = data
	}
	return
}
