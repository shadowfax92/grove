package inventory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/catalog"
	"grove/internal/config"
)

func TestResolveUsesCurrentRepositoryForBareBranch(t *testing.T) {
	repoPath := initInventoryRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runInventoryGit(t, repoPath, "worktree", "add", "-b", "feat/auth", linkedPath)
	cat, warnings := catalog.Build(&config.Config{Repos: []config.RepoConfig{{Path: repoPath, Name: "app"}}}, repoPath)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	inv, failures := Build(cat)
	if len(failures) != 0 {
		t.Fatalf("failures = %#v", failures)
	}

	got, err := inv.Resolve("feat/auth", repoPath)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Worktree.Branch != "feat/auth" || got.Worktree.Path != canonicalInventoryPath(t, linkedPath) {
		t.Fatalf("Resolve() = %#v", got)
	}
}

func TestResolveRepoPrefixAndMain(t *testing.T) {
	repoPath := initInventoryRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runInventoryGit(t, repoPath, "worktree", "add", "-b", "fix/login", linkedPath)
	cat, _ := catalog.Build(&config.Config{Repos: []config.RepoConfig{{Path: repoPath, Name: "app"}}}, "")
	inv, _ := Build(cat)

	main, err := inv.Resolve("app:", "")
	if err != nil || !main.Worktree.Main {
		t.Fatalf("Resolve(app:) = %#v, %v", main, err)
	}
	linked, err := inv.Resolve("app:fix/login", "")
	if err != nil || linked.Worktree.Branch != "fix/login" {
		t.Fatalf("Resolve(app:fix/login) = %#v, %v", linked, err)
	}
}

func TestResolvePathFindsContainingWorktree(t *testing.T) {
	repoPath := initInventoryRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runInventoryGit(t, repoPath, "worktree", "add", "-b", "feat/path", linkedPath)
	nested := filepath.Join(linkedPath, "some", "directory")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	cat, _ := catalog.Build(&config.Config{Repos: []config.RepoConfig{{Path: repoPath, Name: "app"}}}, linkedPath)
	inv, _ := Build(cat)

	got, err := inv.Resolve(".", nested)
	if err != nil {
		t.Fatalf("Resolve(.) error = %v", err)
	}
	if got.Worktree.Path != canonicalInventoryPath(t, linkedPath) {
		t.Fatalf("Resolve(.) path = %q, want %q", got.Worktree.Path, linkedPath)
	}
}

func TestResolveBareBranchRequiresCurrentRepository(t *testing.T) {
	repoPath := initInventoryRepo(t)
	cat, _ := catalog.Build(&config.Config{Repos: []config.RepoConfig{{Path: repoPath, Name: "app"}}}, "")
	inv, _ := Build(cat)

	_, err := inv.Resolve("main", "")
	if err == nil || !strings.Contains(err.Error(), "repo:branch") {
		t.Fatalf("Resolve(main) error = %v, want explicit repository guidance", err)
	}
}

func TestPickerItemsIncludeStableSelectorsAndPaths(t *testing.T) {
	repoPath := initInventoryRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runInventoryGit(t, repoPath, "worktree", "add", "-b", "chore/deps", linkedPath)
	cat, _ := catalog.Build(&config.Config{Repos: []config.RepoConfig{{Path: repoPath, Name: "app"}}}, "")
	inv, _ := Build(cat)

	items := inv.PickerItems()
	if len(items) != 2 {
		t.Fatalf("len(PickerItems) = %d, want 2", len(items))
	}
	if items[0].Selector != "app:" || items[1].Selector != "app:chore/deps" {
		t.Fatalf("PickerItems = %#v", items)
	}
	if items[1].Path != canonicalInventoryPath(t, linkedPath) {
		t.Fatalf("linked picker path = %q", items[1].Path)
	}
}

func initInventoryRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	runInventoryGit(t, path, "init", "-b", "main")
	runInventoryGit(t, path, "config", "user.name", "Grove Test")
	runInventoryGit(t, path, "config", "user.email", "grove@example.test")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	runInventoryGit(t, path, "add", "README")
	runInventoryGit(t, path, "commit", "-m", "initial")
	return path
}

func runInventoryGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), out, err)
	}
}

func canonicalInventoryPath(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
