package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseTTLAcceptsDayShorthand(t *testing.T) {
	got, err := ParseTTL("1d")
	if err != nil {
		t.Fatalf("ParseTTL() error = %v", err)
	}
	if got != 24*time.Hour {
		t.Fatalf("ParseTTL() = %v, want %v", got, 24*time.Hour)
	}
}

func TestParseTTLRejectsInvalidValue(t *testing.T) {
	if _, err := ParseTTL("nope"); err == nil {
		t.Fatal("ParseTTL() error = nil, want parse error")
	}
}

func TestDurationUnmarshalsFromYAML(t *testing.T) {
	var s struct {
		TTL Duration `yaml:"ttl"`
	}
	if err := yaml.Unmarshal([]byte("ttl: 90m\n"), &s); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if s.TTL.Duration() != 90*time.Minute {
		t.Fatalf("TTL = %v, want 90m", s.TTL.Duration())
	}
}

func TestNewWorktreeRepoUsesInitDefaults(t *testing.T) {
	repo := NewWorktreeRepo("/tmp/project", "project", "main")

	if got, want := repo.Path, "/tmp/project"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := repo.Name, "project"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := repo.DefaultBranch, "main"; got != want {
		t.Fatalf("DefaultBranch = %q, want %q", got, want)
	}
	if repo.WorktreeRoot != "" {
		t.Fatalf("WorktreeRoot = %q, want empty repo override", repo.WorktreeRoot)
	}
	if repo.Type != "" {
		t.Fatalf("Type = %q, want empty worktree default", repo.Type)
	}
	if repo.Setup == nil {
		t.Fatal("Setup = nil, want explicit empty setup list")
	}

	if repo.Prepare == nil || len(repo.Prepare) != 0 {
		t.Fatalf("Prepare = %#v, want explicit empty list", repo.Prepare)
	}
}

func TestAddRepoToFileAppendsWorktreeRepo(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	existingPath := t.TempDir()
	projectPath := t.TempDir()
	writeConfigFile(t, configPath, strings.Join([]string{
		"# Grove configuration",
		"worktree_root: ~/worktrees",
		"shadow: {}",
		"repos:",
		"  - path: " + existingPath,
		"    name: existing",
		"",
	}, "\n"))

	repo := NewWorktreeRepo(projectPath, "project", "main")
	if err := AddRepoToFile(configPath, repo); err != nil {
		t.Fatalf("AddRepoToFile() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"# Grove configuration",
		"worktree_root: ~/worktrees",
		"name: project",
		"default_branch: main",
		"prepare: []",
		"setup: []",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "    worktree_root:") {
		t.Fatalf("config rendered repo-level worktree_root unexpectedly:\n%s", out)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing updated config: %v", err)
	}
	if got, want := len(cfg.Repos), 2; got != want {
		t.Fatalf("repo count = %d, want %d", got, want)
	}
	if got, want := cfg.Repos[1].Name, "project"; got != want {
		t.Fatalf("appended repo name = %q, want %q", got, want)
	}
	if got, want := cfg.Repos[1].Path, projectPath; got != want {
		t.Fatalf("appended repo path = %q, want %q", got, want)
	}
}

func TestLoadCreatesDefaultGlobalWorktreeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.WorktreeRoot, filepath.Join(home, "worktrees"); got != want {
		t.Fatalf("WorktreeRoot = %q, want %q", got, want)
	}
	if got := cfg.Reap.TTL.Duration(); got != DefaultReapTTL {
		t.Fatalf("Reap TTL = %v, want %v", got, DefaultReapTTL)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "grove", "config.yaml"))
	if err != nil {
		t.Fatalf("reading default config: %v", err)
	}
	out := string(data)
	for _, want := range []string{"worktree_root: ~/worktrees", "reap:", "ttl: 6h"} {
		if !strings.Contains(out, want) {
			t.Fatalf("default config missing %q:\n%s", want, out)
		}
	}
}

func TestLoadDefaultsReapTTLWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoPath := t.TempDir()
	configPath := filepath.Join(home, ".config", "grove", "config.yaml")
	writeConfigFile(t, configPath, strings.Join([]string{
		"worktree_root: ~/worktrees",
		"repos:",
		"  - path: " + repoPath,
		"    name: project",
		"",
	}, "\n"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Reap.TTL.Duration(); got != DefaultReapTTL {
		t.Fatalf("Reap TTL = %v, want %v", got, DefaultReapTTL)
	}
}

func TestLoadExpandsWorktreeRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoPath := t.TempDir()
	overrideRepoPath := t.TempDir()
	configPath := filepath.Join(home, ".config", "grove", "config.yaml")
	writeConfigFile(t, configPath, strings.Join([]string{
		"worktree_root: ~/worktrees",
		"repos:",
		"  - path: " + repoPath,
		"    name: project",
		"  - path: " + overrideRepoPath,
		"    name: custom",
		"    worktree_root: ~/custom-worktrees",
		"",
	}, "\n"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.WorktreeRoot, filepath.Join(home, "worktrees"); got != want {
		t.Fatalf("WorktreeRoot = %q, want %q", got, want)
	}
	if got, want := cfg.Repos[1].WorktreeRoot, filepath.Join(home, "custom-worktrees"); got != want {
		t.Fatalf("repo override WorktreeRoot = %q, want %q", got, want)
	}
}

func TestLoadKeepsMissingRepositoriesForTolerantCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	missing := filepath.Join(home, "deleted-repository")
	configPath := filepath.Join(home, ".config", "grove", "config.yaml")
	writeConfigFile(t, configPath, strings.Join([]string{
		"repos:",
		"  - path: " + missing,
		"    name: deleted",
		"",
	}, "\n"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want tolerant parse", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != missing {
		t.Fatalf("Repos = %#v, want missing entry preserved", cfg.Repos)
	}
}

func TestEffectiveWorktreeRoot(t *testing.T) {
	cfg := &Config{WorktreeRoot: "/tmp/worktrees"}
	repo := &RepoConfig{Name: "project", Path: "/tmp/project"}

	if got, want := cfg.EffectiveWorktreeRoot(repo), "/tmp/worktrees/project"; got != want {
		t.Fatalf("EffectiveWorktreeRoot(global) = %q, want %q", got, want)
	}

	repo.WorktreeRoot = "/tmp/custom"
	if got, want := cfg.EffectiveWorktreeRoot(repo), "/tmp/custom"; got != want {
		t.Fatalf("EffectiveWorktreeRoot(override) = %q, want %q", got, want)
	}

	repo.WorktreeRoot = ""
	if got := (&Config{}).EffectiveWorktreeRoot(repo); got != "" {
		t.Fatalf("EffectiveWorktreeRoot(no root) = %q, want empty", got)
	}
}

func TestAddRepoToFileRejectsDuplicateName(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	existingPath := t.TempDir()
	writeConfigFile(t, configPath, strings.Join([]string{
		"repos:",
		"  - path: " + existingPath,
		"    name: project",
		"",
	}, "\n"))

	err := AddRepoToFile(configPath, NewWorktreeRepo(t.TempDir(), "project", "main"))
	if err == nil {
		t.Fatal("AddRepoToFile() error = nil, want duplicate name error")
	}
	if !strings.Contains(err.Error(), "repo name project already exists") {
		t.Fatalf("AddRepoToFile() error = %q, want duplicate name", err)
	}
}

func TestAddRepoToFileRejectsDuplicatePath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	projectPath := t.TempDir()
	writeConfigFile(t, configPath, strings.Join([]string{
		"repos:",
		"  - path: " + projectPath + string(os.PathSeparator),
		"    name: existing",
		"",
	}, "\n"))

	err := AddRepoToFile(configPath, NewWorktreeRepo(projectPath, "project", "main"))
	if err == nil {
		t.Fatal("AddRepoToFile() error = nil, want duplicate path error")
	}
	if !strings.Contains(err.Error(), "repo path "+projectPath+" already exists") {
		t.Fatalf("AddRepoToFile() error = %q, want duplicate path", err)
	}
}

func writeConfigFile(t *testing.T, path, data string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}
