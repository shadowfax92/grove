package cmd

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

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

func TestRemoveWorktreesCapsConcurrencyAndCollectsFailures(t *testing.T) {
	const n = 20
	targets := make([]workspaces.RemoveTarget, n)
	for i := range targets {
		// All in one repo on purpose: the cap must hold regardless of repo, and
		// concurrent same-repo removals are the case we now rely on being safe.
		targets[i] = workspaces.RemoveTarget{Workspace: state.Workspace{
			Name:         fmt.Sprintf("ws-%02d", i),
			RepoPath:     "/repo",
			WorktreePath: fmt.Sprintf("/repo/.grove/worktrees/ws-%02d", i),
		}}
	}

	var active, maxActive, calls int64
	remove := func(target workspaces.RemoveTarget) error {
		cur := atomic.AddInt64(&active, 1)
		for { // record high-water mark of concurrent removals
			old := atomic.LoadInt64(&maxActive)
			if cur <= old || atomic.CompareAndSwapInt64(&maxActive, old, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&calls, 1)
		atomic.AddInt64(&active, -1)
		if target.Workspace.Name == "ws-03" || target.Workspace.Name == "ws-11" {
			return fmt.Errorf("boom")
		}
		return nil
	}

	defer silenceStdio(t)()
	failed := removeWorktrees(targets, defaultRemoveJobs, remove)

	if got := atomic.LoadInt64(&calls); got != n {
		t.Fatalf("remove called %d times, want %d", got, n)
	}
	if got := atomic.LoadInt64(&maxActive); got > defaultRemoveJobs {
		t.Fatalf("peak concurrency = %d, want <= %d", got, defaultRemoveJobs)
	}
	if got := atomic.LoadInt64(&maxActive); got < 2 {
		t.Fatalf("peak concurrency = %d, expected parallel execution", got)
	}
	if len(failed) != 2 {
		t.Fatalf("failed = %+v, want 2 entries", failed)
	}
	names := map[string]bool{}
	for _, ws := range failed {
		names[ws.Name] = true
	}
	if !names["ws-03"] || !names["ws-11"] {
		t.Fatalf("failed names = %v, want ws-03 and ws-11", names)
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
