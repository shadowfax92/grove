package catalog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
)

func TestBuildDeduplicatesRepositoryAndPreservesProfiles(t *testing.T) {
	repoPath := initCatalogRepo(t)
	nested := filepath.Join(repoPath, "packages", "agent")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Repos: []config.RepoConfig{
		{Path: repoPath, Name: "agent", Workdir: "packages/agent", DefaultBranch: "main", Setup: []string{"agent-setup"}},
		{Path: repoPath + string(os.PathSeparator), Name: "main", DefaultBranch: "main", Setup: []string{"root-setup"}},
		{Path: nested, Name: "patches", Workdir: "packages/agent", Setup: []string{"patch-setup"}},
	}}

	got, warnings := Build(cfg, nested)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if len(got.Repositories) != 1 {
		t.Fatalf("len(Repositories) = %d, want 1", len(got.Repositories))
	}
	repo := got.Repositories[0]
	if repo.Name != filepath.Base(repoPath) {
		t.Fatalf("repository Name = %q, want checkout name %q", repo.Name, filepath.Base(repoPath))
	}
	if got.Current != repo || !got.CurrentRegistered {
		t.Fatalf("current = %#v registered=%v, want configured repository", got.Current, got.CurrentRegistered)
	}
	for _, name := range []string{"agent", "main", "patches"} {
		resolved, profile, err := got.FindRepository(name)
		if err != nil {
			t.Fatalf("FindRepository(%q) error = %v", name, err)
		}
		if resolved != repo || profile.Name != name {
			t.Fatalf("FindRepository(%q) = %#v %#v", name, resolved, profile)
		}
	}
	resolved, profile, err := got.FindRepository(repo.Name)
	if err != nil || resolved != repo || profile.Name != "main" {
		t.Fatalf("FindRepository(%q) = %#v %#v, %v", repo.Name, resolved, profile, err)
	}
	if profile := repo.DefaultProfile(); profile == nil || profile.Name != "main" {
		t.Fatalf("DefaultProfile() = %#v, want main", profile)
	}
}

func TestBuildWarnsPastInvalidEntriesAndSilentlyIgnoresLegacyTypes(t *testing.T) {
	repoPath := initCatalogRepo(t)
	cfg := &config.Config{Repos: []config.RepoConfig{
		{Path: filepath.Join(t.TempDir(), "deleted"), Name: "deleted"},
		{Path: t.TempDir(), Name: "notes", Type: "dir"},
		{Path: repoPath, Name: "valid", DefaultBranch: "main"},
	}}

	got, warnings := Build(cfg, "")
	if len(got.Repositories) != 1 || got.Repositories[0].Name != "valid" {
		t.Fatalf("Repositories = %#v, want valid repo", got.Repositories)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want 1", warnings)
	}
	if !strings.Contains(warnings[0].Error(), "deleted") {
		t.Fatalf("warnings = %q, want deleted entry", warnings[0].Error())
	}
}

func TestBuildWarnsAndSkipsMalformedRows(t *testing.T) {
	repoPath := initCatalogRepo(t)
	cfg := &config.Config{Repos: []config.RepoConfig{
		{Path: "", Name: "blank"},
		{Path: "relative/repo", Name: "relative"},
		{Path: repoPath, Name: "unknown", Type: "mystery"},
		{Path: t.TempDir(), Name: "legacy", Type: "plain"},
		{Path: repoPath, Name: "valid"},
	}}

	got, warnings := Build(cfg, repoPath)
	if len(got.Repositories) != 1 || got.Repositories[0].Name != "valid" {
		t.Fatalf("Repositories = %#v, want only valid", got.Repositories)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %#v, want blank, relative, and unknown-type warnings", warnings)
	}
	text := fmt.Sprint(warnings)
	for _, want := range []string{"blank", "relative", "mystery"} {
		if !strings.Contains(text, want) {
			t.Fatalf("warnings = %q, missing %q", text, want)
		}
	}
}

func TestBuildAddsUnregisteredCurrentRepository(t *testing.T) {
	repoPath := initCatalogRepo(t)
	cfg := &config.Config{Repos: []config.RepoConfig{}}

	got, warnings := Build(cfg, repoPath)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if got.Current == nil || got.Current.Name != filepath.Base(repoPath) {
		t.Fatalf("Current = %#v", got.Current)
	}
	if got.CurrentRegistered {
		t.Fatal("CurrentRegistered = true, want false")
	}
	if got.UniqueName(got.Current.Name) != got.Current.Name {
		t.Fatalf("UniqueName(current) = %q, want %q", got.UniqueName(got.Current.Name), got.Current.Name)
	}
}

func TestBuildMakesConflictingAliasesAmbiguous(t *testing.T) {
	first := initCatalogRepo(t)
	second := initCatalogRepo(t)
	cfg := &config.Config{Repos: []config.RepoConfig{
		{Path: first, Name: "same"},
		{Path: second, Name: "same"},
	}}

	got, warnings := Build(cfg, "")
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "duplicate") {
		t.Fatalf("warnings = %#v, want duplicate alias warning", warnings)
	}
	if _, _, err := got.FindRepository("same"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("FindRepository(same) error = %v, want ambiguous", err)
	}
}

func initCatalogRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	runCatalogGit(t, path, "init", "-b", "main")
	runCatalogGit(t, path, "config", "user.name", "Grove Test")
	runCatalogGit(t, path, "config", "user.email", "grove@example.test")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	runCatalogGit(t, path, "add", "README")
	runCatalogGit(t, path, "commit", "-m", "initial")
	return path
}

func runCatalogGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), out, err)
	}
}
