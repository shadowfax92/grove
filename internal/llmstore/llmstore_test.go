package llmstore_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gitx "grove/internal/git"
	"grove/internal/llmstore"
)

func TestLinkSharesOneStoreAcrossWorktreesAndStaysIgnored(t *testing.T) {
	home, store := openStore(t)
	repo := initRepo(t, filepath.Join(home, "code", "app"))
	worktree := filepath.Join(repo.MainPath, ".wt", "feat")
	git(t, repo.MainPath, "worktree", "add", "-q", "-b", "feat", worktree)

	for _, path := range []string{repo.MainPath, worktree} {
		link, err := store.Link(llmstore.Repo{Git: repo}, path, false)
		if err != nil {
			t.Fatalf("Link(%s) error = %v", path, err)
		}
		if link.Anchors[0].Action != llmstore.Created || link.Store.Key != "code/app" {
			t.Fatalf("Link(%s) = %+v, want created code/app", path, link)
		}
		if target, _ := os.Readlink(filepath.Join(path, ".llm")); target != link.Store.Path {
			t.Fatalf("anchor target = %q, want %q", target, link.Store.Path)
		}
		if status := git(t, path, "status", "--porcelain"); status != "" {
			t.Fatalf("git status in %s = %q, want the link ignored", path, status)
		}
	}
	again, err := store.Link(llmstore.Repo{Git: repo}, worktree, false)
	if err != nil || again.Anchors[0].Action != llmstore.Linked {
		t.Fatalf("second Link = %+v, %v; want linked", again, err)
	}
	if _, err := os.Stat(filepath.Join(again.Store.Path, ".grove-llm")); err != nil {
		t.Fatalf("store marker missing: %v", err)
	}
}

func TestAliasesLinkTheSameStoreIndependently(t *testing.T) {
	home, store := openStore(t, ".scratch")
	repo := initRepo(t, filepath.Join(home, "code", "app"))

	link, err := store.Link(llmstore.Repo{Git: repo}, repo.MainPath, false)
	if err != nil || len(link.Anchors) != 2 {
		t.Fatalf("Link = %+v, %v; want .llm and .scratch", link, err)
	}
	for _, name := range []string{".llm", ".scratch"} {
		if target, _ := os.Readlink(filepath.Join(repo.MainPath, name)); target != link.Store.Path {
			t.Fatalf("%s -> %q, want %q", name, target, link.Store.Path)
		}
	}
	if status := git(t, repo.MainPath, "status", "--porcelain"); status != "" {
		t.Fatalf("git status = %q, want both links ignored", status)
	}

	// A real directory at an alias blocks only that alias.
	other := initRepo(t, filepath.Join(home, "code", "other"))
	writeFile(t, filepath.Join(other.MainPath, ".scratch", "keep.md"))
	link, err = store.Link(llmstore.Repo{Git: other}, other.MainPath, false)
	if !errors.Is(err, llmstore.ErrBlocked) || link.Anchors[0].Action != llmstore.Created || link.Anchors[1].Action != llmstore.Blocked {
		t.Fatalf("Link with blocked alias = %+v, %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(other.MainPath, ".scratch", "keep.md")); err != nil {
		t.Fatalf("blocked alias content was touched: %v", err)
	}
}

func TestExplicitKeySharesStoreAcrossClones(t *testing.T) {
	home, store := openStore(t)
	first := initRepo(t, filepath.Join(home, "code", "m1"))
	second := initRepo(t, filepath.Join(home, "code", "m2"))

	a, err := store.Link(llmstore.Repo{Git: first, Key: "browseros"}, first.MainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Link(llmstore.Repo{Git: second, Key: "browseros"}, second.MainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Store != b.Store || a.Store.Path != filepath.Join(home, "llm", "browseros") {
		t.Fatalf("stores = %+v and %+v, want one shared browseros store", a.Store, b.Store)
	}
}

func TestLinkRepointsForeignSymlinkWithoutTouchingItsTarget(t *testing.T) {
	home, store := openStore(t)
	repo := initRepo(t, filepath.Join(home, "code", "app"))
	old := filepath.Join(home, "old-llm", "app")
	writeFile(t, filepath.Join(old, "notes.md"))
	anchor := filepath.Join(repo.MainPath, ".llm")
	if err := os.Symlink(old, anchor); err != nil {
		t.Fatal(err)
	}

	preview, err := store.Link(llmstore.Repo{Git: repo}, repo.MainPath, true)
	if err != nil || preview.Anchors[0].Action != llmstore.Repointed || preview.Anchors[0].Previous != old {
		t.Fatalf("dry run = %+v, %v; want repointed from %s", preview, err, old)
	}
	if target, _ := os.Readlink(anchor); target != old {
		t.Fatalf("dry run changed anchor to %q", target)
	}

	link, err := store.Link(llmstore.Repo{Git: repo}, repo.MainPath, false)
	if err != nil || link.Anchors[0].Action != llmstore.Repointed {
		t.Fatalf("Link = %+v, %v; want repointed", link, err)
	}
	if target, _ := os.Readlink(anchor); target != link.Store.Path {
		t.Fatalf("anchor target = %q, want %q", target, link.Store.Path)
	}
	if _, err := os.Stat(filepath.Join(old, "notes.md")); err != nil {
		t.Fatalf("old target content was touched: %v", err)
	}
}

func TestLinkHealsDanglingAnchorAfterStoreRootMoves(t *testing.T) {
	home, store := openStore(t)
	repo := initRepo(t, filepath.Join(home, "code", "app"))
	link, err := store.Link(llmstore.Repo{Git: repo}, repo.MainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(home, "llm"), filepath.Join(home, "moved-llm")); err != nil {
		t.Fatal(err)
	}

	healed, err := store.Link(llmstore.Repo{Git: repo}, repo.MainPath, false)
	if err != nil || healed.Anchors[0].Action != llmstore.Linked {
		t.Fatalf("Link = %+v, %v; want linked", healed, err)
	}
	if info, err := os.Stat(link.Store.Path); err != nil || !info.IsDir() {
		t.Fatalf("store was not recreated: %v", err)
	}
}

func TestLinkNeverReplacesRealFilesOrTrackedAnchors(t *testing.T) {
	home, store := openStore(t)
	repo := initRepo(t, filepath.Join(home, "code", "app"))
	writeFile(t, filepath.Join(repo.MainPath, ".llm", "plan.md"))

	link, err := store.Link(llmstore.Repo{Git: repo}, repo.MainPath, false)
	if !errors.Is(err, llmstore.ErrBlocked) || link.Anchors[0].Action != llmstore.Blocked {
		t.Fatalf("Link over real directory = %+v, %v; want blocked", link, err)
	}
	if _, err := os.Stat(filepath.Join(repo.MainPath, ".llm", "plan.md")); err != nil {
		t.Fatalf("real directory was touched: %v", err)
	}

	tracked := initRepo(t, filepath.Join(home, "code", "tracked"))
	if err := os.Symlink("elsewhere", filepath.Join(tracked.MainPath, ".llm")); err != nil {
		t.Fatal(err)
	}
	git(t, tracked.MainPath, "add", ".llm")
	git(t, tracked.MainPath, "commit", "-q", "-m", "track link")
	if _, err := store.Link(llmstore.Repo{Git: tracked}, tracked.MainPath, false); err == nil || !strings.Contains(err.Error(), "tracks") {
		t.Fatalf("Link over tracked anchor error = %v, want refusal", err)
	}
	if target, _ := os.Readlink(filepath.Join(tracked.MainPath, ".llm")); target != "elsewhere" {
		t.Fatalf("tracked anchor changed to %q", target)
	}
}

func TestTargetKeys(t *testing.T) {
	home, store := openStore(t)
	outside := initRepo(t, t.TempDir())

	if _, err := store.Target(llmstore.Repo{Git: outside}); err == nil {
		t.Fatal("Target outside $HOME without a key succeeded")
	}
	if target, err := store.Target(llmstore.Repo{Git: outside, Key: "team/notes"}); err != nil || target.Path != filepath.Join(home, "llm", "team", "notes") {
		t.Fatalf("Target with key = %+v, %v", target, err)
	}
	for _, key := range []string{"../escape", "/abs", ".", "a/../b", "a//b"} {
		if _, err := store.Target(llmstore.Repo{Git: outside, Key: key}); err == nil {
			t.Errorf("Target(key %q) succeeded, want invalid key", key)
		}
	}
	if _, err := store.Target(llmstore.Repo{Git: outside, Disabled: true}); !errors.Is(err, llmstore.ErrDisabled) {
		t.Fatalf("disabled Target error = %v", err)
	}
}

func TestOpenRejectsUnsafeSettings(t *testing.T) {
	home := t.TempDir()
	for _, settings := range []llmstore.Settings{
		{Root: "relative"},
		{Root: home},
		{Root: filepath.Dir(home)},
		{Root: filepath.Join(home, "llm"), Dir: "a/b"},
		{Root: filepath.Join(home, "llm"), Dir: ".."},
		{Root: filepath.Join(home, "llm"), Aliases: []string{".llm"}},
		{Root: filepath.Join(home, "llm"), Aliases: []string{".git"}},
	} {
		if _, err := llmstore.Open(settings, home); err == nil {
			t.Errorf("Open(%+v) succeeded", settings)
		}
	}
}

func TestArchiveMovesOnlyIdleUnprotectedEntries(t *testing.T) {
	_, store := openStore(t)
	target, err := store.Target(llmstore.Repo{Key: "proj"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	for _, name := range []string{"idle-plan/a.md", "idle-plan/deep/b.md", "idle.md", "runs/r1/brief.md", ".hidden/x", "nested/sub/.grove-llm"} {
		writeFile(t, filepath.Join(target.Path, name))
	}
	writeFile(t, filepath.Join(target.Path, "active/old.md"))
	for _, name := range []string{"idle-plan", "idle.md", "runs", ".hidden", "nested", "active"} {
		touchTree(t, filepath.Join(target.Path, name), old)
	}
	writeFile(t, filepath.Join(target.Path, "active/today.md"))
	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	preview, err := store.Archive(target, cutoff, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(preview); !slices.Equal(got, []string{"idle-plan", "idle.md"}) {
		t.Fatalf("dry run = %v, want idle-plan and idle.md", got)
	}
	if _, err := os.Stat(filepath.Join(target.Path, "idle-plan")); err != nil {
		t.Fatalf("dry run moved an entry: %v", err)
	}

	moved, err := store.Archive(target, cutoff, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(moved); !slices.Equal(got, []string{"idle-plan", "idle.md"}) {
		t.Fatalf("Archive = %v", got)
	}
	if _, err := os.Stat(filepath.Join(target.Path, "archive", "idle-plan", "deep", "b.md")); err != nil {
		t.Fatalf("archived tree incomplete: %v", err)
	}
	for _, name := range []string{"runs", ".hidden", "nested", "active"} {
		if _, err := os.Stat(filepath.Join(target.Path, name)); err != nil {
			t.Errorf("%s moved: %v", name, err)
		}
	}

	// A second entry with an already archived name gets a dated suffix.
	writeFile(t, filepath.Join(target.Path, "idle.md"))
	touchTree(t, filepath.Join(target.Path, "idle.md"), old)
	again, err := store.Archive(target, cutoff, false)
	if err != nil || len(again) != 1 {
		t.Fatalf("second Archive = %+v, %v", again, err)
	}
	if want := filepath.Join(target.Path, "archive", "idle.md."+time.Now().Format("20060102")); again[0].Destination != want {
		t.Fatalf("collision destination = %q, want %q", again[0].Destination, want)
	}
	if _, err := os.Stat(filepath.Join(target.Path, "archive", "idle.md")); err != nil {
		t.Fatalf("first archived copy was overwritten: %v", err)
	}

	missing, err := store.Target(llmstore.Repo{Key: "never-created"})
	if err != nil {
		t.Fatal(err)
	}
	if archived, err := store.Archive(missing, cutoff, false); err != nil || len(archived) != 0 {
		t.Fatalf("Archive of missing store = %+v, %v", archived, err)
	}
}

func openStore(t *testing.T, aliases ...string) (string, *llmstore.Store) {
	t.Helper()
	// A user-level gitignore may already ignore .llm; isolate Git so the tests
	// prove grove's own exclusion.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := llmstore.Open(llmstore.Settings{Root: filepath.Join(home, "llm"), Aliases: aliases}, home)
	if err != nil {
		t.Fatal(err)
	}
	return home, store
}

func initRepo(t *testing.T, path string) *gitx.Repository {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	git(t, path, "init", "-q", "-b", "main")
	git(t, path, "config", "user.name", "Grove Test")
	git(t, path, "config", "user.email", "grove@example.test")
	writeFile(t, filepath.Join(path, "README"))
	git(t, path, "add", "README")
	git(t, path, "commit", "-q", "-m", "init")
	// Grove places worktrees under .wt and excludes it the same way.
	if err := os.WriteFile(filepath.Join(path, ".git", "info", "exclude"), []byte("/.wt/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	repo, err := gitx.OpenRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

// touchTree ages a whole tree, children before parents, because creating a
// child bumps its directory's modification time.
func touchTree(t *testing.T, root string, when time.Time) {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		paths = append(paths, path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Chtimes(paths[index], when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func names(archived []llmstore.Archived) []string {
	out := make([]string, 0, len(archived))
	for _, item := range archived {
		out = append(out, item.Name)
	}
	return out
}
