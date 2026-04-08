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

func TestWorkspacePaneLabelUsesBranchForWorktrees(t *testing.T) {
	ws := &state.Workspace{
		Type:        "worktree",
		Branch:      "feat-auth",
		SessionName: "g/mono/feat-auth",
	}

	if got, want := workspacePaneLabel(ws), "feat-auth"; got != want {
		t.Fatalf("workspacePaneLabel() = %q, want %q", got, want)
	}
}

func TestWorkspacePaneLabelTrimsSessionPrefixForPlainWorkspace(t *testing.T) {
	ws := &state.Workspace{
		Type:        "plain",
		SessionName: "g/notes",
	}

	if got, want := workspacePaneLabel(ws), "notes"; got != want {
		t.Fatalf("workspacePaneLabel() = %q, want %q", got, want)
	}
}

func TestWorkspacePaneLabelPreservesRepoScopedNonWorktreeNames(t *testing.T) {
	ws := &state.Workspace{
		Type:        "dir",
		SessionName: "g/mono/scratch",
	}

	if got, want := workspacePaneLabel(ws), "mono/scratch"; got != want {
		t.Fatalf("workspacePaneLabel() = %q, want %q", got, want)
	}
}

func TestResolvePaneLabelBranchBeatsWorkspaceSession(t *testing.T) {
	ws := &state.Workspace{Type: "plain", SessionName: "g/MAIN"}
	got := resolvePaneLabel(paneLabelInputs{
		cwd:       "/tmp/mono/.grove/worktrees/feat-auth",
		workspace: ws,
		branch:    "feat-auth",
		repoRoot:  "/tmp/mono",
	})
	if got != "feat-auth" {
		t.Fatalf("resolvePaneLabel() = %q, want feat-auth", got)
	}
}

func TestResolvePaneLabelHomeBeatsWorkspaceSession(t *testing.T) {
	ws := &state.Workspace{Type: "plain", SessionName: "g/MAIN"}
	home := "/Users/shadowfax"
	got := resolvePaneLabel(paneLabelInputs{
		cwd:       home,
		home:      home,
		workspace: ws,
	})
	if got != "home" {
		t.Fatalf("resolvePaneLabel() = %q, want home", got)
	}
}

func TestResolvePaneLabelFallsBackToSessionWhenCwdDegenerate(t *testing.T) {
	ws := &state.Workspace{Type: "plain", SessionName: "g/MAIN"}
	got := resolvePaneLabel(paneLabelInputs{
		cwd:       "/",
		workspace: ws,
	})
	if got != "MAIN" {
		t.Fatalf("resolvePaneLabel() = %q, want MAIN", got)
	}
}

func TestResolvePaneLabelMainBranchUsesRepoBasename(t *testing.T) {
	got := resolvePaneLabel(paneLabelInputs{
		cwd:      "/Users/x/code/clis/grove",
		branch:   "main",
		repoRoot: "/Users/x/code/clis/grove",
	})
	if got != "grove" {
		t.Fatalf("resolvePaneLabel() = %q, want grove", got)
	}
}

func TestResolvePaneLabelNonMainBranchUsesBranchName(t *testing.T) {
	got := resolvePaneLabel(paneLabelInputs{
		cwd:      "/Users/x/code/some-repo",
		branch:   "feat/x",
		repoRoot: "/Users/x/code/some-repo",
	})
	if got != "feat/x" {
		t.Fatalf("resolvePaneLabel() = %q, want feat/x", got)
	}
}

func TestResolvePaneLabelDetachedHeadUsesRepoAtSha(t *testing.T) {
	got := resolvePaneLabel(paneLabelInputs{
		cwd:      "/Users/x/code/some-repo",
		repoRoot: "/Users/x/code/some-repo",
		headSha:  "abc1234",
	})
	if got != "some-repo@abc1234" {
		t.Fatalf("resolvePaneLabel() = %q, want some-repo@abc1234", got)
	}
}

func TestResolvePaneLabelHomeReturnsHome(t *testing.T) {
	home := "/Users/x"
	got := resolvePaneLabel(paneLabelInputs{
		cwd:  home,
		home: home,
	})
	if got != "home" {
		t.Fatalf("resolvePaneLabel() = %q, want home", got)
	}
}

func TestResolvePaneLabelFallsBackToBasename(t *testing.T) {
	got := resolvePaneLabel(paneLabelInputs{
		cwd:  "/Users/x/Downloads",
		home: "/Users/x",
	})
	if got != "Downloads" {
		t.Fatalf("resolvePaneLabel() = %q, want Downloads", got)
	}
}

func TestResolvePaneLabelMasterAlsoMapsToRepoBasename(t *testing.T) {
	got := resolvePaneLabel(paneLabelInputs{
		cwd:      "/tmp/legacy",
		branch:   "master",
		repoRoot: "/tmp/legacy",
	})
	if got != "legacy" {
		t.Fatalf("resolvePaneLabel() = %q, want legacy", got)
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
