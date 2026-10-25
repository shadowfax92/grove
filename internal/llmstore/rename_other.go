//go:build !darwin && !linux

package llmstore

import (
	"fmt"
	"io/fs"
	"os"
)

// renameNoReplace has no kernel no-clobber move here, so a destination created
// between the check and the rename could still be replaced.
func renameNoReplace(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("%s: %w", destination, fs.ErrExist)
	}
	return os.Rename(source, destination)
}
