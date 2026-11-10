package cmd

import (
	"encoding/json"
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
