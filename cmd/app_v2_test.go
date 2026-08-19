package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	runV2Git(t, path, "init", "-b", "main")
	runV2Git(t, path, "config", "user.name", "Grove Test")
	runV2Git(t, path, "config", "user.email", "grove@example.test")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	runV2Git(t, path, "add", "README")
	runV2Git(t, path, "commit", "-m", "initial")
	return path
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
