package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
	"grove/internal/state"
)

func TestResolveNewModeDefaultsToCD(t *testing.T) {
	got, err := resolveNewMode(false, false)
	if err != nil {
		t.Fatalf("resolveNewMode() error = %v", err)
	}
	if got != newModeCD {
		t.Fatalf("resolveNewMode() = %v, want %v", got, newModeCD)
	}
}

func TestResolveNewModeAllowsTmux(t *testing.T) {
	got, err := resolveNewMode(false, true)
	if err != nil {
		t.Fatalf("resolveNewMode() error = %v", err)
	}
	if got != newModeTmux {
		t.Fatalf("resolveNewMode() = %v, want %v", got, newModeTmux)
	}
}

func TestResolveNewModeRejectsConflictingFlags(t *testing.T) {
	if _, err := resolveNewMode(true, true); err == nil {
		t.Fatal("resolveNewMode() error = nil, want conflict error")
	}
}

func TestNewCommandExposesFromFlag(t *testing.T) {
	flag := newCmd.Flags().Lookup("from")
	if flag == nil {
		t.Fatal("new command missing --from flag")
	}
	if got, want := flag.Usage, "Create a new branch from this start point"; got != want {
		t.Fatalf("--from usage = %q, want %q", got, want)
	}
}

func TestValidateNewFromFlagRequiresBranch(t *testing.T) {
	if err := validateNewFromFlag("feat/base", ""); err == nil {
		t.Fatal("validateNewFromFlag() error = nil, want missing branch error")
	}
}

func TestValidateNewFromFlagAllowsBranch(t *testing.T) {
	if err := validateNewFromFlag("feat/base", "agent"); err != nil {
		t.Fatalf("validateNewFromFlag() error = %v", err)
	}
}

func TestCreateWorktreeUsesFromStartPoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	runNewTestGit(t, repoPath, "checkout", "-b", "feat/base")
	writeNewTestCommit(t, repoPath, "feature.txt", "feature")
	baseHead := newTestGitOutput(t, repoPath, "rev-parse", "HEAD")
	runNewTestGit(t, repoPath, "checkout", "main")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "mono",
		Path: repoPath,
		Type: "worktree",
	}

	if err := createWorktree(repo, "agent", "feat/base", nil, mgr, st, true, true, newModeCD); err != nil {
		t.Fatalf("createWorktree() error = %v", err)
	}

	worktreePath := filepath.Join(repoPath, ".grove", "worktrees", "agent")
	if got := newTestGitOutput(t, worktreePath, "rev-parse", "HEAD"); got != baseHead {
		t.Fatalf("worktree HEAD = %s, want %s", got, baseHead)
	}
}

func TestCreateWorktreeStoresConfiguredWorkdirAsStartPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name:    "mono",
		Path:    repoPath,
		Type:    "worktree",
		Workdir: "packages/app",
	}

	if err := createWorktree(repo, "agent", "", nil, mgr, st, true, true, newModeCD); err != nil {
		t.Fatalf("createWorktree() error = %v", err)
	}

	worktreePath := filepath.Join(repoPath, ".grove", "worktrees", "agent")
	want := filepath.Join(worktreePath, "packages/app")
	if got := st.Workspaces[0].Path; got != want {
		t.Fatalf("workspace Path = %q, want %q", got, want)
	}
}

func initNewTestRepo(t *testing.T) string {
	t.Helper()

	repoPath := t.TempDir()
	runNewTestGit(t, repoPath, "init", "-b", "main")
	runNewTestGit(t, repoPath, "config", "user.name", "Grove Test")
	runNewTestGit(t, repoPath, "config", "user.email", "grove@example.test")
	return repoPath
}

func writeNewTestCommit(t *testing.T, repoPath, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repoPath, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	runNewTestGit(t, repoPath, "add", name)
	runNewTestGit(t, repoPath, "commit", "-m", name)
}

func newTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out))
}

func runNewTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = newTestGitOutput(t, dir, args...)
}
