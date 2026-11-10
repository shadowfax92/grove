package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"grove/internal/git"
	"grove/internal/trash"
)

func TestRemoveWaitKeepsOutputAndStore(t *testing.T) {
	repoPath := initV2Repo(t)
	linked := filepath.Join(repoPath, ".wt", "fixture")
	runV2Git(t, repoPath, "worktree", "add", "-b", "fixture", linked)
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "keep"), []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".llm", ".scratch"} {
		if err := os.Symlink(store, filepath.Join(linked, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return linked, nil }})
	stdout, _, err := executeV2(root, "--json", "rm", "--discard", "--wait", ".")
	if err != nil {
		t.Fatal(err)
	}
	var output removeOutput
	if err = json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if output.ReturnPath != canonicalV2Path(t, repoPath) || len(output.Removed) != 1 || len(output.PendingDeletion) != 0 {
		t.Fatalf("output: %s", stdout)
	}
	manager, err := trash.Default()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := manager.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending %v: %v", pending, err)
	}
	if _, err = os.Stat(filepath.Join(store, "keep")); err != nil {
		t.Fatal("store removed", err)
	}
}

func TestNextCommandRestartsInterruptedReaper(t *testing.T) {
	// Build before changing HOME so Go's normal cache is reused, never installed.
	binary := filepath.Join(t.TempDir(), "grove")
	build := exec.Command("go", "build", "-o", binary, "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	repoPath := initV2Repo(t)
	linked := filepath.Join(repoPath, ".wt", "fixture")
	runV2Git(t, repoPath, "worktree", "add", "-b", "fixture", linked)
	writeV2Config(t, repoPath, "")
	repo, err := git.OpenRepository(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	// Calling the Git API detaches but does not start the CLI reaper. A canceled
	// first reap leaves exactly the state a killed process would leave on disk.
	if err = repo.RemoveWorktree(linked, true); err != nil {
		t.Fatal(err)
	}
	manager, err := trash.Default()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = manager.Reap(ctx)
	pending, err := manager.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v %v", pending, err)
	}
	// A dry run must leave even an interrupted pending deletion untouched.
	preview := exec.Command(binary, "-C", repoPath, "rm", "--older-than", "1000d", "--dry-run=TRUE")
	if out, err := preview.CombinedOutput(); err != nil {
		t.Fatalf("preview: %s %v", out, err)
	}
	pending, err = manager.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("dry run started deletion: %v %v", pending, err)
	}
	// Keep the worker inside a root scan while more commands request work. The
	// inherited process lease must suppress every additional child at startup.
	gate := flock.New(filepath.Join(pending[0].Root, ".lock"))
	if err = gate.Lock(); err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	for range 4 {
		version := exec.Command(binary, "--version")
		if out, err := version.CombinedOutput(); err != nil {
			t.Fatalf("version: %s %v", out, err)
		}
	}
	processList, err := exec.Command("ps", "-axo", "command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(processList), "\n") {
		if strings.TrimSpace(line) == binary+" __grove-reap" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reaper processes: %d", count)
	}
	if err = gate.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-C", repoPath, "--json", "list")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("next command: %s %v", out, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err = manager.Pending()
		if err == nil && len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reaper did not resume: %v %v", pending, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTrashShowsPendingAndWaits(t *testing.T) {
	repoPath := initV2Repo(t)
	linked := filepath.Join(repoPath, ".wt", "fixture")
	runV2Git(t, repoPath, "worktree", "add", "-b", "fixture", linked)
	writeV2Config(t, repoPath, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }})
	stdout, _, err := executeV2(root, "--json", "rm", "--discard", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var output removeOutput
	if err = json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.PendingDeletion) != 1 {
		t.Fatalf("pending output: %s", stdout)
	}
	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }})
	stdout, _, err = executeV2(root, "--json", "trash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, output.PendingDeletion[0].ID) {
		t.Fatal(stdout)
	}
	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return repoPath, nil }})
	stdout, _, err = executeV2(root, "trash", "--wait")
	if err != nil || !strings.Contains(stdout, "No pending") {
		t.Fatalf("%s: %v", stdout, err)
	}
}
