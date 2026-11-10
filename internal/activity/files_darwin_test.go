package activity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBulkTimeMatchesPortableRule(t *testing.T) {
	root := t.TempDir()
	names := map[string]bool{}
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	for i := range 16 {
		name := fmt.Sprintf("file-%d", i)
		if i == 1 {
			name = "space\nnewline"
		}
		names[name] = true
		write := filepath.Join(root, name)
		if err := os.WriteFile(write, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp(t, write, old.Add(time.Duration(i)*time.Second))
	}
	// Ignore entries absent from Git's list, even if their mtimes are newest.
	if err := os.WriteFile(filepath.Join(root, "ignored"), []byte("output"), 0600); err != nil {
		t.Fatal(err)
	}
	names["missing"] = true
	if err := os.Mkdir(filepath.Join(root, "gitlink"), 0700); err != nil {
		t.Fatal(err)
	}
	names["gitlink"] = true
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	names["link"] = true
	expected, err := statTime(context.Background(), root, names)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := directoryTime(context.Background(), root, names)
	if err != nil {
		t.Fatal(err)
	}
	if !expected.Equal(actual) {
		t.Fatalf("bulk %v portable %v", actual, expected)
	}
}
func TestBulkTimeRejectsMalformedRecords(t *testing.T) {
	for _, buffer := range [][]byte{nil, make([]byte, 56), append([]byte{255, 255, 255, 127}, make([]byte, 60)...)} {
		if _, err := bulkTime(buffer, 1, map[string]bool{}); err == nil {
			t.Fatal("accepted malformed record")
		}
	}
}
