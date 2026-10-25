package llmstore

import "golang.org/x/sys/unix"

// renameNoReplace fails with EEXIST instead of overwriting destination, even if
// another process creates it between our check and the move.
func renameNoReplace(source, destination string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_EXCL)
}
