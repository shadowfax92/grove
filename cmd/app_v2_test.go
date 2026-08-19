package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grove/internal/config"
	"grove/internal/picker"

	"github.com/spf13/cobra"
)

func TestRootPickerPrintsSelectedWorktreePath(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/auth", linkedPath)
	writeV2Config(t, repoPath, "")

	picked := false
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		pick: func(prompt string, items []picker.Item) (string, error) {
			picked = true
			if prompt != "worktree > " || len(items) != 2 {
				t.Fatalf("picker = %q %#v", prompt, items)
			}
			return canonicalV2Path(t, linkedPath), nil
		},
	})
	stdout, _, err := executeV2(root)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !picked {
		t.Fatal("picker was not called")
	}
	if got, want := stdout, canonicalV2Path(t, linkedPath)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRootWithoutSelectorRefusesNonInteractivePicker(t *testing.T) {
	repoPath := initV2Repo(t)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	_, _, err := executeV2(root, "--no-input")
	if err == nil || !strings.Contains(err.Error(), "selector is required") {
		t.Fatalf("Execute() error = %v, want selector-required error", err)
	}
}

func TestCdExactSelectorNeverCallsPicker(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "fix/login", linkedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		pick: func(string, []picker.Item) (string, error) {
			t.Fatal("picker called for exact selector")
			return "", nil
		},
	})

	stdout, _, err := executeV2(root, "cd", "fix/login")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := stdout, canonicalV2Path(t, linkedPath)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestDeletedConfigRepositoryWarnsWithoutBlockingCommand(t *testing.T) {
	repoPath := initV2Repo(t)
	writeV2Config(t, repoPath, "  - path: /definitely/deleted/grove-repo\n    name: deleted\n")
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	stdout, stderr, err := executeV2(root, "cd", "app:")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout != canonicalV2Path(t, repoPath)+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "deleted") {
		t.Fatalf("stderr = %q, want deleted-repo warning", stderr)
	}
}

func TestNewCreatesCanonicalNestedWorktreeAndAutoRegisters(t *testing.T) {
	repoPath := initV2Repo(t)
	t.Setenv("HOME", t.TempDir())
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	stdout, _, err := executeV2(root, "new", "auth")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := filepath.Join(canonicalV2Path(t, repoPath), ".wt", "feat", "auth")
	if stdout != want+"\n" {
		t.Fatalf("stdout = %q, want %q", stdout, want+"\n")
	}
	if got := v2GitOutput(t, want, "branch", "--show-current"); got != "feat/auth" {
		t.Fatalf("branch = %q, want feat/auth", got)
	}
	exclude, err := os.ReadFile(filepath.Join(repoPath, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), "/.wt/") {
		t.Fatalf("exclude = %q, %v", exclude, err)
	}
	configData, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "grove", "config.yaml"))
	if err != nil || !strings.Contains(string(configData), canonicalV2Path(t, repoPath)) {
		t.Fatalf("config = %q, %v", configData, err)
	}
}

func TestNewRegistersBeforeCreatingWorktree(t *testing.T) {
	repoPath := initV2Repo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDirectory := filepath.Join(home, ".config", "grove")
	configPath := filepath.Join(configDirectory, "config.yaml")
	if err := os.MkdirAll(configDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("repos: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDirectory, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(configDirectory, 0755) })
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	_, _, err := executeV2(root, "new", "auth")
	if err == nil || !strings.Contains(err.Error(), "registering repository") {
		t.Fatalf("new error = %v, want registration failure", err)
	}
	worktreePath := filepath.Join(canonicalV2Path(t, repoPath), ".wt", "feat", "auth")
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree was created before registration succeeded: %v", err)
	}
}

func TestNewPreflightsPlainOutputBeforeMutation(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "line\nrepo")
	initV2RepoAt(t, repoPath)
	t.Setenv("HOME", t.TempDir())
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	_, _, err := executeV2(root, "new", "auth")
	if err == nil || !strings.Contains(err.Error(), "--null") {
		t.Fatalf("new error = %v, want output preflight error", err)
	}
	worktreePath := filepath.Join(canonicalV2Path(t, repoPath), ".wt", "feat", "auth")
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree was created before output validation: %v", err)
	}
}

func TestNewNullOutputRegistersNewlineRepositoryExactly(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "line\nrepo")
	initV2RepoAt(t, repoPath)
	t.Setenv("HOME", t.TempDir())
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	_, _, err := executeV2(root, "--null", "new", "auth")
	if err != nil {
		t.Fatalf("new --null error = %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != canonicalV2Path(t, repoPath) {
		t.Fatalf("registered repos = %#v, want exact newline path", cfg.Repos)
	}
}

func TestNewUsesExplicitProfileButPrintsWorktreeRoot(t *testing.T) {
	repoPath := initV2Repo(t)
	if err := os.Mkdir(filepath.Join(repoPath, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "subdir", ".keep"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, repoPath, "add", "subdir/.keep")
	runV2Git(t, repoPath, "commit", "-m", "add subdir")
	writeV2Config(t, "", strings.Join([]string{
		"  - path: " + repoPath,
		"    name: agent",
		"    default_branch: main",
		"    workdir: subdir",
		"    setup:",
		"      - touch setup-marker",
		"  - path: " + repoPath,
		"    name: app",
		"    default_branch: main",
		"",
	}, "\n"))
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	stdout, stderr, err := executeV2(root, "new", "agent:auth")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := filepath.Join(canonicalV2Path(t, repoPath), ".wt", "feat", "auth")
	if stdout != want+"\n" {
		t.Fatalf("stdout = %q, want root %q", stdout, want)
	}
	if _, err := os.Stat(filepath.Join(want, "subdir", "setup-marker")); err != nil {
		t.Fatalf("setup marker: %v; stderr=%s", err, stderr)
	}
}

func TestNewRefusesSetupWorkdirSymlinkOutsideWorktree(t *testing.T) {
	repoPath := initV2Repo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repoPath, "tools")); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, repoPath, "add", "tools")
	runV2Git(t, repoPath, "commit", "-m", "add tools link")
	writeV2Config(t, "", strings.Join([]string{
		"  - path: " + repoPath,
		"    name: agent",
		"    default_branch: main",
		"    workdir: tools",
		"    setup:",
		"      - touch escaped",
		"",
	}, "\n"))
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
	})

	_, stderr, err := executeV2(root, "new", "agent:auth")
	if err != nil {
		t.Fatalf("new error = %v", err)
	}
	if !strings.Contains(stderr, "setup skipped") || !strings.Contains(stderr, "escapes") {
		t.Fatalf("stderr = %q, want escaped-workdir warning", stderr)
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("setup ran outside worktree: %v", err)
	}
}

func TestRemoveRequiresDiscardAndPrintsMainPath(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "fix/dirty", linkedPath)
	if err := os.WriteFile(filepath.Join(linkedPath, "dirty"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return linkedPath, nil }, interactive: func() bool { return false }})
	_, _, err := executeV2(root, "rm", ".")
	if err == nil || !strings.Contains(err.Error(), "--discard") {
		t.Fatalf("rm dirty error = %v, want --discard guidance", err)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return linkedPath, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "rm", "--discard", ".")
	if err != nil {
		t.Fatalf("rm --discard error = %v", err)
	}
	if stdout != canonicalV2Path(t, repoPath)+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err := os.Stat(linkedPath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestRemovePickerSupportsMultipleWorktrees(t *testing.T) {
	repoPath := initV2Repo(t)
	firstPath := filepath.Join(t.TempDir(), "first")
	secondPath := filepath.Join(t.TempDir(), "second")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/first", firstPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/second", secondPath)
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		pickMany: func(prompt string, items []picker.Item) ([]string, error) {
			if prompt != "remove > " || len(items) != 2 {
				t.Fatalf("picker = %q %#v", prompt, items)
			}
			for _, item := range items {
				if !strings.Contains(item.Label, "created") || strings.Contains(item.Label, item.Key) {
					t.Fatalf("picker item = %#v, want compact label with creation age", item)
				}
			}
			return []string{canonicalV2Path(t, firstPath), canonicalV2Path(t, secondPath)}, nil
		},
	})

	stdout, _, err := executeV2(root, "rm")
	if err != nil {
		t.Fatalf("rm error = %v", err)
	}
	if got, want := stdout, canonicalV2Path(t, repoPath)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("worktree remains at %s: %v", path, err)
		}
	}
}

func TestRemoveMultiplePreflightsEveryTargetBeforeDeleting(t *testing.T) {
	repoPath := initV2Repo(t)
	firstPath := filepath.Join(t.TempDir(), "first")
	secondPath := filepath.Join(t.TempDir(), "second")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/first", firstPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/second", secondPath)
	initV2RepoAt(t, filepath.Join(secondPath, "nested"))
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		pickMany: func(string, []picker.Item) ([]string, error) {
			return []string{canonicalV2Path(t, firstPath), canonicalV2Path(t, secondPath)}, nil
		},
	})

	_, _, err := executeV2(root, "rm", "--discard")
	if err == nil || !strings.Contains(err.Error(), "nested Git repository") {
		t.Fatalf("rm error = %v, want nested-repository refusal", err)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("worktree was removed before preflight completed: %s: %v", path, err)
		}
	}
}

func TestRemoveExactPathPreservesTrailingWhitespace(t *testing.T) {
	repoPath := initV2Repo(t)
	base := t.TempDir()
	plainPath := filepath.Join(base, "tree")
	spacedPath := filepath.Join(base, "tree ")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/plain", plainPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/spaced", spacedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "rm", spacedPath)
	if err != nil {
		t.Fatalf("rm error = %v", err)
	}
	if _, err := os.Stat(spacedPath); !os.IsNotExist(err) {
		t.Fatalf("spaced worktree remains: %v", err)
	}
	if _, err := os.Stat(plainPath); err != nil {
		t.Fatalf("plain worktree was removed: %v", err)
	}
}

func TestRemovePreflightsPlainOutputBeforeMutation(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "line\nrepo")
	initV2RepoAt(t, repoPath)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/linked", linkedPath)
	writeV2Config(t, "", "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "rm", "feat/linked")
	if err == nil || !strings.Contains(err.Error(), "--null") {
		t.Fatalf("rm error = %v, want output preflight error", err)
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("worktree was removed before output validation: %v", err)
	}
}

func TestRemoveRefusesConfiguredRepositoryNestedInsideTarget(t *testing.T) {
	parentRepo := initV2Repo(t)
	parentPath := filepath.Join(t.TempDir(), "parent")
	runV2Git(t, parentRepo, "worktree", "add", "-b", "feat/parent", parentPath)
	childRepo := filepath.Join(parentPath, "nested-repo")
	initV2RepoAt(t, childRepo)
	writeV2Config(t, "", strings.Join([]string{
		"  - path: " + parentRepo,
		"    name: parent",
		"    default_branch: main",
		"  - path: " + childRepo,
		"    name: child",
		"    default_branch: main",
		"",
	}, "\n"))
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return parentRepo, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "rm", "--discard", "parent:feat/parent")
	if err == nil || !strings.Contains(err.Error(), "contains") || !strings.Contains(err.Error(), childRepo) {
		t.Fatalf("rm error = %v, want nested-repository refusal", err)
	}
	if _, err := os.Stat(childRepo); err != nil {
		t.Fatalf("nested repository was removed: %v", err)
	}
}

func TestRemoveMergedBulkIsConservative(t *testing.T) {
	repoPath := initV2Repo(t)
	mergedPath := filepath.Join(t.TempDir(), "merged")
	openPath := filepath.Join(t.TempDir(), "open")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/merged", mergedPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/open", openPath)
	if err := os.WriteFile(filepath.Join(openPath, "open.txt"), []byte("open"), 0644); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, openPath, "add", "open.txt")
	runV2Git(t, openPath, "commit", "-m", "open")
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "rm", "--merged", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if !strings.Contains(stdout, "feat/merged") || strings.Contains(stdout, "feat/open") {
		t.Fatalf("dry-run stdout = %q", stdout)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err = executeV2(root, "rm", "--merged")
	if err != nil {
		t.Fatalf("bulk remove error = %v", err)
	}
	if !strings.Contains(stdout, "feat/merged") {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err := os.Stat(mergedPath); !os.IsNotExist(err) {
		t.Fatalf("merged worktree remains: %v", err)
	}
	if _, err := os.Stat(openPath); err != nil {
		t.Fatalf("open worktree removed: %v", err)
	}
}

func TestRemoveMergedIgnoresUnregisteredCurrentRepository(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "merged")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/merged", linkedPath)
	writeV2Config(t, "", "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "rm", "--merged")
	if err != nil {
		t.Fatalf("rm --merged error = %v", err)
	}
	if strings.Contains(stdout, "feat/merged") {
		t.Fatalf("stdout = %q, unregistered repository was included", stdout)
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("unregistered worktree was removed: %v", err)
	}
}

func TestRemoveMergedJSONDryRunReportsWouldRemove(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "merged")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/merged", linkedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "--json", "rm", "--merged", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	var output struct {
		DryRun      bool  `json:"dry_run"`
		Removed     []any `json:"removed"`
		WouldRemove []any `json:"would_remove"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout)
	}
	if !output.DryRun || len(output.Removed) != 0 || len(output.WouldRemove) != 1 {
		t.Fatalf("dry-run JSON = %s", stdout)
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("dry-run removed worktree: %v", err)
	}
}

func TestRemoveMergedRejectsNullOutputBeforeMutation(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "merged")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/merged", linkedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "--null", "rm", "--merged=true")
	if err == nil || !strings.Contains(err.Error(), "single-worktree") {
		t.Fatalf("rm --null --merged error = %v", err)
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("bulk worktree was removed: %v", err)
	}
}

func TestNullPathOutputPreservesNewlines(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "line\nbreak")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/unusual", linkedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "--null", "cd", linkedPath)
	if err != nil {
		t.Fatalf("cd --null error = %v", err)
	}
	if got, want := stdout, canonicalV2Path(t, linkedPath)+"\x00"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	_, _, err = executeV2(root, "cd", linkedPath)
	if err == nil || !strings.Contains(err.Error(), "--null") {
		t.Fatalf("plain cd error = %v, want --null guidance", err)
	}
}

func TestListJSONIsVersionedAndStatusIsOptIn(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "chore/deps", linkedPath)
	if err := os.WriteFile(filepath.Join(linkedPath, "dirty"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "--json", "list")
	if err != nil {
		t.Fatalf("list --json error = %v", err)
	}
	var withoutStatus map[string]any
	if err := json.Unmarshal([]byte(stdout), &withoutStatus); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout)
	}
	if withoutStatus["version"] != float64(1) {
		t.Fatalf("version = %#v", withoutStatus["version"])
	}
	if strings.Contains(stdout, "\"dirty\"") {
		t.Fatalf("default JSON unexpectedly contains status: %s", stdout)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err = executeV2(root, "--json", "list", "--status")
	if err != nil {
		t.Fatalf("list --status error = %v", err)
	}
	if !strings.Contains(stdout, "\"dirty\": true") {
		t.Fatalf("status JSON = %s", stdout)
	}
}

func TestListOmitsWorktreePaths(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/compact", linkedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !strings.Contains(stdout, "feat/compact") {
		t.Fatalf("stdout = %q, want branch", stdout)
	}
	if canonicalPath := canonicalV2Path(t, linkedPath); strings.Contains(stdout, canonicalPath) {
		t.Fatalf("stdout = %q, contains worktree path %q", stdout, canonicalPath)
	}
}

func TestListShowsLinkedWorktreeCreationAge(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/aged", linkedPath)
	createdAt := time.Now().Add(-26 * time.Hour)
	if err := os.Chtimes(filepath.Join(linkedPath, ".git"), createdAt, createdAt); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !strings.Contains(stdout, "feat/aged  created 1d ago") {
		t.Fatalf("stdout = %q, want creation age", stdout)
	}
}

func TestListColorsOnlyHumanOutput(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/color", linkedPath)
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	colored, _, err := executeV2(root, "--color=always", "list")
	if err != nil {
		t.Fatalf("colored list error = %v", err)
	}
	if !strings.Contains(colored, "\x1b[1;36mapp\x1b[0m") || !strings.Contains(colored, "\x1b[32mfeat/color\x1b[0m") {
		t.Fatalf("colored stdout = %q", colored)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	plain, _, err := executeV2(root, "--color=never", "list")
	if err != nil {
		t.Fatalf("plain list error = %v", err)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain stdout contains ANSI: %q", plain)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	jsonOutput, _, err := executeV2(root, "--color=always", "--json", "list")
	if err != nil {
		t.Fatalf("JSON list error = %v", err)
	}
	if strings.Contains(jsonOutput, "\x1b[") || !json.Valid([]byte(jsonOutput)) {
		t.Fatalf("JSON stdout = %q", jsonOutput)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	pathOutput, _, err := executeV2(root, "--color=always", "cd", "app:")
	if err != nil {
		t.Fatalf("colored path command error = %v", err)
	}
	if got, want := pathOutput, canonicalV2Path(t, repoPath)+"\n"; got != want {
		t.Fatalf("path stdout = %q, want %q", got, want)
	}
}

func TestHelpUsesColorMode(t *testing.T) {
	root := newRootCommand(commandDependencies{})
	colored, _, err := executeV2(root, "--color=always", "--help")
	if err != nil {
		t.Fatalf("colored help error = %v", err)
	}
	if !strings.Contains(colored, "\x1b[1;36mUsage:\x1b[0m") || !strings.Contains(colored, "\x1b[32mlist\x1b[0m") {
		t.Fatalf("colored help = %q", colored)
	}

	root = newRootCommand(commandDependencies{})
	plain, _, err := executeV2(root, "--color=never", "--help")
	if err != nil {
		t.Fatalf("plain help error = %v", err)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain help contains ANSI: %q", plain)
	}
}

func TestRootExposesOnlyCohesiveCommands(t *testing.T) {
	root := newRootCommand(commandDependencies{})
	var names []string
	for _, command := range root.Commands() {
		if command.IsAvailableCommand() {
			names = append(names, command.Name())
		}
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"cd", "config", "list", "new", "rm"} {
		if !strings.Contains(got, want) {
			t.Fatalf("commands = %q, missing %s", got, want)
		}
	}
	for _, removed := range []string{"cleanup", "done", "init", "pull", "reap", "recycle", "sync", "which"} {
		if strings.Contains(got, removed) {
			t.Fatalf("commands = %q, still contains %s", got, removed)
		}
	}
}

func executeV2(root *cobra.Command, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func initV2Repo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	initV2RepoAt(t, path)
	return path
}

func initV2RepoAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, path, "init", "-b", "main")
	runV2Git(t, path, "config", "user.name", "Grove Test")
	runV2Git(t, path, "config", "user.email", "grove@example.test")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, path, "add", "README")
	runV2Git(t, path, "commit", "-m", "initial")
}

func writeV2Config(t *testing.T, repoPath, extraEntries string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "grove", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := "repos:\n" + extraEntries
	if repoPath != "" {
		data += "  - path: " + repoPath + "\n    name: app\n    default_branch: main\n"
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func runV2Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), out, err)
	}
}

func v2GitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out))
}

func canonicalV2Path(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
