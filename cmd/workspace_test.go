package cmd

import (
	"os"
	"testing"

	"grove/internal/state"
)

func TestWorkspaceDirUsesWorktreePathForWorktrees(t *testing.T) {
	ws := &state.Workspace{
		Type:         "worktree",
		WorktreePath: "/tmp/worktree",
		Path:         "/tmp/plain",
	}

	if got, want := workspaceDir(ws), "/tmp/worktree"; got != want {
		t.Fatalf("workspaceDir() = %q, want %q", got, want)
	}
}

func TestWorkspaceDirUsesPathForDirWorkspace(t *testing.T) {
	ws := &state.Workspace{
		Type: "dir",
		Path: "/tmp/project",
	}

	if got, want := workspaceDir(ws), "/tmp/project"; got != want {
		t.Fatalf("workspaceDir() = %q, want %q", got, want)
	}
}

func TestWorkspaceDirFallsBackToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	ws := &state.Workspace{Type: "plain"}
	if got, want := workspaceDir(ws), home; got != want {
		t.Fatalf("workspaceDir() = %q, want %q", got, want)
	}
}
