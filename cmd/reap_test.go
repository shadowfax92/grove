package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/workspaces"
)

func TestSelectReapTargetsRequiresAgeAndSafety(t *testing.T) {
	now := mustParseTime(t, "2026-07-10T12:00:00Z")
	root := t.TempDir()
	paths := map[string]string{}
	for _, name := range []string{"safe", "dirty", "unmerged", "active", "recent"} {
		paths[name] = filepath.Join(root, name)
		if err := os.MkdirAll(paths[name], 0755); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}
	restore := stubReapChecks(
		map[string]string{
			paths["safe"]:     "feat/safe",
			paths["dirty"]:    "feat/dirty",
			paths["unmerged"]: "feat/unmerged",
			paths["active"]:   "feat/active",
			paths["recent"]:   "feat/recent",
		},
		map[string]bool{
			paths["safe"]:     true,
			paths["dirty"]:    false,
			paths["unmerged"]: true,
			paths["active"]:   true,
			paths["recent"]:   true,
		},
		map[string]bool{
			paths["safe"]:     true,
			paths["dirty"]:    true,
			paths["unmerged"]: false,
			paths["active"]:   true,
			paths["recent"]:   true,
		},
		map[string]bool{"g/mono/feat/active": true},
	)
	defer restore()

	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		reapTestWorkspace("feat/safe", paths["safe"], "2026-07-10T04:00:00Z"),
		reapTestWorkspace("feat/dirty", paths["dirty"], "2026-07-10T04:00:00Z"),
		reapTestWorkspace("feat/unmerged", paths["unmerged"], "2026-07-10T04:00:00Z"),
		reapTestWorkspace("feat/active", paths["active"], "2026-07-10T04:00:00Z"),
		reapTestWorkspace("feat/recent", paths["recent"], "2026-07-10T10:00:00Z"),
		{Name: "notes", Type: "dir", Path: "/notes", SessionName: "g/notes", LastUsedAt: "2026-07-10T04:00:00Z"},
		{Name: "bad-time", Type: "worktree", Repo: "mono", RepoPath: "/repo", WorktreePath: paths["safe"], Branch: "feat/safe", SessionName: "g/mono/bad-time", LastUsedAt: "wat"},
	}}
	opts := reapOptions{
		TTL:    6 * time.Hour,
		Config: reapTestConfig(),
		Now:    now,
	}

	report, err := selectReapTargets(st, opts)
	if err != nil {
		t.Fatalf("selectReapTargets() error = %v", err)
	}
	if got, want := len(report.Matched), 1; got != want {
		t.Fatalf("matched count = %d, want %d: %#v", got, want, report.Matched)
	}
	if got, want := report.Matched[0].Target.Workspace.Name, "mono/feat/safe"; got != want {
		t.Fatalf("matched workspace = %q, want %q", got, want)
	}

	skips := map[string]string{}
	for _, decision := range report.Skipped {
		skips[decision.Target.Workspace.Name] = decision.SkipReason
	}
	for name, want := range map[string]string{
		"mono/feat/dirty":    "dirty worktree",
		"mono/feat/unmerged": "unmerged branch",
		"mono/feat/active":   "active tmux session",
		"mono/feat/recent":   "below ttl",
		"notes":              "not a worktree workspace",
		"bad-time":           "invalid last_used_at",
	} {
		if !strings.Contains(skips[name], want) {
			t.Fatalf("skip[%s] = %q, want containing %q", name, skips[name], want)
		}
	}
}

func TestForceReapBypassesPolicyChecks(t *testing.T) {
	now := mustParseTime(t, "2026-07-10T12:00:00Z")
	tests := []struct {
		name          string
		branch        string
		currentBranch string
		lastUsed      string
		active        bool
		clean         bool
		merged        bool
		wantSkip      string
	}{
		{"recent", "feat/recent", "feat/recent", "2026-07-10T11:00:00Z", false, true, true, "below ttl"},
		{"active", "feat/active", "feat/active", "2026-07-10T04:00:00Z", true, true, true, "active tmux session"},
		{"unverified branch", "feat/unverified", "", "2026-07-10T04:00:00Z", false, true, true, "could not verify current branch"},
		{"branch mismatch", "feat/expected", "feat/actual", "2026-07-10T04:00:00Z", false, true, true, "branch mismatch"},
		{"dirty", "feat/dirty", "feat/dirty", "2026-07-10T04:00:00Z", false, false, true, "dirty worktree"},
		{"default branch", "main", "main", "2026-07-10T04:00:00Z", false, true, true, "default branch workspace"},
		{"unmerged", "feat/unmerged", "feat/unmerged", "2026-07-10T04:00:00Z", false, true, false, "unmerged branch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir()
			ws := reapTestWorkspace(tt.branch, path, tt.lastUsed)
			restore := stubReapChecks(
				map[string]string{path: tt.currentBranch},
				map[string]bool{path: tt.clean},
				map[string]bool{path: tt.merged},
				map[string]bool{ws.SessionName: tt.active},
			)
			defer restore()

			opts := reapOptions{TTL: 6 * time.Hour, Config: reapTestConfig(), Now: now}
			safe := evaluateReapWorkspace(ws, opts)
			if !strings.Contains(safe.SkipReason, tt.wantSkip) {
				t.Fatalf("safe skip = %q, want containing %q", safe.SkipReason, tt.wantSkip)
			}

			opts.Force = true
			forced := evaluateReapWorkspace(ws, opts)
			if forced.SkipReason != "" || forced.Reason != "forced; safety checks bypassed" {
				t.Fatalf("forced decision = %#v, want selected", forced)
			}
		})
	}
}

func TestForceReapKeepsStructuralGuards(t *testing.T) {
	worktreePath := t.TempDir()
	repoPath := t.TempDir()
	filePath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	valid := reapTestWorkspace("feat/valid", worktreePath, "")
	forged := valid
	forged.RepoPath = initRepoForReap(t)
	tests := []struct {
		name          string
		ws            state.Workspace
		listWorktrees func(string) ([]git.WorktreeInfo, error)
		wantSkip      string
	}{
		{"non-worktree", state.Workspace{Name: "notes", Type: "dir"}, nil, "not a worktree workspace"},
		{"missing repo path", func() state.Workspace { ws := valid; ws.RepoPath = ""; return ws }(), nil, "missing worktree metadata"},
		{"missing worktree path", func() state.Workspace { ws := valid; ws.WorktreePath = ""; return ws }(), nil, "missing worktree metadata"},
		{"missing session name", func() state.Workspace { ws := valid; ws.SessionName = ""; return ws }(), nil, "missing worktree metadata"},
		{"base repo", func() state.Workspace {
			ws := valid
			ws.RepoPath = repoPath
			ws.WorktreePath = repoPath + string(os.PathSeparator) + "."
			return ws
		}(), nil, "missing worktree metadata"},
		{"missing directory", func() state.Workspace {
			ws := valid
			ws.WorktreePath = filepath.Join(t.TempDir(), "missing")
			return ws
		}(), nil, "worktree path is missing"},
		{"non-directory", func() state.Workspace { ws := valid; ws.WorktreePath = filePath; return ws }(), nil, "worktree path is not a directory"},
		{"ordinary directory", valid, func(string) ([]git.WorktreeInfo, error) { return nil, nil }, "path is not a registered worktree"},
		{"forged registration", forged, func(string) ([]git.WorktreeInfo, error) {
			return []git.WorktreeInfo{{Path: worktreePath}}, nil
		}, "could not verify worktree identity"},
		{"unusable repo", func() state.Workspace { ws := valid; ws.RepoPath = t.TempDir(); return ws }(), nil, "could not verify registered worktree"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origList := reapListWorktrees
			if tt.listWorktrees != nil {
				reapListWorktrees = tt.listWorktrees
			}
			defer func() { reapListWorktrees = origList }()

			got := evaluateReapWorkspace(tt.ws, reapOptions{Force: true})
			if !strings.Contains(got.SkipReason, tt.wantSkip) {
				t.Fatalf("skip = %q, want containing %q", got.SkipReason, tt.wantSkip)
			}
		})
	}
}

func TestForceReapRejectsBaseRepoResolvedFromMetadataPath(t *testing.T) {
	repoPath := initRepoForReap(t)
	subdir := filepath.Join(repoPath, "nested")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(subdir, symlinkPath); err != nil {
		t.Fatal(err)
	}
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runGitForReap(t, repoPath, "worktree", "add", "-b", "feat/metadata", linkedPath)

	for _, metadataPath := range []string{subdir, symlinkPath, linkedPath} {
		ws := reapTestWorkspace("main", repoPath, "")
		ws.RepoPath = metadataPath
		got := evaluateReapWorkspace(ws, reapOptions{Force: true})
		if got.SkipReason != "worktree path is base repository" {
			t.Fatalf("RepoPath %q skip = %q, want base repository", metadataPath, got.SkipReason)
		}
	}
}

func TestForceReapRejectsPrunableWorktreeReplacement(t *testing.T) {
	repoPath := initRepoForReap(t)
	parent := t.TempDir()
	worktreePath := filepath.Join(parent, "stale")
	runGitForReap(t, repoPath, "worktree", "add", "-b", "feat/stale", worktreePath)
	if err := os.Rename(worktreePath, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(worktreePath, 0755); err != nil {
		t.Fatal(err)
	}
	writeFileForReap(t, filepath.Join(worktreePath, "unrelated.txt"), "keep\n")

	ws := reapTestWorkspace("feat/stale", worktreePath, "")
	ws.RepoPath = repoPath
	got := evaluateReapWorkspace(ws, reapOptions{Force: true})
	if got.SkipReason != "path is not a registered worktree" {
		t.Fatalf("skip = %q, want prunable worktree rejected", got.SkipReason)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "unrelated.txt")); err != nil {
		t.Fatalf("replacement data was touched: %v", err)
	}
}

func TestRunForceReapRevalidatesAfterSessionCleanup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := initRepoForReap(t)
	parent := t.TempDir()
	worktreePath := filepath.Join(parent, "worktree")
	movedPath := filepath.Join(parent, "moved")
	runGitForReap(t, repoPath, "worktree", "add", "-b", "feat/live", worktreePath)

	ws := reapTestWorkspace("feat/live", worktreePath, "")
	ws.RepoPath = repoPath
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Save(&state.State{Version: 1, Workspaces: []state.Workspace{ws}}); err != nil {
		t.Fatal(err)
	}

	origKill := reapKillTmuxSession
	origRemove := reapRemoveWorktree
	removeCalled := false
	reapKillTmuxSession = func(sessionName string) error {
		if sessionName != ws.SessionName {
			t.Fatalf("session cleanup = %q, want %q", sessionName, ws.SessionName)
		}
		if err := os.Rename(worktreePath, movedPath); err != nil {
			return err
		}
		if err := os.Mkdir(worktreePath, 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(worktreePath, "unrelated.txt"), []byte("keep\n"), 0644)
	}
	reapRemoveWorktree = func(workspaces.RemoveTarget) error {
		removeCalled = true
		return nil
	}
	defer func() {
		reapKillTmuxSession = origKill
		reapRemoveWorktree = origRemove
	}()

	_, err = runReap(reapOptions{Force: true, Jobs: 1}, io.Discard, io.Discard)
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("runReap() error = %v, want ErrRemoveFailed", err)
	}
	if removeCalled {
		t.Fatal("worktree removal ran after session cleanup replaced the target path")
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "unrelated.txt")); err != nil {
		t.Fatalf("replacement data was touched: %v", err)
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Workspaces, []state.Workspace{ws}) {
		t.Fatalf("state after revalidation failure = %#v, want workspace restored", loaded.Workspaces)
	}
}

func TestPrintDryRunReapReportIncludesSelectedAndSkippedReasons(t *testing.T) {
	report := reapReport{
		Matched: []reapDecision{{
			Target: workspaces.RemoveTarget{Workspace: state.Workspace{Name: "mono/feat/safe"}},
			Reason: "idle 7h, clean, merged into main",
		}},
		Skipped: []reapDecision{{
			Target:     workspaces.RemoveTarget{Workspace: state.Workspace{Name: "mono/feat/dirty"}},
			SkipReason: "dirty worktree",
		}},
	}
	var out bytes.Buffer

	printReapReport(&out, report, reapOptions{DryRun: true, TTL: 6 * time.Hour})

	got := out.String()
	for _, want := range []string{
		"Would reap 1 workspaces (ttl 6h):",
		"mono/feat/safe",
		"idle 7h, clean, merged into main",
		"Skipped 1 workspaces:",
		"mono/feat/dirty",
		"dirty worktree",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry-run report missing %q:\n%s", want, got)
		}
	}
}

func TestRunForceReapRestoresStateWhenRemovalFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := mustParseTime(t, "2026-07-10T12:00:00Z")
	root := t.TempDir()
	worktreePath := filepath.Join(root, "safe")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	origRemove := reapRemoveWorktree
	origKill := reapKillTmuxSession
	restoreList := stubReapWorktrees(worktreePath)
	reapRemoveWorktree = func(workspaces.RemoveTarget) error { return fmt.Errorf("boom") }
	reapKillTmuxSession = func(string) error { return nil }
	defer func() {
		reapRemoveWorktree = origRemove
		reapKillTmuxSession = origKill
		restoreList()
	}()

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	eligible := reapTestWorkspace("feat/safe", worktreePath, "2026-07-10T04:00:00Z")
	recent := reapTestWorkspace("feat/recent", filepath.Join(root, "recent"), "2026-07-10T10:00:00Z")
	st := &state.State{Version: 1, Workspaces: []state.Workspace{eligible, recent}}
	original := append([]state.Workspace(nil), st.Workspaces...)
	if err := mgr.Save(st); err != nil {
		t.Fatalf("mgr.Save() error = %v", err)
	}

	_, err = runReap(reapOptions{Force: true, TTL: 6 * time.Hour, Config: reapTestConfig(), Now: now}, io.Discard, io.Discard)
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("runReap() error = %v, want ErrRemoveFailed", err)
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatalf("mgr.Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Workspaces, original) {
		t.Fatalf("restored workspaces = %#v, want %#v", loaded.Workspaces, original)
	}
}

func TestRunForceReapDryRunPreviewsWithoutMutation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	worktreePath := t.TempDir()
	ws := reapTestWorkspace("feat/dirty", worktreePath, "invalid")
	st := &state.State{Version: 1, Workspaces: []state.Workspace{ws}}
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Save(st); err != nil {
		t.Fatal(err)
	}
	restoreList := stubReapWorktrees(worktreePath)
	defer restoreList()

	report, err := runReap(reapOptions{DryRun: true, Force: true}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("runReap() error = %v", err)
	}
	if len(report.Matched) != 1 {
		t.Fatalf("matched = %#v, want forced workspace", report.Matched)
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Workspaces, st.Workspaces) {
		t.Fatalf("dry-run mutated state: %#v", loaded.Workspaces)
	}

	var out bytes.Buffer
	printReapReport(&out, report, reapOptions{DryRun: true, Force: true})
	if !strings.Contains(out.String(), "Would force reap 1 workspaces:") {
		t.Fatalf("forced dry-run report = %q", out.String())
	}
}

func TestRunForceReapCleansLiveSessionByExactName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_TEST_LOG\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_LOG", logPath)

	worktreePath := t.TempDir()
	ws := reapTestWorkspace("feat/live", worktreePath, "")
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Save(&state.State{Version: 1, Workspaces: []state.Workspace{ws}}); err != nil {
		t.Fatal(err)
	}
	origRemove := reapRemoveWorktree
	restoreList := stubReapWorktrees(worktreePath)
	reapRemoveWorktree = func(workspaces.RemoveTarget) error { return nil }
	defer func() {
		reapRemoveWorktree = origRemove
		restoreList()
	}()

	if _, err := runReap(reapOptions{Force: true, Jobs: 1}, io.Discard, io.Discard); err != nil {
		t.Fatalf("runReap() error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"has-session -t =g/mono/feat/live",
		"kill-session -t =g/mono/feat/live",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux calls = %q, want %q", got, want)
	}
}

func TestGitReapSafetyChecksCleanAndMerged(t *testing.T) {
	repoPath := t.TempDir()
	runGitForReap(t, repoPath, "init", "-b", "main")
	runGitForReap(t, repoPath, "config", "user.email", "test@example.com")
	runGitForReap(t, repoPath, "config", "user.name", "Test User")
	writeFileForReap(t, filepath.Join(repoPath, "README.md"), "base\n")
	runGitForReap(t, repoPath, "add", "README.md")
	runGitForReap(t, repoPath, "commit", "-m", "base")

	worktreePath := filepath.Join(t.TempDir(), "feature")
	runGitForReap(t, repoPath, "worktree", "add", "-b", "feat/reap", worktreePath)
	writeFileForReap(t, filepath.Join(worktreePath, "feature.txt"), "work\n")
	runGitForReap(t, worktreePath, "add", "feature.txt")
	runGitForReap(t, worktreePath, "commit", "-m", "feature")

	clean, err := gitWorktreeClean(worktreePath)
	if err != nil {
		t.Fatalf("gitWorktreeClean() error = %v", err)
	}
	if !clean {
		t.Fatal("gitWorktreeClean() = false, want true after commit")
	}
	merged, err := gitMergedIntoDefault(repoPath, worktreePath, "main")
	if err != nil {
		t.Fatalf("gitMergedIntoDefault() error = %v", err)
	}
	if merged {
		t.Fatal("gitMergedIntoDefault() = true, want false before merge")
	}

	runGitForReap(t, repoPath, "merge", "--no-ff", "--no-edit", "feat/reap")
	merged, err = gitMergedIntoDefault(repoPath, worktreePath, "main")
	if err != nil {
		t.Fatalf("gitMergedIntoDefault() after merge error = %v", err)
	}
	if !merged {
		t.Fatal("gitMergedIntoDefault() = false, want true after merge")
	}

	writeFileForReap(t, filepath.Join(worktreePath, "untracked.txt"), "dirty\n")
	clean, err = gitWorktreeClean(worktreePath)
	if err != nil {
		t.Fatalf("gitWorktreeClean() dirty error = %v", err)
	}
	if clean {
		t.Fatal("gitWorktreeClean() = true, want false with untracked file")
	}
}

func TestGitMergedIntoDefaultRequiresOriginWhenPresent(t *testing.T) {
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	runGitForReap(t, t.TempDir(), "init", "--bare", remotePath)

	repoPath := t.TempDir()
	runGitForReap(t, repoPath, "init", "-b", "main")
	runGitForReap(t, repoPath, "config", "user.email", "test@example.com")
	runGitForReap(t, repoPath, "config", "user.name", "Test User")
	writeFileForReap(t, filepath.Join(repoPath, "README.md"), "base\n")
	runGitForReap(t, repoPath, "add", "README.md")
	runGitForReap(t, repoPath, "commit", "-m", "base")
	runGitForReap(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForReap(t, repoPath, "push", "-u", "origin", "main")
	runGitForReap(t, repoPath, "remote", "set-head", "origin", "main")

	worktreePath := filepath.Join(t.TempDir(), "feature")
	runGitForReap(t, repoPath, "worktree", "add", "-b", "feat/reap", worktreePath)
	writeFileForReap(t, filepath.Join(worktreePath, "feature.txt"), "work\n")
	runGitForReap(t, worktreePath, "add", "feature.txt")
	runGitForReap(t, worktreePath, "commit", "-m", "feature")
	runGitForReap(t, repoPath, "merge", "--no-ff", "--no-edit", "feat/reap")

	merged, err := gitMergedIntoDefault(repoPath, worktreePath, "main")
	if err != nil {
		t.Fatalf("gitMergedIntoDefault() error = %v", err)
	}
	if merged {
		t.Fatal("gitMergedIntoDefault() = true, want false until origin/main contains the work")
	}

	runGitForReap(t, repoPath, "push", "origin", "main")
	merged, err = gitMergedIntoDefault(repoPath, worktreePath, "main")
	if err != nil {
		t.Fatalf("gitMergedIntoDefault() after push error = %v", err)
	}
	if !merged {
		t.Fatal("gitMergedIntoDefault() = false, want true after origin/main contains the work")
	}
}

func TestReapHelpDocumentsDryRunAndSafety(t *testing.T) {
	flag := reapCmd.Flags().Lookup("force")
	if flag == nil || flag.Shorthand != "f" {
		t.Fatalf("force flag = %#v, want -f shorthand", flag)
	}
	defer func() {
		_ = flag.Value.Set("false")
		flag.Changed = false
	}()
	for _, arg := range []string{"--force", "-f"} {
		_ = flag.Value.Set("false")
		flag.Changed = false
		if err := reapCmd.ParseFlags([]string{arg}); err != nil {
			t.Fatalf("parsing %s: %v", arg, err)
		}
		force, _ := reapCmd.Flags().GetBool("force")
		if !force {
			t.Fatalf("%s did not enable force", arg)
		}
	}
	if jobs := reapCmd.Flags().Lookup("jobs"); jobs == nil || jobs.Shorthand != "j" {
		t.Fatalf("jobs flag = %#v, want -j shorthand", jobs)
	}
	for _, want := range []string{"--dry-run", "--force", "--ttl", "--jobs", "dirty", "unmerged", "active", "discard"} {
		if !strings.Contains(reapCmd.Long+reapCmd.Flags().FlagUsages(), want) {
			t.Fatalf("reap help missing %q", want)
		}
	}
}

func reapTestWorkspace(branch, path, lastUsed string) state.Workspace {
	return state.Workspace{
		Name:         "mono/" + branch,
		Type:         "worktree",
		Repo:         "mono",
		RepoPath:     "/repo",
		WorktreePath: path,
		Branch:       branch,
		SessionName:  "g/mono/" + branch,
		CreatedAt:    "2026-07-10T04:00:00Z",
		LastUsedAt:   lastUsed,
	}
}

func reapTestConfig() *config.Config {
	return &config.Config{Repos: []config.RepoConfig{{
		Name:          "mono",
		Path:          "/repo",
		DefaultBranch: "main",
	}}}
}

func stubReapChecks(branches map[string]string, clean map[string]bool, merged map[string]bool, active map[string]bool) func() {
	origBranch := reapCurrentBranch
	origClean := reapWorktreeClean
	origMerged := reapMergedIntoDefault
	origList := reapListWorktrees
	origValidate := reapValidateWorktree
	origActive := reapTmuxSessionActive

	reapCurrentBranch = func(path string) string { return branches[path] }
	reapWorktreeClean = func(path string) (bool, error) { return clean[path], nil }
	reapMergedIntoDefault = func(_, worktreePath, _ string) (bool, error) { return merged[worktreePath], nil }
	reapListWorktrees = func(string) ([]git.WorktreeInfo, error) {
		registered := make([]git.WorktreeInfo, 0, len(branches))
		for path := range branches {
			registered = append(registered, git.WorktreeInfo{Path: path})
		}
		return registered, nil
	}
	reapValidateWorktree = func(string, string) error { return nil }
	reapTmuxSessionActive = func(sessionName string) (bool, error) { return active[sessionName], nil }

	return func() {
		reapCurrentBranch = origBranch
		reapWorktreeClean = origClean
		reapMergedIntoDefault = origMerged
		reapListWorktrees = origList
		reapValidateWorktree = origValidate
		reapTmuxSessionActive = origActive
	}
}

func stubReapWorktrees(paths ...string) func() {
	orig := reapListWorktrees
	origValidate := reapValidateWorktree
	reapListWorktrees = func(string) ([]git.WorktreeInfo, error) {
		registered := make([]git.WorktreeInfo, 0, len(paths))
		for _, path := range paths {
			registered = append(registered, git.WorktreeInfo{Path: path})
		}
		return registered, nil
	}
	reapValidateWorktree = func(string, string) error { return nil }
	return func() {
		reapListWorktrees = orig
		reapValidateWorktree = origValidate
	}
}

func initRepoForReap(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()
	runGitForReap(t, repoPath, "init", "-b", "main")
	runGitForReap(t, repoPath, "config", "user.email", "test@example.com")
	runGitForReap(t, repoPath, "config", "user.name", "Test User")
	writeFileForReap(t, filepath.Join(repoPath, "README.md"), "base\n")
	runGitForReap(t, repoPath, "add", "README.md")
	runGitForReap(t, repoPath, "commit", "-m", "base")
	return repoPath
}

func mustParseTime(t *testing.T, raw string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse time %q: %v", raw, err)
	}
	return ts
}

func runGitForReap(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s (%v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
}

func writeFileForReap(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
