//go:build darwin || linux

package trash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func directoryID(path string) (string, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("refusing non-directory %s", path)
	}
	return strconv.FormatUint(uint64(st.Dev), 16) + ":" + strconv.FormatUint(st.Ino, 16), nil
}

// Every descent uses a directory descriptor and O_NOFOLLOW, so a writer swapping
// a directory for a symlink cannot redirect unlink or chmod into a notes store.
// Permissions change only on opened directories, never through a pathname link.
func deleteTree(ctx context.Context, path string) error {
	parent, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	return deleteAt(ctx, parent, filepath.Base(path), 0)
}
func deleteAt(ctx context.Context, parent int, name string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 1024 {
		return fmt.Errorf("trash tree exceeds maximum depth")
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		err = unix.Unlinkat(parent, name, 0)
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err = unix.Fchmod(fd, 0700); err != nil {
		return err
	}
	for range 8 {
		if _, err = f.Seek(0, 0); err != nil {
			return err
		}
		// Read bounded batches; huge node_modules directories must not be buffered whole.
		for {
			names, readErr := f.Readdirnames(256)
			for _, child := range names {
				if err = deleteAt(ctx, fd, child, depth+1); err != nil {
					return err
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					return readErr
				}
				break
			}
		}
		err = unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
		if err == nil || errors.Is(err, unix.ENOENT) {
			return nil
		}
		if !errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	return fmt.Errorf("trash tree is still being written; will retry later: %s", name)
}
