package syncfile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanPrunesReposAndExcludedDirectories(t *testing.T) {
	root := t.TempDir()
	mkdir := func(rel string) string {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	repo := func(rel string) string {
		path := mkdir(rel)
		mkdir(filepath.ToSlash(filepath.Join(rel, ".git")))
		return path
	}

	clis := repo("clis/grove")
	nested := repo("hacks/tools/thing")
	repo(".hidden/secret")
	repo("node_modules/vendor")
	worktree := mkdir("browseros/worktree")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: elsewhere"), 0644); err != nil {
		t.Fatal(err)
	}
	repo("browseros/worktree/nested-should-prune")
	noOrigin := repo("learn/no-origin")

	origins := map[string]string{
		clis:   "https://example.com/grove.git",
		nested: "https://example.com/thing.git",
	}
	candidates, warnings, err := Scan(root, 3, func(path string) (string, error) {
		if url := origins[path]; url != "" {
			return url, nil
		}
		return "", errors.New("no origin")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{
		{Path: clis, Relative: "clis/grove", Group: "clis", Name: "grove", URL: "https://example.com/grove.git"},
		{Path: nested, Relative: "hacks/tools/thing", Group: "hacks", Name: "tools/thing", URL: "https://example.com/thing.git"},
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("Scan() = %#v, want %#v", candidates, want)
	}
	if len(warnings) != 1 || warnings[0].Path != noOrigin {
		t.Fatalf("warnings = %#v, want no-origin only", warnings)
	}
}

func TestFilterNewCandidatesUsesTargetPathNotURL(t *testing.T) {
	m := &Manifest{Root: "/code", Groups: map[string][]Entry{
		"old": {{URL: "https://example.com/same.git", Name: "one"}},
	}}
	candidates := []Candidate{
		{Group: "old", Name: "one", URL: "https://example.com/same.git"},
		{Group: "old", Name: "two", URL: "https://example.com/same.git"},
	}
	got := FilterNewCandidates(candidates, m)
	if len(got) != 1 || got[0].Name != "two" {
		t.Fatalf("FilterNewCandidates() = %#v", got)
	}
}
