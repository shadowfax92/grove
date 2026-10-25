package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestNewLinksWorktreeAndMainCheckoutBeforeSetup(t *testing.T) {
	home := llmTestHome(t)
	repoPath := filepath.Join(home, "code", "app")
	initV2RepoAt(t, repoPath)
	writeLLMConfig(t, home, "llm:\n  root: ~/llm\n  aliases: [.scratch]\n", "  - path: "+repoPath+"\n    name: app\n    default_branch: main\n    setup:\n      - readlink .llm > setup-saw\n")

	stdout, stderr, err := executeV2(newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }}), "--json", "new", "auth")
	if err != nil {
		t.Fatalf("new error = %v\n%s", err, stderr)
	}
	var output newOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "llm", "code", "app")
	if output.LLM != store {
		t.Fatalf("json llm = %q, want %q", output.LLM, store)
	}
	for _, checkout := range []string{output.Path, repoPath} {
		for _, name := range []string{".llm", ".scratch"} {
			if target, _ := os.Readlink(filepath.Join(checkout, name)); target != store {
				t.Fatalf("%s/%s -> %q, want %q", checkout, name, target, store)
			}
		}
	}
	if saw, _ := os.ReadFile(filepath.Join(output.Path, "setup-saw")); strings.TrimSpace(string(saw)) != store {
		t.Fatalf("setup saw .llm -> %q, want the link to exist before setup", saw)
	}
}

func TestRemovingWorktreeKeepsItsStore(t *testing.T) {
	home := llmTestHome(t)
	repoPath := filepath.Join(home, "code", "app")
	initV2RepoAt(t, repoPath)
	writeLLMConfig(t, home, "llm:\n  root: ~/llm\n  aliases: [.scratch]\n", "  - path: "+repoPath+"\n    name: app\n    default_branch: main\n")
	getwd := func() (string, error) { return repoPath, nil }

	stdout, stderr, err := executeV2(newRootCommand(commandDependencies{getwd: getwd}), "new", "auth")
	if err != nil {
		t.Fatalf("new error = %v\n%s", err, stderr)
	}
	worktree := strings.TrimSpace(stdout)
	note := filepath.Join(worktree, ".llm", "plan.md")
	if err := os.WriteFile(note, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := executeV2(newRootCommand(commandDependencies{getwd: getwd}), "--no-input", "rm", "feat/auth"); err != nil {
		t.Fatalf("rm error = %v\n%s", err, stderr)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "llm", "code", "app", "plan.md")); err != nil || string(data) != "keep" {
		t.Fatalf("store content after rm = %q, %v", data, err)
	}
}

func TestLLMSyncSharesKeyedStoreAndArchivesItOnce(t *testing.T) {
	home := llmTestHome(t)
	first := filepath.Join(home, "code", "m1")
	second := filepath.Join(home, "code", "m2")
	optedOut := filepath.Join(home, "code", "skip")
	for _, path := range []string{first, second, optedOut} {
		initV2RepoAt(t, path)
	}
	writeLLMConfig(t, home, "llm:\n  root: ~/llm\n", ""+
		"  - path: "+first+"\n    name: m1\n    llm: browseros\n"+
		"  - path: "+second+"\n    name: m2\n    llm: browseros\n"+
		"  - path: "+optedOut+"\n    name: skip\n    llm: false\n")
	command := func() *cobra.Command {
		return newRootCommand(commandDependencies{getwd: func() (string, error) { return home, nil }})
	}

	if _, stderr, err := executeV2(command(), "llm", "sync"); err != nil {
		t.Fatalf("sync error = %v\n%s", err, stderr)
	}
	store := filepath.Join(home, "llm", "browseros")
	for _, checkout := range []string{first, second} {
		if target, _ := os.Readlink(filepath.Join(checkout, ".llm")); target != store {
			t.Fatalf("%s/.llm -> %q, want shared %q", checkout, target, store)
		}
	}
	if _, err := os.Lstat(filepath.Join(optedOut, ".llm")); !os.IsNotExist(err) {
		t.Fatalf("opted-out repository was linked: %v", err)
	}

	idle := filepath.Join(store, "old-plan.md")
	if err := os.WriteFile(idle, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(idle, old, old); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeV2(command(), "--json", "llm", "archive")
	if err != nil {
		t.Fatalf("archive error = %v\n%s", err, stderr)
	}
	var output llmArchiveOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Stores) != 1 || len(output.Stores[0].Archived) != 1 || output.Stores[0].Archived[0].Name != "old-plan.md" {
		t.Fatalf("archive output = %+v, want the shared store archived once", output)
	}
	if _, err := os.Stat(filepath.Join(store, "archive", "old-plan.md")); err != nil {
		t.Fatalf("idle entry not archived: %v", err)
	}
}

func TestLLMPrintsStorePathFromInsideWorktree(t *testing.T) {
	home := llmTestHome(t)
	repoPath := filepath.Join(home, "code", "app")
	initV2RepoAt(t, repoPath)
	subdirectory := filepath.Join(repoPath, "pkg")
	if err := os.MkdirAll(subdirectory, 0755); err != nil {
		t.Fatal(err)
	}
	writeLLMConfig(t, home, "llm:\n  root: ~/llm\n  dir: .context\n", "  - path: "+repoPath+"\n    name: app\n")

	stdout, stderr, err := executeV2(newRootCommand(commandDependencies{getwd: func() (string, error) { return subdirectory, nil }}), "llm")
	if err != nil {
		t.Fatalf("llm error = %v\n%s", err, stderr)
	}
	store := filepath.Join(home, "llm", "code", "app")
	if stdout != store+"\n" {
		t.Fatalf("stdout = %q, want %q", stdout, store)
	}
	if target, _ := os.Readlink(filepath.Join(repoPath, ".context")); target != store {
		t.Fatalf("configured anchor -> %q, want %q", target, store)
	}
}

func TestLLMStaysOffWithoutRoot(t *testing.T) {
	home := llmTestHome(t)
	repoPath := filepath.Join(home, "code", "app")
	initV2RepoAt(t, repoPath)
	writeLLMConfig(t, home, "", "  - path: "+repoPath+"\n    name: app\n")
	getwd := func() (string, error) { return repoPath, nil }

	if _, _, err := executeV2(newRootCommand(commandDependencies{getwd: getwd}), "llm"); err == nil || !strings.Contains(err.Error(), "llm.root") {
		t.Fatalf("llm without root error = %v, want llm.root hint", err)
	}
	stdout, stderr, err := executeV2(newRootCommand(commandDependencies{getwd: getwd}), "new", "auth")
	if err != nil {
		t.Fatalf("new error = %v\n%s", err, stderr)
	}
	if _, err := os.Lstat(filepath.Join(strings.TrimSpace(stdout), ".llm")); !os.IsNotExist(err) {
		t.Fatalf("new linked .llm with llm stores off: %v", err)
	}
}

// llmTestHome isolates HOME, state and Git config so a user-level gitignore
// cannot hide whether grove excluded the link itself.
func llmTestHome(t *testing.T) string {
	t.Helper()
	home := canonicalV2Path(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GROVE_PANE_REPORT", "0")
	return home
}

func writeLLMConfig(t *testing.T, home, llmBlock, repos string) {
	t.Helper()
	path := filepath.Join(home, ".config", "grove", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(llmBlock+"repos:\n"+repos), 0644); err != nil {
		t.Fatal(err)
	}
}
