package cmd

import (
	"testing"

	"grove/internal/state"
	"grove/internal/workspaces"
)

func TestRemoveManagedEntriesRemovesSelectedWorkspaces(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets := []workspaces.RemoveTarget{
		{Workspace: state.Workspace{Name: "alpha", SessionName: "g/alpha"}, SessionName: "g/alpha"},
		{Workspace: state.Workspace{Name: "gamma", SessionName: "g/gamma"}, SessionName: "g/gamma"},
	}

	workspaces.RemoveManagedEntries(st, targets)

	if got, want := len(st.Workspaces), 1; got != want {
		t.Fatalf("workspace count after removal = %d, want %d", got, want)
	}
	if got, want := st.Workspaces[0].SessionName, "g/beta"; got != want {
		t.Fatalf("remaining workspace = %q, want %q", got, want)
	}
}

func TestRemoveManagedEntriesLeavesTargetValuesUntouched(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets := []workspaces.RemoveTarget{
		{Workspace: state.Workspace{Name: "alpha", SessionName: "g/alpha"}, SessionName: "g/alpha"},
		{Workspace: state.Workspace{Name: "gamma", SessionName: "g/gamma"}, SessionName: "g/gamma"},
	}

	workspaces.RemoveManagedEntries(st, targets[:1])

	if got, want := targets[1].SessionName, "g/gamma"; got != want {
		t.Fatalf("second target session changed: got %q want %q", got, want)
	}
	if got, want := targets[1].Workspace.Name, "gamma"; got != want {
		t.Fatalf("second target name changed: got %q want %q", got, want)
	}
}
