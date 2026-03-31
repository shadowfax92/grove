package workspaces

import (
	"reflect"
	"testing"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
)

func TestBuildInventoryIncludesStoppedManagedAndUnmanagedSessions(t *testing.T) {
	restore := stubWorkspaceRuntime(
		func() ([]string, error) {
			return []string{"g/beta", "scratch"}, nil
		},
		func(string) ([]git.WorktreeInfo, error) {
			return nil, nil
		},
	)
	defer restore()

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

	alpha, ok := inv.FindManaged("alpha")
	if !ok {
		t.Fatalf("FindManaged(alpha) = missing")
	}
	if alpha.Running {
		t.Fatalf("alpha should be stopped")
	}

	beta, ok := inv.FindManaged("g/beta")
	if !ok {
		t.Fatalf("FindManaged(g/beta) = missing")
	}
	if !beta.Running {
		t.Fatalf("beta should be running")
	}

	if got, want := inv.Unmanaged, []UnmanagedSession{{SessionName: "scratch"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected unmanaged sessions: got %#v want %#v", got, want)
	}
}

func TestResolveRemoveTargetsSupportsManagedAndUnmanagedRefs(t *testing.T) {
	restore := stubWorkspaceRuntime(
		func() ([]string, error) {
			return []string{"g/beta", "scratch"}, nil
		},
		func(string) ([]git.WorktreeInfo, error) {
			return nil, nil
		},
	)
	defer restore()

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

	targets, err := inv.ResolveRemoveTargets([]string{"alpha", "g/beta", "scratch"})
	if err != nil {
		t.Fatalf("ResolveRemoveTargets() error = %v", err)
	}

	if got, want := len(targets), 3; got != want {
		t.Fatalf("unexpected target count: got %d want %d", got, want)
	}
	if got, want := targets[0].SessionName, "g/alpha"; got != want {
		t.Fatalf("first target session = %q want %q", got, want)
	}
	if got, want := targets[1].Kind, RemoveManagedWorkspace; got != want {
		t.Fatalf("second target kind = %q want %q", got, want)
	}
	if got, want := targets[2].Kind, RemoveUnmanagedSession; got != want {
		t.Fatalf("third target kind = %q want %q", got, want)
	}
}

func TestCleanupTargetsIncludeStoppedManagedAndOrphansAsValues(t *testing.T) {
	restore := stubWorkspaceRuntime(
		func() ([]string, error) {
			return []string{"g/mono/running"}, nil
		},
		func(repoPath string) ([]git.WorktreeInfo, error) {
			return []git.WorktreeInfo{
				{Path: repoPath + "/.grove/worktrees/tracked", Branch: "tracked"},
				{Path: repoPath + "/.grove/worktrees/orphan", Branch: "orphan"},
				{Path: repoPath + "/external", Branch: "external"},
				{Path: repoPath + "/.grove/worktrees/bare", Bare: true},
			}, nil
		},
	)
	defer restore()

	repoPath := "/tmp/mono"
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "mono/running", Type: "worktree", Repo: "mono", RepoPath: repoPath, WorktreePath: repoPath + "/.grove/worktrees/running", SessionName: "g/mono/running"},
			{Name: "mono/stale", Type: "worktree", Repo: "mono", RepoPath: repoPath, WorktreePath: repoPath + "/.grove/worktrees/stale", SessionName: "g/mono/stale"},
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
	if got, want := len(targets), 3; got != want {
		t.Fatalf("unexpected cleanup target count: got %d want %d", got, want)
	}
	if got, want := targets[0].Label, "mono/stale"; got != want {
		t.Fatalf("first cleanup target label = %q want %q", got, want)
	}
	if got, want := targets[2].Label, "mono/orphan"; got != want {
		t.Fatalf("orphan cleanup target label = %q want %q", got, want)
	}

	st.Workspaces[1].Name = "changed"
	if got, want := targets[0].Workspace.Name, "mono/stale"; got != want {
		t.Fatalf("cleanup target workspace mutated with state: got %q want %q", got, want)
	}
}

func stubWorkspaceRuntime(
	sessionFn func() ([]string, error),
	worktreeFn func(string) ([]git.WorktreeInfo, error),
) func() {
	prevListSessions := listSessions
	prevListWorktrees := listWorktrees
	listSessions = sessionFn
	listWorktrees = worktreeFn
	return func() {
		listSessions = prevListSessions
		listWorktrees = prevListWorktrees
	}
}
