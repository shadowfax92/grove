package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSignalsAreAdditiveAndOptIn(t *testing.T) {
	path := initV2Repo(t)
	writeV2Config(t, path, "")
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return path, nil }, interactive: func() bool { return false }})
	plain, _, err := executeV2(root, "--json", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"last_active", "activity_error", "in_use", "merged"} {
		if strings.Contains(plain, `"`+field+`"`) {
			t.Fatalf("plain list computed %s: %s", field, plain)
		}
	}
	root = newRootCommand(commandDependencies{getwd: func() (string, error) { return path, nil }, interactive: func() bool { return false }})
	status, _, err := executeV2(root, "--json", "list", "--status")
	if err != nil {
		t.Fatal(err)
	}
	var document listDocument
	if err := json.Unmarshal([]byte(status), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || len(document.Repositories) != 1 || len(document.Repositories[0].Worktrees) != 1 {
		t.Fatalf("%s", status)
	}
	tree := document.Repositories[0].Worktrees[0]
	if tree.LastActive == nil || tree.ActivityError != "" || tree.InUse == nil || tree.Merged == nil {
		t.Fatalf("missing signals: %s", status)
	}
}

func TestListHumanSignalsAndForgeOptIn(t *testing.T) {
	path := initV2Repo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, path, "worktree", "add", "-b", "topic", linked)
	writeV2Config(t, path, "")
	dependencies := commandDependencies{getwd: func() (string, error) { return path, nil }, interactive: func() bool { return false }}
	human, _, err := executeV2(newRootCommand(dependencies), "list", "--status")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"last active ", "not in use", "merged: ancestor"} {
		if !strings.Contains(human, label) {
			t.Fatalf("missing %q: %s", label, human)
		}
	}
	// Both fixture tips are ancestors, so the optional flag never needs a network
	// request here. It must still imply status without an explicit --status flag.
	output, _, err := executeV2(newRootCommand(dependencies), "--json", "list", "--forge")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"last_active"`) || !strings.Contains(output, `"merged": true`) {
		t.Fatalf("forge did not imply status: %s", output)
	}
}
