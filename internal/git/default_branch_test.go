package git

import (
	"path/filepath"
	"testing"
)

func TestDefaultBranchPrefersOriginHead(t *testing.T) {
	repoPath := initTestRepo(t)
	writeCommit(t, repoPath, "base.txt", "base")

	originPath := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, repoPath, "init", "--bare", originPath)
	runGit(t, repoPath, "remote", "add", "origin", originPath)
	runGit(t, repoPath, "push", "-u", "origin", "main")
	runGit(t, repoPath, "remote", "set-head", "origin", "main")
	runGit(t, repoPath, "checkout", "-b", "feature")

	if got, want := DefaultBranch(repoPath), "main"; got != want {
		t.Fatalf("DefaultBranch() = %q, want %q", got, want)
	}
}

func TestDefaultBranchFallsBackToLocalMain(t *testing.T) {
	repoPath := initTestRepo(t)
	writeCommit(t, repoPath, "base.txt", "base")
	runGit(t, repoPath, "checkout", "-b", "feature")

	if got, want := DefaultBranch(repoPath), "main"; got != want {
		t.Fatalf("DefaultBranch() = %q, want %q", got, want)
	}
}

func TestDefaultBranchFallsBackToCurrentBranch(t *testing.T) {
	repoPath := initTestRepo(t)
	writeCommit(t, repoPath, "base.txt", "base")
	runGit(t, repoPath, "branch", "-m", "trunk")

	if got, want := DefaultBranch(repoPath), "trunk"; got != want {
		t.Fatalf("DefaultBranch() = %q, want %q", got, want)
	}
}
