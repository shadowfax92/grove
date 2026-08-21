package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestRootPickerShowsCompactWorktreesInRecentOrder(t *testing.T) {
	repoPath := initV2Repo(t)
	olderPath := filepath.Join(t.TempDir(), "older")
	recentPath := filepath.Join(t.TempDir(), "recent")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/older", olderPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "fix/recent", recentPath)
	writeV2Config(t, repoPath, "")
	recentPath = canonicalV2Path(t, recentPath)

	markedPath := ""
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		lastVisited: func(path string) (time.Time, bool) {
			if path == recentPath {
				return time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC), true
			}
			return time.Time{}, false
		},
		markVisited: func(path string) error {
			markedPath = path
			return nil
		},
		pick: func(prompt string, items []picker.Item) (string, error) {
			if prompt != "worktree > " || len(items) != 3 {
				t.Fatalf("picker = %q %#v", prompt, items)
			}
			if items[0].Key != recentPath || items[0].Label != "app  fix/recent" {
				t.Fatalf("first picker item = %#v, want compact recent worktree", items[0])
			}
			for _, item := range items {
				if strings.Contains(item.Label, item.Key) {
					t.Fatalf("picker label exposes path: %#v", item)
				}
				if !strings.HasPrefix(item.Label, "app  ") {
					t.Fatalf("picker label = %q, want repository column", item.Label)
				}
			}
			return recentPath, nil
		},
	})

	stdout, _, err := executeV2(root)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout != recentPath+"\n" || markedPath != recentPath {
		t.Fatalf("stdout = %q, marked = %q, want %q", stdout, markedPath, recentPath)
	}
}

func TestRootPickerFallsBackToNewestWorktreeCreation(t *testing.T) {
	repoPath := initV2Repo(t)
	worktreeRoot := t.TempDir()
	olderPath := filepath.Join(worktreeRoot, "a-older")
	newerPath := filepath.Join(worktreeRoot, "z-newer")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/older", olderPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/newer", newerPath)
	createdAt := time.Now()
	for path, timestamp := range map[string]time.Time{
		olderPath: createdAt.Add(-4 * time.Hour),
		newerPath: createdAt.Add(-1 * time.Hour),
	} {
		if err := os.Chtimes(filepath.Join(path, ".git"), timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	writeV2Config(t, repoPath, "")
	olderPath = canonicalV2Path(t, olderPath)
	newerPath = canonicalV2Path(t, newerPath)

	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		lastVisited: func(string) (time.Time, bool) { return time.Time{}, false },
		markVisited: func(string) error { return nil },
		pick: func(_ string, items []picker.Item) (string, error) {
			olderIndex, newerIndex := -1, -1
			for index, item := range items {
				switch item.Key {
				case olderPath:
					olderIndex = index
				case newerPath:
					newerIndex = index
				}
			}
			if newerIndex == -1 || olderIndex == -1 || newerIndex > olderIndex {
				t.Fatalf("picker items = %#v, want newer creation before older", items)
			}
			return newerPath, nil
		},
	})

	if _, _, err := executeV2(root); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootPickerVisitOrderWinsOverCreationMetadata(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/linked", linkedPath)
	base := time.Now().Add(-24 * time.Hour)
	for path, timestamp := range map[string]time.Time{
		filepath.Join(linkedPath, ".git"): base.Add(1 * time.Hour),
		filepath.Join(repoPath, ".git"):   base.Add(4 * time.Hour),
	} {
		if err := os.Chtimes(path, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	writeV2Config(t, repoPath, "")
	mainPath := canonicalV2Path(t, repoPath)
	linkedPath = canonicalV2Path(t, linkedPath)

	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		lastVisited: func(path string) (time.Time, bool) {
			switch path {
			case linkedPath:
				return base.Add(3 * time.Hour), true
			case mainPath:
				return base.Add(2 * time.Hour), true
			default:
				return time.Time{}, false
			}
		},
		markVisited: func(string) error { return nil },
		pick: func(_ string, items []picker.Item) (string, error) {
			if items[0].Key != linkedPath {
				t.Fatalf("first picker item = %#v, want most recently visited worktree", items[0])
			}
			return linkedPath, nil
		},
	})

	if _, _, err := executeV2(root); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootPickerDoesNotTreatMainGitMtimeAsCreation(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/linked", linkedPath)
	base := time.Now().Add(-24 * time.Hour)
	for path, timestamp := range map[string]time.Time{
		filepath.Join(linkedPath, ".git"): base.Add(1 * time.Hour),
		filepath.Join(repoPath, ".git"):   base.Add(4 * time.Hour),
	} {
		if err := os.Chtimes(path, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	writeV2Config(t, repoPath, "")
	mainPath := canonicalV2Path(t, repoPath)
	linkedPath = canonicalV2Path(t, linkedPath)

	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		lastVisited: func(string) (time.Time, bool) { return time.Time{}, false },
		markVisited: func(string) error { return nil },
		pick: func(_ string, items []picker.Item) (string, error) {
			mainIndex, linkedIndex := -1, -1
			for index, item := range items {
				switch item.Key {
				case mainPath:
					mainIndex = index
				case linkedPath:
					linkedIndex = index
				}
			}
			if linkedIndex == -1 || mainIndex == -1 || linkedIndex > mainIndex {
				t.Fatalf("picker items = %#v, want dated linked worktree before unranked main", items)
			}
			return linkedPath, nil
		},
	})

	if _, _, err := executeV2(root); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestNavigationRecencyFailureDoesNotBlockPath(t *testing.T) {
	repoPath := initV2Repo(t)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
		markVisited: func(string) error { return errors.New("state is read-only") },
	})

	stdout, stderr, err := executeV2(root, "cd", "app:")
	if err != nil {
		t.Fatalf("cd error = %v", err)
	}
	if stdout != canonicalV2Path(t, repoPath)+"\n" || !strings.Contains(stderr, "warning: recording navigation recency") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
}

func TestJSONNavigationDoesNotChangeRecency(t *testing.T) {
	repoPath := initV2Repo(t)
	writeV2Config(t, repoPath, "")
	marked := false
	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return false },
		markVisited: func(string) error {
			marked = true
			return nil
		},
	})

	if _, _, err := executeV2(root, "--json", "cd", "app:"); err != nil {
		t.Fatalf("cd --json error = %v", err)
	}
	if marked {
		t.Fatal("JSON navigation changed recency")
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
	runV2Git(t, repoPath, "worktree", "lock", "--reason", "active agent", secondPath)
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{
		getwd:       func() (string, error) { return repoPath, nil },
		interactive: func() bool { return true },
		pickMany: func(string, []picker.Item) ([]string, error) {
			return []string{canonicalV2Path(t, firstPath), canonicalV2Path(t, secondPath)}, nil
		},
	})

	_, _, err := executeV2(root, "rm", "--discard")
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("rm error = %v, want locked-worktree refusal", err)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("worktree was removed before preflight completed: %s: %v", path, err)
		}
	}
}

func TestRemoveDiscardRemovesUnregisteredNestedRepository(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/linked", linkedPath)
	nestedPath := filepath.Join(linkedPath, "nested")
	initV2RepoAt(t, nestedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "rm", "--discard", "feat/linked")
	if err != nil {
		t.Fatalf("rm --discard error = %v", err)
	}
	if _, err := os.Stat(linkedPath); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	runV2Git(t, repoPath, "show-ref", "--verify", "refs/heads/feat/linked")
}

func TestRemoveDiscardRemovesWorktreeWithStaleGitPointer(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/linked", linkedPath)
	if err := os.WriteFile(filepath.Join(linkedPath, ".git"), []byte("gitdir: /path/that/no-longer-exists\n"), 0644); err != nil {
		t.Fatal(err)
	}
	canonicalLinkedPath := canonicalV2Path(t, linkedPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "rm", "--discard", "feat/linked")
	if err != nil {
		t.Fatalf("rm --discard error = %v", err)
	}
	if _, err := os.Stat(linkedPath); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	if listing := v2GitOutput(t, repoPath, "worktree", "list", "--porcelain"); strings.Contains(listing, canonicalLinkedPath) {
		t.Fatalf("worktree registration remains:\n%s", listing)
	}
}

func TestRemoveDiscardRepairsMisdirectedGitPointer(t *testing.T) {
	repoPath := initV2Repo(t)
	firstPath := filepath.Join(t.TempDir(), "first")
	secondPath := filepath.Join(t.TempDir(), "second")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/first", firstPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/second", secondPath)
	secondGitFile, err := os.ReadFile(filepath.Join(secondPath, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(firstPath, ".git"), secondGitFile, 0644); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err = executeV2(root, "rm", "--discard", "feat/first")
	if err != nil {
		t.Fatalf("rm --discard error = %v", err)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("first worktree remains: %v", err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("second worktree was removed: %v", err)
	}
}

func TestRemoveDiscardDoesNotFollowStaleGitFileSymlink(t *testing.T) {
	repoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/linked", linkedPath)
	externalPath := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(externalPath, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(linkedPath, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalPath, filepath.Join(linkedPath, ".git")); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	_, _, err := executeV2(root, "rm", "--discard", "feat/linked")
	if err != nil {
		t.Fatalf("rm --discard error = %v", err)
	}
	contents, err := os.ReadFile(externalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "outside" {
		t.Fatalf("external file = %q, want unchanged", contents)
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

func TestRemoveOlderThanIgnoresMergeStateAndKeepsBranches(t *testing.T) {
	repoPath := initV2Repo(t)
	oldPath := filepath.Join(t.TempDir(), "old")
	recentPath := filepath.Join(t.TempDir(), "recent")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/old", oldPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/recent", recentPath)
	if err := os.WriteFile(filepath.Join(oldPath, "unmerged.txt"), []byte("keep the commit"), 0644); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, oldPath, "add", "unmerged.txt")
	runV2Git(t, oldPath, "commit", "-m", "unmerged work")
	oldCreatedAt := time.Now().Add(-15 * 24 * time.Hour)
	recentCreatedAt := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(filepath.Join(oldPath, ".git"), oldCreatedAt, oldCreatedAt); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(recentPath, ".git"), recentCreatedAt, recentCreatedAt); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "rm", "--older-than", "14d", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if !strings.Contains(stdout, "feat/old") || !strings.Contains(stdout, "older than 14d") || !strings.Contains(stdout, "branches kept") || strings.Contains(stdout, "feat/recent") {
		t.Fatalf("dry-run stdout = %q", stdout)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("dry-run removed old worktree: %v", err)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err = executeV2(root, "rm", "--older-than", "14d")
	if err != nil {
		t.Fatalf("remove old error = %v", err)
	}
	if !strings.Contains(stdout, "feat/old") || !strings.Contains(stdout, "branches kept") {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old worktree remains: %v", err)
	}
	if _, err := os.Stat(recentPath); err != nil {
		t.Fatalf("recent worktree removed: %v", err)
	}
	runV2Git(t, repoPath, "show-ref", "--verify", "refs/heads/feat/old")
}

func TestRemoveOlderThanRequiresDiscardButNeverOverridesLocks(t *testing.T) {
	repoPath := initV2Repo(t)
	dirtyPath := filepath.Join(t.TempDir(), "dirty")
	lockedPath := filepath.Join(t.TempDir(), "locked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/dirty-old", dirtyPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/locked-old", lockedPath)
	if err := os.WriteFile(filepath.Join(dirtyPath, "uncommitted.txt"), []byte("discard me"), 0644); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, repoPath, "worktree", "lock", "--reason", "active agent", lockedPath)
	createdAt := time.Now().Add(-15 * 24 * time.Hour)
	for _, path := range []string{dirtyPath, lockedPath} {
		if err := os.Chtimes(filepath.Join(path, ".git"), createdAt, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, stderr, err := executeV2(root, "rm", "--older-than", "14d", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if strings.Contains(stdout, "feat/dirty-old") || !strings.Contains(stderr, "Skipped 1 dirty") || !strings.Contains(stderr, "1 locked") || !strings.Contains(stderr, "--discard") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err = executeV2(root, "rm", "--older-than", "14d", "--discard")
	if err != nil {
		t.Fatalf("discard error = %v", err)
	}
	if !strings.Contains(stdout, "feat/dirty-old") {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err := os.Stat(dirtyPath); !os.IsNotExist(err) {
		t.Fatalf("dirty worktree remains: %v", err)
	}
	if _, err := os.Stat(lockedPath); err != nil {
		t.Fatalf("locked worktree removed: %v", err)
	}
	runV2Git(t, repoPath, "show-ref", "--verify", "refs/heads/feat/dirty-old")
}

func TestRemoveOlderThanDiscardRemovesUnregisteredNestedRepository(t *testing.T) {
	repoPath := initV2Repo(t)
	oldPath := filepath.Join(t.TempDir(), "old")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/old", oldPath)
	initV2RepoAt(t, filepath.Join(oldPath, "nested"))
	createdAt := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(oldPath, ".git"), createdAt, createdAt); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, stderr, err := executeV2(root, "rm", "--older-than", "14d", "--discard")
	if err != nil {
		t.Fatalf("rm --older-than --discard error = %v", err)
	}
	if !strings.Contains(stdout, "feat/old") || strings.Contains(stderr, "unsafe") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
}

func TestRemoveOlderThanDiscardRemovesWorktreeWithStaleGitPointer(t *testing.T) {
	repoPath := initV2Repo(t)
	oldPath := filepath.Join(t.TempDir(), "old")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/old", oldPath)
	if err := os.WriteFile(filepath.Join(oldPath, ".git"), []byte("gitdir: /path/that/no-longer-exists\n"), 0644); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(oldPath, ".git"), createdAt, createdAt); err != nil {
		t.Fatal(err)
	}
	canonicalOldPath := canonicalV2Path(t, oldPath)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, stderr, err := executeV2(root, "rm", "--older-than", "14d", "--discard")
	if err != nil {
		t.Fatalf("rm --older-than --discard error = %v", err)
	}
	if !strings.Contains(stdout, "feat/old") || strings.Contains(stderr, "unsafe") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	if listing := v2GitOutput(t, repoPath, "worktree", "list", "--porcelain"); strings.Contains(listing, canonicalOldPath) {
		t.Fatalf("worktree registration remains:\n%s", listing)
	}
}

func TestRemoveOlderThanScansEveryConfiguredRepository(t *testing.T) {
	currentRepo := initV2Repo(t)
	otherRepo := initV2Repo(t)
	oldPath := filepath.Join(t.TempDir(), "other-old")
	runV2Git(t, otherRepo, "worktree", "add", "-b", "feat/fleet-old", oldPath)
	createdAt := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(oldPath, ".git"), createdAt, createdAt); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, currentRepo, strings.Join([]string{
		"  - path: " + otherRepo,
		"    name: other",
		"    default_branch: main",
		"",
	}, "\n"))

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return currentRepo, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "rm", "--older-than", "14d")
	if err != nil {
		t.Fatalf("fleet cleanup error = %v", err)
	}
	if !strings.Contains(stdout, "other:feat/fleet-old") {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old worktree in other repository remains: %v", err)
	}
	runV2Git(t, otherRepo, "show-ref", "--verify", "refs/heads/feat/fleet-old")
}

func TestRemoveOlderThanNeverRemovesNestedWorktrees(t *testing.T) {
	repoPath := initV2Repo(t)
	parentPath := filepath.Join(t.TempDir(), "parent")
	childPath := filepath.Join(parentPath, "nested")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/parent", parentPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/child", childPath)
	oldCreatedAt := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(parentPath, ".git"), oldCreatedAt, oldCreatedAt); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, stderr, err := executeV2(root, "rm", "--older-than", "14d", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if !strings.Contains(stdout, "No worktrees") || !strings.Contains(stderr, "Skipped 1 dirty") || strings.Contains(stderr, "contains registered worktree") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, stderr, err = executeV2(root, "rm", "--older-than", "14d", "--discard")
	if err != nil {
		t.Fatalf("cleanup error = %v", err)
	}
	if !strings.Contains(stdout, "No worktrees") || !strings.Contains(stderr, "contains registered worktree") || !strings.Contains(stderr, "1 unsafe") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
	for _, path := range []string{parentPath, childPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("nested cleanup removed %s: %v", path, err)
		}
	}
}

func TestRemoveMissingPrunesRegistrationsAndKeepsBranches(t *testing.T) {
	repoPath := initV2Repo(t)
	missingPath := filepath.Join(t.TempDir(), "missing")
	lockedPath := filepath.Join(t.TempDir(), "locked-missing")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/gone", missingPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/locked-gone", lockedPath)
	runV2Git(t, repoPath, "worktree", "lock", "--reason", "keep registration", lockedPath)
	canonicalMissingPath := canonicalV2Path(t, missingPath)
	canonicalLockedPath := canonicalV2Path(t, lockedPath)
	if err := os.RemoveAll(missingPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(lockedPath); err != nil {
		t.Fatal(err)
	}
	writeV2Config(t, repoPath, "")

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "rm", "--missing", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if !strings.Contains(stdout, "feat/gone") || strings.Contains(stdout, "feat/locked-gone") || !strings.Contains(stdout, "branches kept") {
		t.Fatalf("dry-run stdout = %q", stdout)
	}
	if listing := v2GitOutput(t, repoPath, "worktree", "list", "--porcelain"); !strings.Contains(listing, canonicalMissingPath) {
		t.Fatalf("dry-run pruned registration:\n%s", listing)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err = executeV2(root, "rm", "--missing")
	if err != nil {
		t.Fatalf("remove missing error = %v", err)
	}
	if !strings.Contains(stdout, "feat/gone") || !strings.Contains(stdout, "branches kept") {
		t.Fatalf("stdout = %q", stdout)
	}
	if listing := v2GitOutput(t, repoPath, "worktree", "list", "--porcelain"); strings.Contains(listing, canonicalMissingPath) {
		t.Fatalf("registration remains:\n%s", listing)
	}
	if listing := v2GitOutput(t, repoPath, "worktree", "list", "--porcelain"); !strings.Contains(listing, canonicalLockedPath) {
		t.Fatalf("locked registration was pruned:\n%s", listing)
	}
	runV2Git(t, repoPath, "show-ref", "--verify", "refs/heads/feat/gone")
	runV2Git(t, repoPath, "show-ref", "--verify", "refs/heads/feat/locked-gone")
}

func TestRemoveBulkCleanupRejectsAmbiguousFlags(t *testing.T) {
	repoPath := initV2Repo(t)
	writeV2Config(t, repoPath, "")
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"rm", "--older-than", "later"}, want: "invalid --older-than"},
		{args: []string{"rm", "--older-than", "0d"}, want: "positive duration"},
		{args: []string{"rm", "--merged", "--older-than", "14d"}, want: "cannot be used together"},
		{args: []string{"rm", "--missing", "--older-than", "14d"}, want: "cannot be used together"},
		{args: []string{"rm", "--missing", "--discard"}, want: "--discard"},
		{args: []string{"rm", "--dry-run"}, want: "requires"},
		{args: []string{"rm", "--missing", "feat/anything"}, want: "does not accept selectors"},
		{args: []string{"--null", "rm", "--missing"}, want: "single-worktree"},
	}
	for _, test := range tests {
		root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
		_, _, err := executeV2(root, test.args...)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("grove %s error = %v, want %q", strings.Join(test.args, " "), err, test.want)
		}
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
	if !strings.Contains(stdout, `"main": true`) {
		t.Fatalf("JSON omitted main worktree: %s", stdout)
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

func TestListShowsOnlyRepositoriesWithLinkedWorktrees(t *testing.T) {
	repoPath := initV2Repo(t)
	emptyRepoPath := initV2Repo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/visible", linkedPath)
	writeV2Config(t, repoPath, strings.Join([]string{
		"  - path: " + emptyRepoPath,
		"    name: empty",
		"    default_branch: main",
		"",
	}, "\n"))
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if strings.Contains(stdout, "[main]") || strings.Contains(stdout, "── main") {
		t.Fatalf("stdout = %q, contains redundant main worktree", stdout)
	}
	if !strings.Contains(stdout, "└── feat/visible") {
		t.Fatalf("stdout = %q, want linked worktree", stdout)
	}
	if strings.Contains(stdout, canonicalV2Path(t, emptyRepoPath)) {
		t.Fatalf("stdout = %q, contains repository without linked worktrees", stdout)
	}
}

func TestListSeparatesMultiProfileRepositoryAndProfileNames(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "browseros")
	initV2RepoAt(t, repoPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/visible", filepath.Join(t.TempDir(), "linked"))
	writeV2Config(t, "", strings.Join([]string{
		"  - path: " + repoPath,
		"    name: agent",
		"    default_branch: main",
		"    workdir: packages/agent",
		"  - path: " + repoPath,
		"    name: main",
		"    default_branch: main",
		"",
	}, "\n"))

	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err := executeV2(root, "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !strings.Contains(stdout, "browseros (agent, main)") || strings.Contains(stdout, "main (agent)") {
		t.Fatalf("stdout = %q", stdout)
	}

	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})
	stdout, _, err = executeV2(root, "cd", "browseros:")
	if err != nil {
		t.Fatalf("canonical selector error = %v", err)
	}
	if stdout != canonicalV2Path(t, repoPath)+"\n" {
		t.Fatalf("stdout = %q", stdout)
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

func TestListSortsLinkedWorktreesNewestFirst(t *testing.T) {
	repoPath := initV2Repo(t)
	worktreeRoot := t.TempDir()
	olderPath := filepath.Join(worktreeRoot, "a-older")
	newerPath := filepath.Join(worktreeRoot, "z-newer")
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/older", olderPath)
	runV2Git(t, repoPath, "worktree", "add", "-b", "feat/newer", newerPath)

	now := time.Now()
	for path, createdAt := range map[string]time.Time{
		olderPath: now.Add(-3 * time.Hour),
		newerPath: now.Add(-30 * time.Minute),
	} {
		if err := os.Chtimes(filepath.Join(path, ".git"), createdAt, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }, interactive: func() bool { return false }})

	stdout, _, err := executeV2(root, "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	newerIndex := strings.Index(stdout, "feat/newer")
	olderIndex := strings.Index(stdout, "feat/older")
	if newerIndex == -1 || olderIndex == -1 || newerIndex > olderIndex {
		t.Fatalf("stdout = %q, want newer worktree before older worktree", stdout)
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
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
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
