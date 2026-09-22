package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPruneMissingReposPreservesUserSettingsAndSupportsDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	live := t.TempDir() // Existing non-Git directories must survive cleanup too.
	path := filepath.Join(home, "config.yaml")
	original := fmt.Sprintf(`# custom config
legacy_setting: keep
repos:
  - path: ~/deleted
    name: deleted
  # retained profile
  - path: %s
    name: live
    setup: ["echo hello"]
    custom_setting: keep
  - path: ~/legacy-deleted
    name: legacy
    type: dir
  - path: relative/missing
    name: invalid
  - path: ~/unsupported
    name: unsupported
    type: mystery
`, live)
	writeConfigFile(t, path, original)
	removed, err := PruneMissingRepos(path, true)
	if err != nil || len(removed) != 1 || removed[0].Path != filepath.Join(home, "deleted") {
		t.Fatalf("preview = %#v, %v", removed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("preview changed config: %q, %v", data, err)
	}
	removed, err = PruneMissingRepos(path, false)
	if err != nil || len(removed) != 1 || removed[0].Name != "deleted" {
		t.Fatalf("prune = %#v, %v", removed, err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# custom config", "legacy_setting: keep", "# retained profile", "echo hello", "custom_setting: keep", "~/legacy-deleted", "relative/missing", "~/unsupported"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lost %q:\n%s", want, data)
		}
	}
	cfg, err := parseConfig(data)
	if err != nil || len(cfg.Repos) != 4 || cfg.Repos[0].Name != "live" {
		t.Fatalf("pruned config = %#v, %v", cfg, err)
	}
	removed, err = PruneMissingRepos(path, false)
	if err != nil || len(removed) != 0 {
		t.Fatalf("second prune = %#v, %v", removed, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(data) {
		t.Fatalf("no-op prune rewrote config: %q, %v", unchanged, err)
	}
}

func TestPruneMissingReposPreservesSymlinkAndFileMode(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "tracked.yaml")
	link := filepath.Join(directory, "config.yaml")
	writeConfigFile(t, target, fmt.Sprintf("repos: [{path: %s, name: gone}] # keep\n", filepath.Join(directory, "gone")))
	if err := os.Chmod(target, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tracked.yaml", link); err != nil {
		t.Fatal(err)
	}
	removed, err := PruneMissingRepos(link, false)
	if err != nil || len(removed) != 1 {
		t.Fatalf("prune = %#v, %v", removed, err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink replaced: %v, %v", info, err)
	}
	info, err = os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions changed: %v, %v", info, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "repos: []") || !strings.Contains(string(data), "# keep") {
		t.Fatalf("target config = %q, %v", data, err)
	}
}

func TestPruneMissingReposRetainsInaccessiblePaths(t *testing.T) {
	directory := t.TempDir()
	blocked := filepath.Join(directory, "blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0700) })
	unreadable := filepath.Join(blocked, "repo")
	if _, err := os.Stat(unreadable); !os.IsPermission(err) {
		t.Skipf("filesystem does not enforce directory permissions: %v", err)
	}
	path := filepath.Join(directory, "config.yaml")
	original := fmt.Sprintf("repos:\n  - path: %s\n    name: inaccessible\n", unreadable)
	writeConfigFile(t, path, original)
	removed, err := PruneMissingRepos(path, false)
	if err != nil || len(removed) != 0 {
		t.Fatalf("prune = %#v, %v", removed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("inaccessible entry changed: %q, %v", data, err)
	}
}

func TestPruneMissingReposRejectsDanglingYAMLAnchorWithoutWriting(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	original := fmt.Sprintf("repos:\n  - path: %s\n    name: gone\n    setup: &setup [echo hello]\n  - path: %s\n    name: live\n    setup: *setup\n", filepath.Join(directory, "gone"), directory)
	writeConfigFile(t, path, original)
	if _, err := PruneMissingRepos(path, false); err == nil {
		t.Fatal("pruned YAML anchor still needed by live entry")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("failed prune changed config: %q, %v", data, err)
	}
}

func TestPruneMissingReposSerializesWithRegistration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	writeConfigFile(t, path, fmt.Sprintf("repos:\n  - path: %s\n    name: gone\n", filepath.Join(directory, "gone")))
	const count = 8
	start := make(chan struct{})
	errors := make(chan error, count*2)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		repo := NewWorktreeRepo(t.TempDir(), fmt.Sprintf("repo-%d", index), "main")
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			errors <- AddRepoToFile(path, repo)
		}()
		go func() {
			defer group.Done()
			<-start
			_, err := PruneMissingRepos(path, false)
			errors <- err
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfig(data)
	if err != nil || len(cfg.Repos) != count {
		t.Fatalf("concurrent registration lost: %#v, %v", cfg, err)
	}
}
