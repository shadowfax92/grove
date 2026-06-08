package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
	"grove/internal/state"
)

func TestRepoNameForPathMatchesRegisteredRepo(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Repos: []config.RepoConfig{{
		Name: "mono",
		Path: root,
	}}}

	got, err := repoNameForPath(cfg, &state.State{}, filepath.Join(root, "packages", "app"))
	if err != nil {
		t.Fatalf("repoNameForPath() error = %v", err)
	}
	if got != "mono" {
		t.Fatalf("repoNameForPath() = %q, want mono", got)
	}
}

func TestRepoNameForPathMatchesManagedWorktree(t *testing.T) {
	worktreePath := t.TempDir()
	st := &state.State{Workspaces: []state.Workspace{{
		Name:         "mono/feat-auth",
		Type:         "worktree",
		Repo:         "mono",
		WorktreePath: worktreePath,
	}}}

	got, err := repoNameForPath(&config.Config{}, st, filepath.Join(worktreePath, "cmd"))
	if err != nil {
		t.Fatalf("repoNameForPath() error = %v", err)
	}
	if got != "mono" {
		t.Fatalf("repoNameForPath() = %q, want mono", got)
	}
}

func TestRepoNameForPathErrorsWhenUnregistered(t *testing.T) {
	_, err := repoNameForPath(&config.Config{}, &state.State{}, t.TempDir())
	if err == nil {
		t.Fatal("repoNameForPath() error = nil, want unregistered error")
	}
	if !strings.Contains(err.Error(), "unregistered") {
		t.Fatalf("repoNameForPath() error = %q, want unregistered", err)
	}
}
