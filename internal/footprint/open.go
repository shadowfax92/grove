package footprint

import (
	"strings"

	"golang.org/x/sys/unix"
)

// openDirectoryComponents is the portable no-symlink fallback. Only the root
// anchor and the previous/next component stay open, even for very deep paths.
func openDirectoryComponents(rootFD int, path string) (int, error) {
	current, err := unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == "" {
			continue
		}
		next, err := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(current)
		if err != nil {
			return -1, err
		}
		current = next
	}
	return current, nil
}
