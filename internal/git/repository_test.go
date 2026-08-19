package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRepositoryFromLinkedWorktreeUsesMainWorktree(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainPath, "worktree", "add", "-b", "feat/linked", linkedPath)

	repo, err := OpenRepository(linkedPath)
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	if got, want := repo.MainPath, canonicalTestPath(t, mainPath); got != want {
		t.Fatalf("MainPath = %q, want %q", got, want)
	}
	if got, want := repo.CommonDir, canonicalTestPath(t, filepath.Join(mainPath, ".git")); got != want {
		t.Fatalf("CommonDir = %q, want %q", got, want)
	}

	worktrees, err := repo.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees() error = %v", err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("len(Worktrees()) = %d, want 2", len(worktrees))
	}
	if !worktrees[0].Main {
		t.Fatalf("first worktree Main = false, want true: %#v", worktrees[0])
	}
}

func TestListWorktreesPreservesUnusualPathAndLockReason(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	linkedPath := filepath.Join(t.TempDir(), "line\nbreak")
	runGit(t, mainPath, "worktree", "add", "-b", "feat/unusual", linkedPath)
	runGit(t, mainPath, "worktree", "lock", "--reason", "agent owns this", linkedPath)

	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	worktrees, err := repo.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees() error = %v", err)
	}
	var got *WorktreeInfo
	for i := range worktrees {
		if worktrees[i].Branch == "feat/unusual" {
			got = &worktrees[i]
		}
	}
	if got == nil {
		t.Fatalf("feat/unusual missing from %#v", worktrees)
	}
	if want := canonicalTestPath(t, linkedPath); got.Path != want {
		t.Fatalf("Path = %q, want %q", got.Path, want)
	}
	if !got.Locked || got.LockReason != "agent owns this" {
		t.Fatalf("lock = %v %q, want true with reason", got.Locked, got.LockReason)
	}
}

func TestEnsureManagedRootUsesSharedExclude(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}

	root, err := repo.EnsureManagedRoot()
	if err != nil {
		t.Fatalf("EnsureManagedRoot() error = %v", err)
	}
	if got, want := root, filepath.Join(canonicalTestPath(t, mainPath), ".wt"); got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
	data, err := os.ReadFile(filepath.Join(mainPath, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("reading exclude: %v", err)
	}
	if !strings.Contains(string(data), "/.wt/\n") {
		t.Fatalf("exclude = %q, want /.wt/", data)
	}
	if got := gitOutput(t, mainPath, "status", "--porcelain"); got != "" {
		t.Fatalf("status = %q, want clean", got)
	}
}

func TestEnsureManagedRootRejectsTrackedPath(t *testing.T) {
	mainPath := initTestRepo(t)
	if err := os.MkdirAll(filepath.Join(mainPath, ".wt"), 0755); err != nil {
		t.Fatal(err)
	}
	writeCommit(t, mainPath, filepath.Join(".wt", "README"), "tracked")
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.EnsureManagedRoot()
	if err == nil || !strings.Contains(err.Error(), "tracked") {
		t.Fatalf("EnsureManagedRoot() error = %v, want tracked-path error", err)
	}
}

func TestEnsureManagedRootRejectsSymlink(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	if err := os.Symlink(t.TempDir(), filepath.Join(mainPath, ".wt")); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.EnsureManagedRoot()
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("EnsureManagedRoot() error = %v, want symlink error", err)
	}
}

func TestManagedPathPreservesBranchHierarchy(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.ManagedPath("feat/auth/session")
	if err != nil {
		t.Fatalf("ManagedPath() error = %v", err)
	}
	if want := filepath.Join(canonicalTestPath(t, mainPath), ".wt", "feat", "auth", "session"); got != want {
		t.Fatalf("ManagedPath() = %q, want %q", got, want)
	}
	for _, branch := range []string{"../escape", "/absolute", "bad:branch", "-flag"} {
		if _, err := repo.ManagedPath(branch); err == nil {
			t.Errorf("ManagedPath(%q) error = nil, want validation error", branch)
		}
	}
}

func TestRemoveWorktreeRequiresDiscardForDirtyTree(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainPath, "worktree", "add", "-b", "feat/dirty", linkedPath)
	if err := os.WriteFile(filepath.Join(linkedPath, "untracked.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.RemoveWorktree(linkedPath, false); err == nil {
		t.Fatal("RemoveWorktree(discard=false) error = nil, want dirty-tree refusal")
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("dirty worktree was removed: %v", err)
	}
	if err := repo.RemoveWorktree(linkedPath, true); err != nil {
		t.Fatalf("RemoveWorktree(discard=true) error = %v", err)
	}
}

func TestRemoveWorktreeDoesNotOverrideLock(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainPath, "worktree", "add", "-b", "feat/locked", linkedPath)
	runGit(t, mainPath, "worktree", "lock", linkedPath)
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.RemoveWorktree(linkedPath, true); err == nil {
		t.Fatal("RemoveWorktree() error = nil, want locked-tree refusal")
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("locked worktree was removed: %v", err)
	}
}

func TestWorktreeStatusIncludesUntrackedAndSubmoduleChanges(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	dirty, err := repo.Dirty(mainPath)
	if err != nil || dirty {
		t.Fatalf("Dirty(clean) = %v, %v; want false, nil", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(mainPath, "untracked.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	dirty, err = repo.Dirty(mainPath)
	if err != nil || !dirty {
		t.Fatalf("Dirty(untracked) = %v, %v; want true, nil", dirty, err)
	}
}

func TestMergedUsesConfiguredDefaultReference(t *testing.T) {
	mainPath := initTestRepo(t)
	writeCommit(t, mainPath, "base.txt", "base")
	runGit(t, mainPath, "branch", "feat/merged")
	runGit(t, mainPath, "checkout", "-b", "feat/open")
	writeCommit(t, mainPath, "open.txt", "open")
	runGit(t, mainPath, "checkout", "main")
	repo, err := OpenRepository(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	merged, base, err := repo.BranchMerged("feat/merged", "main")
	if err != nil || !merged || base != "refs/heads/main" {
		t.Fatalf("BranchMerged(merged) = %v, %q, %v", merged, base, err)
	}
	merged, _, err = repo.BranchMerged("feat/open", "main")
	if err != nil || merged {
		t.Fatalf("BranchMerged(open) = %v, %v; want false, nil", merged, err)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}
