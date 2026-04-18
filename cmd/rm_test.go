package cmd

import (
	"testing"

	"grove/internal/shadow"
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

func TestRemovePickerTargetsHideUnmanagedByDefault(t *testing.T) {
	inv := &workspaces.Inventory{
		Managed: []workspaces.ManagedEntry{
			{Workspace: state.Workspace{Name: "mono/feat-auth", SessionName: "g/mono/feat-auth"}},
		},
		Unmanaged: []workspaces.UnmanagedSession{
			{SessionName: shadow.Name("%101", "vim")},
			{SessionName: "scratch"},
		},
	}

	got := removePickerTargets(inv, false)
	if gotLen, wantLen := len(got), 1; gotLen != wantLen {
		t.Fatalf("len(removePickerTargets(false)) = %d, want %d", gotLen, wantLen)
	}
	if got[0].SessionName != "g/mono/feat-auth" {
		t.Fatalf("removePickerTargets(false)[0] = %q, want managed workspace", got[0].SessionName)
	}
}

func TestRemovePickerTargetsIncludeUnmanagedWhenExpanded(t *testing.T) {
	inv := &workspaces.Inventory{
		Managed: []workspaces.ManagedEntry{
			{Workspace: state.Workspace{Name: "mono/feat-auth", SessionName: "g/mono/feat-auth"}},
		},
		Unmanaged: []workspaces.UnmanagedSession{
			{SessionName: shadow.Name("%101", "vim")},
			{SessionName: "scratch"},
		},
	}

	got := removePickerTargets(inv, true)
	if gotLen, wantLen := len(got), 3; gotLen != wantLen {
		t.Fatalf("len(removePickerTargets(true)) = %d, want %d", gotLen, wantLen)
	}
}

func TestShouldExpandRemovePickerOnlyForShadowQuery(t *testing.T) {
	if shouldExpandRemovePicker("") {
		t.Fatal("shouldExpandRemovePicker(\"\") = true, want false")
	}
	if shouldExpandRemovePicker("notes") {
		t.Fatal("shouldExpandRemovePicker(\"notes\") = true, want false")
	}
	if !shouldExpandRemovePicker("gs/") {
		t.Fatal("shouldExpandRemovePicker(\"gs/\") = false, want true")
	}
}
