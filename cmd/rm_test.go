package cmd

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"grove/internal/state"
	"grove/internal/workspaces"
)

func TestRemoveManagedEntriesRemovesSelectedWorkspaces(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets := []workspaces.RemoveTarget{
		{Workspace: state.Workspace{Name: "alpha", SessionName: "g/alpha"}, SessionName: "g/alpha"},
		{Workspace: state.Workspace{Name: "gamma", SessionName: "g/gamma"}, SessionName: "g/gamma"},
	}

	workspaces.RemoveManagedEntries(st, targets)

	if got, want := len(st.Workspaces), 1; got != want {
		t.Fatalf("workspace count after removal = %d, want %d", got, want)
	}
	if got, want := st.Workspaces[0].SessionName, "g/beta"; got != want {
		t.Fatalf("remaining workspace = %q, want %q", got, want)
	}
}

func TestRemoveWorktreesGroupsByRepoAndMapsFailures(t *testing.T) {
	// a2 is the 3rd target but shares repoA with a1; its failure must map back to
	// its own slot (not b1's) even though grouping reshuffles iteration order.
	targets := []workspaces.RemoveTarget{
		{Workspace: state.Workspace{Name: "a1", RepoPath: "/repoA", WorktreePath: "/repoA/a1"}},
		{Workspace: state.Workspace{Name: "b1", RepoPath: "/repoB", WorktreePath: "/repoB/b1"}},
		{Workspace: state.Workspace{Name: "a2", RepoPath: "/repoA", WorktreePath: "/repoA/a2"}},
	}

	var mu sync.Mutex
	seqByRepo := map[string][]string{}
	remove := func(target workspaces.RemoveTarget) error {
		mu.Lock()
		seqByRepo[target.Workspace.RepoPath] = append(seqByRepo[target.Workspace.RepoPath], target.Workspace.Name)
		mu.Unlock()
		if target.Workspace.Name == "a2" {
			return fmt.Errorf("boom")
		}
		return nil
	}

	defer silenceStdio(t)()
	failed := removeWorktrees(targets, remove)

	if len(failed) != 1 || failed[0].Name != "a2" {
		t.Fatalf("failed = %+v, want exactly [a2]", failed)
	}
	if got := seqByRepo["/repoA"]; len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Fatalf("repoA removal order = %v, want [a1 a2] (sequential within a repo)", got)
	}
	if got := seqByRepo["/repoB"]; len(got) != 1 || got[0] != "b1" {
		t.Fatalf("repoB removal order = %v, want [b1]", got)
	}
}

// silenceStdio redirects stdout/stderr to /dev/null for the duration of a test
// so removeWorktrees' progress lines don't clutter test output. Safe because
// package tests run sequentially.
func silenceStdio(t *testing.T) func() {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	return func() {
		os.Stdout, os.Stderr = origOut, origErr
		devnull.Close()
	}
}

func TestRemoveManagedEntriesLeavesTargetValuesUntouched(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets := []workspaces.RemoveTarget{
		{Workspace: state.Workspace{Name: "alpha", SessionName: "g/alpha"}, SessionName: "g/alpha"},
		{Workspace: state.Workspace{Name: "gamma", SessionName: "g/gamma"}, SessionName: "g/gamma"},
	}

	workspaces.RemoveManagedEntries(st, targets[:1])

	if got, want := targets[1].SessionName, "g/gamma"; got != want {
		t.Fatalf("second target session changed: got %q want %q", got, want)
	}
	if got, want := targets[1].Workspace.Name, "gamma"; got != want {
		t.Fatalf("second target name changed: got %q want %q", got, want)
	}
}
