package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"grove/internal/state"
	"grove/internal/workspaces"
)

func TestRunRemovePathRemovesMatchingWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	worktreePath := "/tmp/grove/worktrees/mono/feat-json"
	st := &state.State{Version: 1, Workspaces: []state.Workspace{{
		Name:         "mono/feat-json",
		Type:         "worktree",
		Repo:         "mono",
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		SessionName:  "g/mono/feat/json",
	}}}
	if err := mgr.Save(st); err != nil {
		t.Fatalf("mgr.Save() error = %v", err)
	}

	calls := 0
	err = runRemovePath(mgr, st, worktreePath, 1, func(target workspaces.RemoveTarget) error {
		calls++
		if got := target.Workspace.WorktreePath; got != worktreePath {
			t.Fatalf("target WorktreePath = %q, want %q", got, worktreePath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runRemovePath() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("remove calls = %d, want 1", calls)
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatalf("mgr.Load() error = %v", err)
	}
	if len(loaded.Workspaces) != 0 {
		t.Fatalf("workspace count after remove = %d, want 0", len(loaded.Workspaces))
	}
}

func TestRunRemovePathReturnsNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	if err := runRemovePath(mgr, st, "/missing", 1, removeWorktreeForTarget); !errors.Is(err, ErrRemovePathNotFound) {
		t.Fatalf("runRemovePath() error = %v, want ErrRemovePathNotFound", err)
	}
}

func TestRunRemovePathRestoresStateOnRemovalFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	worktreePath := "/tmp/grove/worktrees/mono/feat-json"
	st := &state.State{Version: 1, Workspaces: []state.Workspace{{
		Name:        "mono/other",
		Type:        "worktree",
		Repo:        "mono",
		RepoPath:    "/repo",
		SessionName: "g/mono/other",
		CreatedAt:   "2026-07-01T18:00:00Z",
	}, {
		Name:         "mono/feat-json",
		Type:         "worktree",
		Repo:         "mono",
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		SessionName:  "g/mono/feat/json",
		CreatedAt:    "2026-07-01T18:06:00Z",
	}, {
		Name:        "mono/later",
		Type:        "worktree",
		Repo:        "mono",
		RepoPath:    "/repo",
		SessionName: "g/mono/later",
		CreatedAt:   "2026-07-01T18:10:00Z",
	}}}
	original := append([]state.Workspace(nil), st.Workspaces...)
	if err := mgr.Save(st); err != nil {
		t.Fatalf("mgr.Save() error = %v", err)
	}

	err = runRemovePath(mgr, st, worktreePath, 1, func(workspaces.RemoveTarget) error {
		return fmt.Errorf("boom")
	})
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("runRemovePath() error = %v, want ErrRemoveFailed", err)
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatalf("mgr.Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Workspaces, original) {
		t.Fatalf("restored workspaces = %#v, want %#v", loaded.Workspaces, original)
	}
}

func TestRemoveSelectedTargetsRestoresOnlyFailedTargets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	alpha := state.Workspace{Name: "alpha", SessionName: "g/alpha", WorktreePath: "/worktrees/alpha"}
	beta := state.Workspace{Name: "beta", SessionName: "g/beta", WorktreePath: "/worktrees/beta"}
	gamma := state.Workspace{Name: "gamma", SessionName: "g/gamma", WorktreePath: "/worktrees/gamma"}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{alpha, beta, gamma}}
	if err := mgr.Save(st); err != nil {
		t.Fatalf("mgr.Save() error = %v", err)
	}

	targets := []workspaces.RemoveTarget{
		{Workspace: alpha, SessionName: alpha.SessionName},
		{Workspace: beta, SessionName: beta.SessionName},
	}
	failed, err := removeSelectedTargets(mgr, st, targets, 1, func(target workspaces.RemoveTarget) error {
		if target.SessionName == beta.SessionName {
			return fmt.Errorf("boom")
		}
		return nil
	}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("removeSelectedTargets() error = %v", err)
	}
	if len(failed) != 1 || failed[0].SessionName != beta.SessionName {
		t.Fatalf("failed = %#v, want beta only", failed)
	}

	loaded, err := mgr.Load()
	if err != nil {
		t.Fatalf("mgr.Load() error = %v", err)
	}
	want := []state.Workspace{beta, gamma}
	if !reflect.DeepEqual(loaded.Workspaces, want) {
		t.Fatalf("workspaces after mixed removal = %#v, want %#v", loaded.Workspaces, want)
	}
}

func TestValidateRemovePathModeRequiresYesAndNoArgs(t *testing.T) {
	if err := validateRemovePathMode("/worktree", false, nil); err == nil {
		t.Fatal("validateRemovePathMode() error = nil, want missing --yes error")
	}
	if err := validateRemovePathMode("/worktree", true, []string{"mono/feat"}); err == nil {
		t.Fatal("validateRemovePathMode() error = nil, want args conflict")
	}
	if err := validateRemovePathMode("/worktree", true, nil); err != nil {
		t.Fatalf("validateRemovePathMode() error = %v", err)
	}
}

func TestRemovePathRejectsForceWithoutYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	worktreePath := "/tmp/grove/worktrees/mono/feat-json"
	st := &state.State{Version: 1, Workspaces: []state.Workspace{{
		Name:         "mono/feat-json",
		Type:         "worktree",
		Repo:         "mono",
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		SessionName:  "g/mono/feat/json",
	}}}
	if err := mgr.Save(st); err != nil {
		t.Fatalf("mgr.Save() error = %v", err)
	}

	resetRemoveFlags := func() {
		_ = rmCmd.Flags().Set("path", "")
		_ = rmCmd.Flags().Set("force", "false")
		_ = rmCmd.Flags().Set("yes", "false")
		_ = rmCmd.Flags().Set("jobs", "8")
	}
	resetRemoveFlags()
	defer resetRemoveFlags()
	if err := rmCmd.Flags().Set("path", worktreePath); err != nil {
		t.Fatalf("set path flag: %v", err)
	}
	if err := rmCmd.Flags().Set("force", "true"); err != nil {
		t.Fatalf("set force flag: %v", err)
	}

	if err := rmCmd.RunE(rmCmd, nil); err == nil {
		t.Fatal("rmCmd.RunE() error = nil, want --yes validation error")
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatalf("mgr.Load() error = %v", err)
	}
	if len(loaded.Workspaces) != 1 {
		t.Fatalf("workspace count after rejected remove = %d, want 1", len(loaded.Workspaces))
	}
}

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
