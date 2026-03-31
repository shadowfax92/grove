package cmd

import (
	"testing"

	"grove/internal/state"
	"grove/internal/workspaces"
)

func TestRemoveManagedTargetsRemovesOnlyManagedSelections(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets := []workspaces.RemoveTarget{
		{
			Kind:        workspaces.RemoveManagedWorkspace,
			Workspace:   state.Workspace{Name: "alpha", SessionName: "g/alpha"},
			SessionName: "g/alpha",
		},
		{
			Kind:        workspaces.RemoveUnmanagedSession,
			SessionName: "scratch",
		},
		{
			Kind:        workspaces.RemoveManagedWorkspace,
			Workspace:   state.Workspace{Name: "gamma", SessionName: "g/gamma"},
			SessionName: "g/gamma",
		},
	}

	workspaces.RemoveManagedEntries(st, targets)

	if got, want := len(st.Workspaces), 1; got != want {
		t.Fatalf("unexpected workspace count after removal: got %d want %d", got, want)
	}
	if got, want := st.Workspaces[0].SessionName, "g/beta"; got != want {
		t.Fatalf("unexpected remaining workspace: got %q want %q", got, want)
	}
}

func TestRemoveManagedTargetsLeavesTargetValuesUntouched(t *testing.T) {
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets := []workspaces.RemoveTarget{
		{
			Kind:        workspaces.RemoveManagedWorkspace,
			Workspace:   state.Workspace{Name: "alpha", SessionName: "g/alpha"},
			SessionName: "g/alpha",
		},
		{
			Kind:        workspaces.RemoveManagedWorkspace,
			Workspace:   state.Workspace{Name: "gamma", SessionName: "g/gamma"},
			SessionName: "g/gamma",
		},
	}

	workspaces.RemoveManagedEntries(st, targets[:1])

	if got, want := targets[1].SessionName, "g/gamma"; got != want {
		t.Fatalf("second target session changed after state mutation: got %q want %q", got, want)
	}
	if got, want := targets[1].Workspace.Name, "gamma"; got != want {
		t.Fatalf("second target name changed after state mutation: got %q want %q", got, want)
	}
}
