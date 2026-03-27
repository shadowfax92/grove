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

func TestFindWorkspaceRefMatchesNameAndSession(t *testing.T) {
	mgr := &state.StateManager{}
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "mono/feat-auth", SessionName: "g/mono/feat-auth"},
		},
	}

	if got := findWorkspaceRef(mgr, st, "mono/feat-auth"); got == nil || got.SessionName != "g/mono/feat-auth" {
		t.Fatalf("findWorkspaceRef(name) = %#v, want session g/mono/feat-auth", got)
	}
	if got := findWorkspaceRef(mgr, st, "g/mono/feat-auth"); got == nil || got.Name != "mono/feat-auth" {
		t.Fatalf("findWorkspaceRef(session) = %#v, want workspace mono/feat-auth", got)
	}
}

func TestFindWorkspaceByCwdPrefersMostSpecificMatch(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "notes", Type: "plain", Path: "/tmp"},
			{Name: "mono/feat-auth", Type: "worktree", WorktreePath: "/tmp/mono/.grove/worktrees/feat-auth"},
		},
	}

	got, err := findWorkspaceByCwd(st, "/tmp/mono/.grove/worktrees/feat-auth/app")
	if err != nil {
		t.Fatalf("findWorkspaceByCwd() error = %v", err)
	}
	if got.Name != "mono/feat-auth" {
		t.Fatalf("findWorkspaceByCwd() = %q, want mono/feat-auth", got.Name)
	}
}

func TestFindWorkspaceByCwdErrorsOnAmbiguousSameRoot(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "mono/alpha", Type: "dir", Path: "/tmp/mono"},
			{Name: "mono/beta", Type: "dir", Path: "/tmp/mono"},
		},
	}

	_, err := findWorkspaceByCwd(st, "/tmp/mono/internal")
	if err == nil {
		t.Fatalf("findWorkspaceByCwd() error = nil, want ambiguity error")
	}
}
