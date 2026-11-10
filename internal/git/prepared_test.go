package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/trash"
)

func TestPreparedRemovalRechecksIdentityInsideDetach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	main := initTestRepo(t)
	writeCommit(t, main, "base", "base")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, main, "worktree", "add", "-b", "topic", linked)
	repo, err := OpenRepository(main)
	if err != nil {
		t.Fatal(err)
	}
	removal, err := repo.PrepareWorktreeRemoval(linked, false)
	if err != nil {
		t.Fatal(err)
	}
	saved := linked + "-saved"
	if err := os.Rename(linked, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(linked, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git", "base"} {
		data, err := os.ReadFile(filepath.Join(saved, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(linked, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Call the internal detach path directly to model a replacement after the
	// public method's early check but before its root-lock callback.
	manager, err := trash.Default()
	if err != nil {
		t.Fatal(err)
	}
	err = repo.removePreparedWorktree(linked, false, removal, manager)
	if err == nil || !strings.Contains(err.Error(), "changed since validation") {
		t.Fatal(err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal("replacement removed", err)
	}
}
func TestPreparedRemovalGuardRunsAtDetachBoundary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	main := initTestRepo(t)
	writeCommit(t, main, "base", "base")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, main, "worktree", "add", "-b", "topic", linked)
	repo, err := OpenRepository(main)
	if err != nil {
		t.Fatal(err)
	}
	p, err := repo.PrepareWorktreeRemoval(linked, false)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	p.Guard(func() error { called = true; return fmt.Errorf("tip changed") })
	err = p.Remove()
	if err == nil || !called || !strings.Contains(err.Error(), "tip changed") {
		t.Fatal(called, err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal(err)
	}
}
