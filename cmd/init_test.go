package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildInitRepoUsesGitRootDefaults(t *testing.T) {
	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")

	nested := filepath.Join(repoPath, "packages", "app")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}

	repo, err := buildInitRepo(nested, "", "")
	if err != nil {
		t.Fatalf("buildInitRepo() error = %v", err)
	}

	wantPath, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("resolving repo path: %v", err)
	}
	if got, want := repo.Path, wantPath; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := repo.Name, filepath.Base(repoPath); got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := repo.DefaultBranch, "main"; got != want {
		t.Fatalf("DefaultBranch = %q, want %q", got, want)
	}
}

func TestBuildInitRepoAllowsNameAndDefaultBranchOverrides(t *testing.T) {
	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")

	repo, err := buildInitRepo(repoPath, "mono", "dev")
	if err != nil {
		t.Fatalf("buildInitRepo() error = %v", err)
	}

	if got, want := repo.Name, "mono"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := repo.DefaultBranch, "dev"; got != want {
		t.Fatalf("DefaultBranch = %q, want %q", got, want)
	}
}

func TestBuildInitRepoRejectsNonGitDirectory(t *testing.T) {
	if _, err := buildInitRepo(t.TempDir(), "", ""); err == nil {
		t.Fatal("buildInitRepo() error = nil, want non-git error")
	}
}

func TestInitCommandExposesOverrideFlags(t *testing.T) {
	for _, name := range []string{"name", "default-branch"} {
		if flag := initCmd.Flags().Lookup(name); flag == nil {
			t.Fatalf("init command missing --%s flag", name)
		}
	}
}
