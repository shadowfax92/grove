package cmd

import (
	"reflect"
	"testing"
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
