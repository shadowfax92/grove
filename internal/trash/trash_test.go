package trash

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every destructive scenario uses an independent repository and state directory.
// The fake unregister hook exercises crash boundaries without killing the test.
func fixture(t *testing.T) (*Manager, string, string, string) {
	t.Helper()
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(base, "repo")
	must(t, os.Mkdir(main, 0755))
	gitRun(t, main, "init", "-b", "main")
	gitRun(t, main, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "fixture")
	original := filepath.Join(main, ".wt", "fixture")
	gitRun(t, main, "worktree", "add", "-b", "fixture", original)
	pointer, err := os.ReadFile(filepath.Join(original, ".git"))
	must(t, err)
	admin := strings.TrimSpace(strings.TrimPrefix(string(pointer), "gitdir: "))
	return New(filepath.Join(base, "state")), main, original, admin
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}
func detach(t *testing.T, m *Manager, main, original, admin string) {
	t.Helper()
	detached, err := m.Detach(original, main, admin, func() error { return nil }, func() error {
		cmd := exec.Command("git", "worktree", "remove", "--force", "--", original)
		cmd.Dir = main
		return cmd.Run()
	})
	must(t, err)
	if !detached {
		t.Fatal("expected fast detach")
	}
}
func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still present: %v", path, err)
	}
}

func TestDetachReturnsBeforeDeletionAndReapProtectsStore(t *testing.T) {
	m, main, original, admin := fixture(t)
	store := filepath.Join(t.TempDir(), "notes")
	must(t, os.Mkdir(store, 0755))
	must(t, os.WriteFile(filepath.Join(store, "keep"), []byte("notes"), 0600))
	for _, name := range []string{".llm", ".scratch"} {
		must(t, os.Symlink(store, filepath.Join(original, name)))
	}
	cache := filepath.Join(original, "cache")
	must(t, os.Mkdir(cache, 0755))
	must(t, os.WriteFile(filepath.Join(cache, "data"), []byte("bytes"), 0444))
	must(t, os.Chmod(cache, 0555))
	// A separate missing registration must survive removing this one.
	missing := filepath.Join(main, ".wt", "missing")
	gitRun(t, main, "worktree", "add", "-b", "missing", missing)
	must(t, os.RemoveAll(missing))
	detach(t, m, main, original, admin)
	absent(t, original)
	listing := gitRun(t, main, "worktree", "list", "--porcelain")
	if strings.Contains(listing, "worktree "+original+"\n") || !strings.Contains(listing, "worktree "+missing+"\n") {
		t.Fatalf("registrations: %s", listing)
	}
	gitRun(t, main, "show-ref", "--verify", "refs/heads/fixture")
	pending, err := m.Pending()
	must(t, err)
	if len(pending) != 1 {
		t.Fatalf("pending: %#v", pending)
	}
	if _, err = os.Stat(filepath.Join(pending[0].TreePath(), "cache", "data")); err != nil {
		t.Fatalf("detach deleted tree: %v", err)
	}
	must(t, m.Reap(context.Background()))
	absent(t, pending[0].TreePath())
	data, err := os.ReadFile(filepath.Join(store, "keep"))
	must(t, err)
	if string(data) != "notes" {
		t.Fatal("store changed")
	}
}

func TestInterruptedReapResumes(t *testing.T) {
	m, main, original, admin := fixture(t)
	detach(t, m, main, original, admin)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Reap(ctx); err == nil {
		t.Fatal("canceled reaper succeeded")
	}
	pending, err := m.Pending()
	must(t, err)
	if len(pending) != 1 {
		t.Fatal("journal lost")
	}
	must(t, m.Reap(context.Background()))
	pending, err = m.Pending()
	must(t, err)
	if len(pending) != 0 {
		t.Fatal("not reaped")
	}
}

func TestInterruptedDetachRecovery(t *testing.T) {
	for _, stage := range []string{"prepared", "moved", "stub", "empty-stub", "unregistered", "reused"} {
		t.Run(stage, func(t *testing.T) {
			m, main, original, admin := fixture(t)
			// A panic models termination: defers release the lock but do not roll back.
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("expected interruption")
					}
				}()
				_, _ = m.Detach(original, main, admin, func() error { return nil }, func() error {
					switch stage {
					case "prepared":
						// Undo the two renames to model a crash immediately after journaling.
						entries, _ := mEntriesUnlocked(m)
						e := entries[0]
						must(t, renameNoReplace(original, filepath.Join(e.directory(), "stub")))
						must(t, renameNoReplace(e.TreePath(), original))
					case "moved":
						entries, _ := mEntriesUnlocked(m)
						must(t, renameNoReplace(original, filepath.Join(entries[0].directory(), "stub")))
					case "empty-stub":
						must(t, os.Remove(filepath.Join(original, ".git")))
					case "unregistered", "reused":
						gitRun(t, main, "worktree", "remove", "--force", original)
						if stage == "reused" {
							gitRun(t, main, "worktree", "add", "-b", "replacement", original)
						}
					}
					panic("interrupted")
				})
			}()
			must(t, m.Reap(context.Background()))
			if stage == "unregistered" {
				absent(t, original)
			} else {
				gitRun(t, original, "status", "--porcelain")
				gitRun(t, main, "worktree", "list")
				if stage == "reused" {
					if got := strings.TrimSpace(gitRun(t, original, "branch", "--show-current")); got != "replacement" {
						t.Fatalf("reused branch: %s", got)
					}
				}
			}
			pending, err := m.Pending()
			must(t, err)
			if len(pending) != 0 {
				t.Fatalf("pending: %v", pending)
			}
		})
	}
}

// Inspection while Detach owns the root lock avoids recursively acquiring it.
func mEntriesUnlocked(m *Manager) ([]Entry, error) {
	indexes, err := os.ReadDir(filepath.Join(m.State, "roots"))
	if err != nil {
		return nil, err
	}
	var root rootRecord
	if err = readJSON(filepath.Join(m.State, "roots", indexes[0].Name()), &root); err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(root.Path)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if strings.HasPrefix(dir.Name(), "entry-") {
			entry, err := readEntry(root, filepath.Join(root.Path, dir.Name()))
			return []Entry{entry}, err
		}
	}
	return nil, errors.New("entry missing")
}

func TestUnregisterFailureRollsBack(t *testing.T) {
	m, main, original, admin := fixture(t)
	_, err := m.Detach(original, main, admin, func() error { return nil }, func() error { return errors.New("git failed") })
	if err == nil {
		t.Fatal("expected failure")
	}
	gitRun(t, original, "status", "--porcelain")
	pending, err := m.Pending()
	must(t, err)
	if len(pending) != 0 {
		t.Fatal("rollback left journal")
	}
}
func TestRenameFailureFallsBack(t *testing.T) {
	m, main, original, admin := fixture(t)
	m.rename = func(string, string) error { return errors.New("cross-device rename") }
	called := false
	detached, err := m.Detach(original, main, admin, func() error { return nil }, func() error { called = true; return nil })
	must(t, err)
	if detached || called {
		t.Fatal("failed rename reached unregister")
	}
	gitRun(t, original, "status", "--porcelain")
	pending, err := m.Pending()
	must(t, err)
	if len(pending) != 0 {
		t.Fatal("failed rename left journal")
	}
}
func TestUnrecognizedAndReplacedEntriesRefused(t *testing.T) {
	m, main, original, admin := fixture(t)
	detach(t, m, main, original, admin)
	root := RootFor(main, original)
	unknown := filepath.Join(root, "not-grove")
	must(t, os.Mkdir(unknown, 0755))
	must(t, os.WriteFile(filepath.Join(unknown, "keep"), []byte("safe"), 0600))
	if err := m.Reap(context.Background()); err == nil {
		t.Fatal("unrecognized entry accepted")
	}
	if _, err := os.Stat(filepath.Join(unknown, "keep")); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentDetachesAndReapers(t *testing.T) {
	m, main, original, admin := fixture(t)
	second := filepath.Join(main, ".wt", "second")
	gitRun(t, main, "worktree", "add", "-b", "second", second)
	pointer, err := os.ReadFile(filepath.Join(second, ".git"))
	must(t, err)
	admin2 := strings.TrimSpace(strings.TrimPrefix(string(pointer), "gitdir: "))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, path := range []string{original, second} {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			a := admin
			if i == 1 {
				a = admin2
			}
			_, err := m.Detach(path, main, a, func() error { return nil }, func() error {
				cmd := exec.Command("git", "worktree", "remove", "--force", path)
				cmd.Dir = main
				return cmd.Run()
			})
			errs <- err
		}(i, path)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs := m.Reap(context.Background())
			if errs != nil {
				t.Error(errs)
			}
		}()
	}
	wg.Wait()
	pending, err := m.Pending()
	must(t, err)
	if len(pending) != 0 {
		t.Fatal("pending entries")
	}
}

func TestWriterCannotRedirectReaperThroughSymlink(t *testing.T) {
	m, main, original, admin := fixture(t)
	store := t.TempDir()
	must(t, os.WriteFile(filepath.Join(store, "keep"), []byte("notes"), 0600))
	detach(t, m, main, original, admin)
	pending, err := m.Pending()
	must(t, err)
	tree := pending[0].TreePath()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			path := filepath.Join(tree, "changing")
			_ = os.Mkdir(path, 0755)
			_ = os.WriteFile(filepath.Join(path, "writing"), []byte("data"), 0600)
			_ = os.Remove(filepath.Join(path, "writing"))
			_ = os.Remove(path)
			_ = os.Symlink(store, path)
			_ = os.Remove(path)
		}
	}()
	_ = m.Reap(context.Background())
	close(stop)
	<-done
	must(t, m.Reap(context.Background()))
	data, err := os.ReadFile(filepath.Join(store, "keep"))
	must(t, err)
	if string(data) != "notes" {
		t.Fatal("store changed")
	}
}

func TestExternalWorktreeUsesSiblingTrash(t *testing.T) {
	m, main, original, _ := fixture(t)
	external := filepath.Join(filepath.Dir(main), "external")
	gitRun(t, main, "worktree", "move", original, external)
	pointer, err := os.ReadFile(filepath.Join(external, ".git"))
	must(t, err)
	admin := strings.TrimSpace(strings.TrimPrefix(string(pointer), "gitdir: "))
	detach(t, m, main, external, admin)
	pending, err := m.Pending()
	must(t, err)
	if pending[0].Root != filepath.Join(filepath.Dir(external), ".grove-trash") {
		t.Fatalf("root %s", pending[0].Root)
	}
	must(t, m.Reap(context.Background()))
}
