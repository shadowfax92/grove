package cmd

import (
	"strings"
	"testing"
	"time"

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

func TestShadowRemovePickerSortsByRecentToggleThenActivity(t *testing.T) {
	base := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	sessions := []shadow.Session{
		{
			SessionName:   "gs/sh/old-toggle",
			Type:          "sh",
			OpenedAt:      base.Add(-72 * time.Hour),
			LastToggledAt: base.Add(-6 * time.Hour),
			LastActiveAt:  base.Add(-10 * time.Minute),
		},
		{
			SessionName:   "gs/vim/new-toggle",
			Type:          "vim",
			OpenedAt:      base.Add(-48 * time.Hour),
			LastToggledAt: base.Add(-5 * time.Minute),
			LastActiveAt:  base.Add(-2 * time.Hour),
		},
		{
			SessionName:  "gs/sh/activity-only",
			Type:         "sh",
			OpenedAt:     base.Add(-24 * time.Hour),
			LastActiveAt: base.Add(-1 * time.Minute),
		},
	}

	got := sortShadowSessionsForRemoval(sessions)

	if got[0].SessionName != "gs/vim/new-toggle" {
		t.Fatalf("first sorted session = %q, want newest toggle", got[0].SessionName)
	}
	if got[1].SessionName != "gs/sh/old-toggle" {
		t.Fatalf("second sorted session = %q, want older toggle before activity fallback", got[1].SessionName)
	}
}

func TestRenderShadowRemovePickerInputIncludesMetadataColumns(t *testing.T) {
	base := time.Now().UTC()
	lookup := map[string]workspaces.RemoveTarget{}

	out := renderShadowRemovePickerInput([]shadow.Session{{
		SessionName:   "gs/sh/101",
		Type:          "sh",
		ParentPane:    "%101",
		OpenedAt:      base.Add(-48 * time.Hour),
		LastToggledAt: base.Add(-2 * time.Hour),
		LastActiveAt:  base.Add(-30 * time.Minute),
		Orphan:        false,
	}}, lookup)

	fields := strings.Split(out, "\t")
	if len(fields) != 7 {
		t.Fatalf("rendered shadow picker row field count = %d, want 7: %q", len(fields), out)
	}
	wantFields := []string{"gs/sh/101", "gs/sh/101", "sh", "parent %101"}
	for i, want := range wantFields {
		if strings.TrimSpace(fields[i]) != want {
			t.Fatalf("field %d = %q, want %q in row %q", i, fields[i], want, out)
		}
	}
	for i, wantPrefix := range map[int]string{4: "opened ", 5: "toggled ", 6: "active "} {
		if !strings.HasPrefix(fields[i], wantPrefix) {
			t.Fatalf("field %d = %q, want prefix %q in row %q", i, fields[i], wantPrefix, out)
		}
	}
	target, ok := lookup["gs/sh/101"]
	if !ok {
		t.Fatal("lookup missing rendered shadow target")
	}
	if target.Kind != workspaces.RemoveUnmanagedSession || target.SessionName != "gs/sh/101" {
		t.Fatalf("lookup target = %#v, want unmanaged shadow session", target)
	}
}
