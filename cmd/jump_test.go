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

func TestRootIncludesQuickCommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"quick"})
	if err != nil {
		t.Fatalf("rootCmd.Find(quick) error = %v", err)
	}
	if cmd == nil || cmd.Name() != "quick" {
		t.Fatalf("rootCmd.Find(quick) = %v, want quick command", cmd)
	}
}

func TestJumpTargetSessionName(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "pane", target: "g/main:2.1", want: "g/main"},
		{name: "window", target: "notes:3", want: "notes"},
		{name: "session", target: "scratch", want: "scratch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jumpTargetSessionName(tt.target); got != tt.want {
				t.Fatalf("jumpTargetSessionName(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
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
