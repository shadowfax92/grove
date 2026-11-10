package footprint

import "golang.org/x/sys/unix"

func openDirectoryAt(rootFD int, path string) (int, error) {
	fd, err := unix.Openat(rootFD, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW_ANY|unix.O_CLOEXEC, 0)
	if err == unix.EINVAL {
		return openDirectoryComponents(rootFD, path)
	}
	return fd, err
}
