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

func TestRunReapRestoresStateWhenRemovalFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := mustParseTime(t, "2026-07-10T12:00:00Z")
	root := t.TempDir()
	worktreePath := filepath.Join(root, "safe")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	restore := stubReapChecks(
		map[string]string{worktreePath: "feat/safe"},
		map[string]bool{worktreePath: true},
		map[string]bool{worktreePath: true},
		nil,
	)
	defer restore()
	origRemove := reapRemoveWorktree
	reapRemoveWorktree = func(workspaces.RemoveTarget) error { return fmt.Errorf("boom") }
	defer func() { reapRemoveWorktree = origRemove }()

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

	_, err = runReap(reapOptions{TTL: 6 * time.Hour, Config: reapTestConfig(), Now: now}, io.Discard, io.Discard)
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
	for _, want := range []string{"--dry-run", "--ttl", "dirty", "unmerged", "active"} {
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
	origActive := reapTmuxSessionActive

	reapCurrentBranch = func(path string) string { return branches[path] }
	reapWorktreeClean = func(path string) (bool, error) { return clean[path], nil }
	reapMergedIntoDefault = func(_, worktreePath, _ string) (bool, error) { return merged[worktreePath], nil }
	reapTmuxSessionActive = func(sessionName string) (bool, error) { return active[sessionName], nil }

	return func() {
		reapCurrentBranch = origBranch
		reapWorktreeClean = origClean
		reapMergedIntoDefault = origMerged
		reapTmuxSessionActive = origActive
	}
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
