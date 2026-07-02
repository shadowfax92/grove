package cmd

import (
	"encoding/json"
	"reflect"
	"testing"

	"grove/internal/state"
)

func TestBuildSessionTreeRowsNestedSessions(t *testing.T) {
	rows := buildSessionTreeRows([]string{
		"g/patches/feat/mar24-new-dev-cli",
		"g/position-exercise",
		"g/SHIP",
		"g/CLIs",
	})

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.label)
	}

	want := []string{
		"├── patches",
		"│   └── feat",
		"│       └── mar24-new-dev-cli",
		"├── position-exercise",
		"├── SHIP",
		"└── CLIs",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tree labels:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildSessionTreeRowsKeepsSessionOnBranchNode(t *testing.T) {
	rows := buildSessionTreeRows([]string{
		"g/foo",
		"g/foo/bar",
	})
	if len(rows) != 2 {
		t.Fatalf("unexpected row count: got %d want 2", len(rows))
	}

	if got, want := rows[0].sessionName, "g/foo"; got != want {
		t.Fatalf("branch node session changed: got %q want %q", got, want)
	}
	if got, want := rows[1].sessionName, "g/foo/bar"; got != want {
		t.Fatalf("leaf node session changed: got %q want %q", got, want)
	}
}

func TestBuildSessionTreeRowsBranchTargetsMostRecentDescendant(t *testing.T) {
	rows := buildSessionTreeRows([]string{
		"g/foo/bar",
		"g/foo/baz",
		"g/qux",
	})

	if got, want := rows[0].defaultTarget, "g/foo/bar"; got != want {
		t.Fatalf("branch target changed: got %q want %q", got, want)
	}
	if got, want := rows[0].leafCount, 2; got != want {
		t.Fatalf("branch leaf count changed: got %d want %d", got, want)
	}
}

func TestListWorkspaceJSONContainsStateFields(t *testing.T) {
	workspaces := []state.Workspace{{
		Name:         "mono/feat-json",
		Repo:         "mono",
		RepoPath:     "/repo",
		WorktreePath: "/worktrees/mono/feat-json",
		Branch:       "feat/json",
		SessionName:  "g/mono/feat/json",
		CreatedAt:    "2026-07-01T18:06:00Z",
		LastUsedAt:   "2026-07-01T18:07:00Z",
		Path:         "/worktrees/mono/feat-json/packages/app",
		Type:         "worktree",
	}}

	data, err := json.Marshal(listWorkspaceJSON(workspaces))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got []map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("json length = %d, want 1", len(got))
	}

	want := map[string]string{
		"name":          "mono/feat-json",
		"repo":          "mono",
		"repo_path":     "/repo",
		"branch":        "feat/json",
		"worktree_path": "/worktrees/mono/feat-json",
		"session_name":  "g/mono/feat/json",
		"created_at":    "2026-07-01T18:06:00Z",
		"last_used_at":  "2026-07-01T18:07:00Z",
	}
	if len(got[0]) != len(want) {
		t.Fatalf("field count = %d, want %d: %v", len(got[0]), len(want), got[0])
	}
	for key, value := range want {
		if got[0][key] != value {
			t.Fatalf("%s = %q, want %q", key, got[0][key], value)
		}
	}
}
