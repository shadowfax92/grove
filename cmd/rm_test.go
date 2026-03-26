package cmd

import (
	"testing"

	"grove/internal/state"
)

func TestResolveRemoveTargetsStayStableAcrossStateMutation(t *testing.T) {
	mgr := &state.StateManager{}
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets, err := resolveRemoveTargets(mgr, st, nil, []string{"g/alpha", "g/gamma"})
	if err != nil {
		t.Fatalf("resolveRemoveTargets returned error: %v", err)
	}

	mgr.RemoveWorkspace(st, targets[0].session)

	if got, want := targets[1].session, "g/gamma"; got != want {
		t.Fatalf("second target session changed after state mutation: got %q want %q", got, want)
	}
	if got, want := targets[1].workspace.Name, "gamma"; got != want {
		t.Fatalf("second target name changed after state mutation: got %q want %q", got, want)
	}
}

func TestResolveRemoveTargetsRemoveOnlySelectedSessions(t *testing.T) {
	mgr := &state.StateManager{}
	st := &state.State{
		Workspaces: []state.Workspace{
			{Name: "alpha", SessionName: "g/alpha"},
			{Name: "beta", SessionName: "g/beta"},
			{Name: "gamma", SessionName: "g/gamma"},
		},
	}

	targets, err := resolveRemoveTargets(mgr, st, nil, []string{"g/alpha", "g/gamma"})
	if err != nil {
		t.Fatalf("resolveRemoveTargets returned error: %v", err)
	}

	for _, t := range targets {
		mgr.RemoveWorkspace(st, t.session)
	}

	if got, want := len(st.Workspaces), 1; got != want {
		t.Fatalf("unexpected workspace count after removal: got %d want %d", got, want)
	}
	if got, want := st.Workspaces[0].SessionName, "g/beta"; got != want {
		t.Fatalf("unexpected remaining workspace: got %q want %q", got, want)
	}
}
