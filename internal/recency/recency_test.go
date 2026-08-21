package recency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrackerMarksAndReadsLastVisit(t *testing.T) {
	tracker := New(t.TempDir())
	worktreePath := filepath.Join(t.TempDir(), "line\nbreak")
	if err := tracker.MarkVisited(worktreePath); err != nil {
		t.Fatalf("MarkVisited() error = %v", err)
	}

	visitedAt, ok := tracker.LastVisited(worktreePath)
	if !ok || time.Since(visitedAt) > time.Minute {
		t.Fatalf("LastVisited() = %v, %v", visitedAt, ok)
	}
	marker, err := os.ReadFile(tracker.markerPath(worktreePath))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSuffix(string(marker), "\n"), filepath.Clean(worktreePath); got != want {
		t.Fatalf("marker path = %q, want %q", got, want)
	}
}

func TestTrackerRefreshesExistingMarker(t *testing.T) {
	tracker := New(t.TempDir())
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := tracker.MarkVisited(worktreePath); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(tracker.markerPath(worktreePath), old, old); err != nil {
		t.Fatal(err)
	}

	if err := tracker.MarkVisited(worktreePath); err != nil {
		t.Fatal(err)
	}
	visitedAt, ok := tracker.LastVisited(worktreePath)
	if !ok || !visitedAt.After(old) {
		t.Fatalf("LastVisited() = %v, %v, want after %v", visitedAt, ok, old)
	}
}

func TestDefaultUsesXDGStateHome(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)
	tracker, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tracker.directory, filepath.Join(stateRoot, "grove", "recent"); got != want {
		t.Fatalf("directory = %q, want %q", got, want)
	}
}
