package workspaces

import (
	"testing"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
)

func TestBuildInventoryTracksManagedWorkspaces(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
		},
	}

	inv, err := Build(st, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if got, want := len(inv.Managed), 2; got != want {
		t.Fatalf("managed count = %d, want %d", got, want)
	}
	if _, ok := inv.FindManaged("alpha"); !ok {
		t.Fatalf("FindManaged(alpha) = missing")
	}
	if _, ok := inv.FindManaged("g/beta"); !ok {
		t.Fatalf("FindManaged(g/beta) = missing")
	}
}

func TestResolveRemoveTargetsResolvesManagedRefs(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
		},
	}

	inv, err := Build(st, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	targets, err := inv.ResolveRemoveTargets([]string{"alpha", "g/beta"})
	if err != nil {
		t.Fatalf("ResolveRemoveTargets() error = %v", err)
	}
	if got, want := len(targets), 2; got != want {
		t.Fatalf("target count = %d, want %d", got, want)
	}
	if got, want := targets[0].SessionName, "g/alpha"; got != want {
		t.Fatalf("first target session = %q, want %q", got, want)
	}

	if _, err := inv.ResolveRemoveTargets([]string{"nope"}); err == nil {
		t.Fatal("ResolveRemoveTargets(unknown) error = nil, want not-found")
	}
}

func TestCleanupTargetsReturnsOrphanWorktreesOnly(t *testing.T) {
	restore := stubListWorktrees(func(repoPath string) ([]git.WorktreeInfo, error) {
		return []git.WorktreeInfo{
			{Path: repoPath + "/.grove/worktrees/tracked", Branch: "tracked"},
			{Path: repoPath + "/.grove/worktrees/orphan", Branch: "orphan"},
			{Path: "/tmp/other/.grove/worktrees/foreign", Branch: "foreign"},
			{Path: repoPath + "/external", Branch: "external"},
			{Path: repoPath + "/.grove/worktrees/bare", Bare: true},
		}, nil
	})
	defer restore()

	repoPath := "/tmp/mono"
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "mono/tracked", Type: "worktree", Repo: "mono", RepoPath: repoPath, WorktreePath: repoPath + "/.grove/worktrees/tracked", SessionName: "g/mono/tracked"},
		},
	}
	cfg := &config.Config{
		Repos: []config.RepoConfig{{Name: "mono", Path: repoPath, Type: "worktree"}},
	}

	inv, err := Build(st, cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	targets := inv.CleanupTargets()
	if got, want := len(targets), 1; got != want {
		t.Fatalf("cleanup target count = %d, want %d", got, want)
	}
	if got, want := targets[0].Label, "mono/orphan"; got != want {
		t.Fatalf("orphan label = %q, want %q", got, want)
	}
}

func TestCleanupTargetsIncludesConfiguredWorktreeRootOrphans(t *testing.T) {
	worktreeRoot := "/tmp/worktrees/mono"
	restore := stubListWorktrees(func(repoPath string) ([]git.WorktreeInfo, error) {
		return []git.WorktreeInfo{
			{Path: worktreeRoot + "/tracked", Branch: "tracked"},
			{Path: worktreeRoot + "/orphan", Branch: "orphan"},
			{Path: "/tmp/other/orphan", Branch: "external"},
		}, nil
	})
	defer restore()

	repoPath := "/tmp/mono"
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "mono/tracked", Type: "worktree", Repo: "mono", RepoPath: repoPath, WorktreePath: worktreeRoot + "/tracked", SessionName: "g/mono/tracked"},
		},
	}
	cfg := &config.Config{
		Repos: []config.RepoConfig{{Name: "mono", Path: repoPath, Type: "worktree", WorktreeRoot: worktreeRoot}},
	}

	inv, err := Build(st, cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	targets := inv.CleanupTargets()
	if got, want := len(targets), 1; got != want {
		t.Fatalf("cleanup target count = %d, want %d", got, want)
	}
	if got, want := targets[0].WorktreePath, worktreeRoot+"/orphan"; got != want {
		t.Fatalf("orphan path = %q, want %q", got, want)
	}
	if got, want := targets[0].Label, "mono/orphan"; got != want {
		t.Fatalf("orphan label = %q, want %q", got, want)
	}
}

func stubListWorktrees(fn func(string) ([]git.WorktreeInfo, error)) func() {
	prev := listWorktrees
	listWorktrees = fn
	return func() { listWorktrees = prev }
}
