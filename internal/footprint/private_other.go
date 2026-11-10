//go:build !darwin || !cgo

package footprint

import "errors"

// PrivateSizeSupported reports whether this build can query APFS private bytes.
func PrivateSizeSupported() bool { return false }
func privateSizeAt(int, string) (int64, error) {
	return 0, errors.New("private size unavailable in this build")
}
