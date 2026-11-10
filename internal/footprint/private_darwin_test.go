//go:build darwin && cgo

package footprint

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateSizeExcludesClonedData(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	writeFile(t, source, 65536)
	root := t.TempDir()
	if err := unix.Clonefile(source, filepath.Join(root, "clone"), 0); err != nil {
		t.Skipf("clonefile unsupported: %v", err)
	}
	entry, err := Scan(context.Background(), Request{Path: root, PrivateSize: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Bytes < 65536 || entry.PrivateBytes == nil || *entry.PrivateBytes != 0 {
		t.Fatalf("cloned data charged as private: %#v", entry)
	}
}
