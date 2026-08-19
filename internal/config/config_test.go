package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNewWorktreeRepoUsesMinimalDefaults(t *testing.T) {
	repo := NewWorktreeRepo("/tmp/project", "project", "main")
	if repo.Path != "/tmp/project" || repo.Name != "project" || repo.DefaultBranch != "main" {
		t.Fatalf("NewWorktreeRepo() = %#v", repo)
	}
	if repo.Setup == nil || len(repo.Setup) != 0 {
		t.Fatalf("Setup = %#v, want explicit empty list", repo.Setup)
	}
}

func TestLoadCreatesMinimalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Repos) != 0 {
		t.Fatalf("Repos = %#v, want empty", cfg.Repos)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "grove", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "worktree_root") || strings.Contains(string(data), "reap:") {
		t.Fatalf("legacy config fields in fresh config:\n%s", data)
	}
	if !strings.Contains(string(data), "repos: []") {
		t.Fatalf("fresh config missing repos list:\n%s", data)
	}
}

func TestLoadIsTolerantOfMissingRepositories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	missing := filepath.Join(home, "deleted")
	writeConfigFile(t, filepath.Join(home, ".config", "grove", "config.yaml"), "repos:\n  - path: "+missing+"\n    name: deleted\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != missing {
		t.Fatalf("Repos = %#v", cfg.Repos)
	}
}

func TestLoadExpandsRepoTildeAndLegacyFieldsDoNotFail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "grove", "config.yaml")
	writeConfigFile(t, path, "worktree_root: ~/old\nreap:\n  ttl: 6h\nrepos:\n  - path: ~/code/app\n    name: app\n    prepare:\n      - git pull\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Repos[0].Path, filepath.Join(home, "code", "app"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestAddRepoPreservesExistingTextAndAppendsMinimalEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	existingPath := t.TempDir()
	newPath := t.TempDir()
	writeConfigFile(t, path, "# custom\nworktree_root: ~/legacy\nrepos:\n  - path: "+existingPath+"\n    name: existing\n    prepare:\n      - git pull\n")
	if err := AddRepoToFile(path, NewWorktreeRepo(newPath, "new", "main")); err != nil {
		t.Fatalf("AddRepoToFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# custom", "worktree_root: ~/legacy", "prepare:", "path: " + newPath, "name: new", "default_branch: main", "setup: []"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("updated config is invalid YAML: %v", err)
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("repo count = %d, want 2", len(cfg.Repos))
	}
}

func TestAddRepoRejectsDuplicateNameAndPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	existingPath := t.TempDir()
	writeConfigFile(t, path, "repos:\n  - path: "+existingPath+"\n    name: existing\n")
	if err := AddRepoToFile(path, NewWorktreeRepo(t.TempDir(), "existing", "main")); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if err := AddRepoToFile(path, NewWorktreeRepo(existingPath+string(os.PathSeparator), "other", "main")); err == nil {
		t.Fatal("duplicate path accepted")
	}
}

func writeConfigFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
