package cmd

import (
	"reflect"
	"testing"

	"grove/internal/shadow"
	"grove/internal/tmux"
)

func TestVisibleJumpSessionsFiltersShadowSessions(t *testing.T) {
	sessions := []tmux.SessionInfo{
		{Name: "g/main"},
		{Name: shadow.Name("%764", "sh")},
		{Name: "notes"},
		{Name: shadow.Name("%628", "vim")},
	}

	got := visibleJumpSessions(sessions)
	want := []tmux.SessionInfo{
		{Name: "g/main"},
		{Name: "notes"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleJumpSessions() mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestVisibleJumpPanesFiltersShadowSessionPanes(t *testing.T) {
	panes := []tmux.PaneInfo{
		{Target: "g/main:1.1", Session: "g/main"},
		{Target: "gs/sh/764:1.1", Session: shadow.Name("%764", "sh")},
		{Target: "notes:1.1", Session: "notes"},
	}

	got := visibleJumpPanes(panes)
	want := []tmux.PaneInfo{
		{Target: "g/main:1.1", Session: "g/main"},
		{Target: "notes:1.1", Session: "notes"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleJumpPanes() mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
