package activity

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

var attributeBuffers = sync.Pool{New: func() any { return make([]byte, 32*1024) }}

func directoryTime(ctx context.Context, dir string, names map[string]bool) (time.Time, error) {
	if len(names) < 8 {
		return statTime(ctx, dir, names)
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err == unix.ENOENT {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer unix.Close(fd)
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_ERROR | unix.ATTR_CMN_NAME | unix.ATTR_CMN_OBJTYPE | unix.ATTR_CMN_MODTIME}
	buffer := attributeBuffers.Get().([]byte)
	defer attributeBuffers.Put(buffer)
	var newest time.Time
	for {
		if err := ctx.Err(); err != nil {
			return newest, err
		}
		// macOS exposes no Go wrapper for getattrlistbulk. This stable kernel ABI
		// returns link metadata, never symlink targets. It batches directory metadata
		// instead of issuing one APFS lookup per file across hundreds of worktrees.
		count, _, errno := syscall.Syscall6(unix.SYS_GETATTRLISTBULK, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), unix.FSOPT_PACK_INVAL_ATTRS, 0)
		if errno != 0 {
			// Other filesystems may not vend bulk attributes. Preserve the same rule
			// via portable Lstat rather than treating an unsupported fast path as idle.
			if errno == unix.ENOTSUP || errno == unix.ENOSYS || errno == unix.EINVAL {
				return statTime(ctx, dir, names)
			}
			return newest, errno
		}
		if count == 0 {
			return newest, nil
		}
		timestamp, err := bulkTime(buffer, int(count), names)
		if err != nil {
			return newest, err
		}
		if timestamp.After(newest) {
			newest = timestamp
		}
	}
}

func bulkTime(buffer []byte, count int, names map[string]bool) (time.Time, error) {
	var newest time.Time
	for range count {
		// FSOPT_PACK_INVAL_ATTRS fixes the packed layout: length(4), returned
		// attribute_set_t(20), error(4), name reference(8), vnode type(4), timespec(16).
		// Name offsets are relative to their own reference, not the record start.
		if len(buffer) < 56 {
			return newest, fmt.Errorf("short bulk attribute record")
		}
		length := int(binary.LittleEndian.Uint32(buffer))
		if length < 56 || length > len(buffer) {
			return newest, fmt.Errorf("invalid bulk attribute length")
		}
		record := buffer[:length]
		returned := binary.LittleEndian.Uint32(record[4:])
		offset := 28 + int(int32(binary.LittleEndian.Uint32(record[28:])))
		size := int(binary.LittleEndian.Uint32(record[32:]))
		if offset < 56 || size < 1 || offset > length || size > length-offset || record[offset+size-1] != 0 {
			return newest, fmt.Errorf("invalid bulk attribute name")
		}
		name := string(record[offset : offset+size-1])
		if names[name] {
			if errno := binary.LittleEndian.Uint32(record[24:]); errno != 0 {
				if syscall.Errno(errno) != unix.ENOENT {
					return newest, syscall.Errno(errno)
				}
			} else {
				required := uint32(unix.ATTR_CMN_NAME | unix.ATTR_CMN_OBJTYPE | unix.ATTR_CMN_MODTIME)
				if returned&required != required {
					return newest, fmt.Errorf("missing bulk file attributes")
				}
				if binary.LittleEndian.Uint32(record[36:]) != 2 { // VDIR; Gitlinks are not source files.
					timestamp := time.Unix(int64(binary.LittleEndian.Uint64(record[40:])), int64(binary.LittleEndian.Uint64(record[48:])))
					if timestamp.After(newest) {
						newest = timestamp
					}
				}
			}
		}
		buffer = buffer[length:]
	}
	return newest, nil
}
