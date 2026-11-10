package inuse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLSOFNULFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "line\nbreak space")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("p123\x00cworker name\x00\nfcwd\x00n" + path + "\x00\n")
	got, err := parseLSOF(data)
	if err != nil || len(got) != 1 || got[0].Command != "worker name" || got[0].PID != 123 {
		t.Fatalf("%+v %v", got, err)
	}
	resolved, _ := canonical(path)
	if got[0].path != resolved {
		t.Fatalf("cwd %q", got[0].path)
	}
	for _, bad := range []string{"", "pbad\x00", "n/tmp\x00"} {
		if _, err := parseLSOF([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
