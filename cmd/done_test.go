package cmd

import (
	"testing"

	"grove/internal/state"
)

func TestFindNextSessionPrefersLastActive(t *testing.T) {
	st := &state.State{
		LastActive: "g/notes",
		Workspaces: []state.Workspace{
			{SessionName: "g/mono/feat-auth", LastUsedAt: "2026-03-26T18:00:00Z"},
			{SessionName: "g/notes", LastUsedAt: "2026-03-26T17:00:00Z"},
		},
	}

	if got, want := findNextSession(st, "g/mono/feat-auth"), "g/notes"; got != want {
		t.Fatalf("findNextSession() = %q, want %q", got, want)
	}
}

func TestFindNextSessionFallsBackToMostRecent(t *testing.T) {
	st := &state.State{
		LastActive: "g/mono/feat-auth",
		Workspaces: []state.Workspace{
			{SessionName: "g/mono/feat-auth", LastUsedAt: "2026-03-26T18:00:00Z"},
			{SessionName: "g/notes", LastUsedAt: "2026-03-26T17:00:00Z"},
			{SessionName: "g/ship", LastUsedAt: "2026-03-26T19:00:00Z"},
		},
	}

	if got, want := findNextSession(st, "g/mono/feat-auth"), "g/ship"; got != want {
		t.Fatalf("findNextSession() = %q, want %q", got, want)
	}
}
