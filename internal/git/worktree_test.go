package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddWorktreeFromCreatesNewBranchAtStartPoint(t *testing.T) {
	repoPath := initTestRepo(t)
	writeCommit(t, repoPath, "base.txt", "base")
	runGit(t, repoPath, "checkout", "-b", "feat/base")
	writeCommit(t, repoPath, "feature.txt", "feature")
	baseHead := gitOutput(t, repoPath, "rev-parse", "HEAD")
	runGit(t, repoPath, "checkout", "main")

	worktreePath := filepath.Join(t.TempDir(), "agent")
	if err := AddWorktreeFrom(repoPath, worktreePath, "agent", "feat/base"); err != nil {
		t.Fatalf("AddWorktreeFrom() error = %v", err)
	}

	if got := gitOutput(t, worktreePath, "rev-parse", "HEAD"); got != baseHead {
		t.Fatalf("worktree HEAD = %s, want %s", got, baseHead)
	}
	if got := gitOutput(t, worktreePath, "branch", "--show-current"); got != "agent" {
		t.Fatalf("worktree branch = %q, want %q", got, "agent")
	}
}

func TestAddWorktreeFromRejectsExistingBranch(t *testing.T) {
	repoPath := initTestRepo(t)
	writeCommit(t, repoPath, "base.txt", "base")
	runGit(t, repoPath, "checkout", "-b", "feat/base")
	writeCommit(t, repoPath, "feature.txt", "feature")
	runGit(t, repoPath, "checkout", "main")
	runGit(t, repoPath, "branch", "agent")

	err := AddWorktreeFrom(repoPath, filepath.Join(t.TempDir(), "agent"), "agent", "feat/base")
	if err == nil {
		t.Fatal("AddWorktreeFrom() error = nil, want existing branch error")
	}
	if !strings.Contains(err.Error(), "can only be used when creating a new branch") {
		t.Fatalf("AddWorktreeFrom() error = %q, want --from existing branch error", err)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()

	repoPath := t.TempDir()
	runGit(t, repoPath, "init", "-b", "main")
	runGit(t, repoPath, "config", "user.name", "Grove Test")
	runGit(t, repoPath, "config", "user.email", "grove@example.test")
	return repoPath
}

func writeCommit(t *testing.T, repoPath, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repoPath, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	runGit(t, repoPath, "add", name)
	runGit(t, repoPath, "commit", "-m", name)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = gitOutput(t, dir, args...)
}
