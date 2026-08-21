package recency

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
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

func TestTrackerConcurrentMarksKeepNewestVisit(t *testing.T) {
	tracker := New(t.TempDir())
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	const writers = 32

	start := make(chan struct{})
	errors := make(chan error, writers)
	var group sync.WaitGroup
	for index := 1; index <= writers; index++ {
		visitedAt := base.Add(time.Duration(index) * time.Second)
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errors <- tracker.markVisitedAt(worktreePath, visitedAt)
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("markVisitedAt() error = %v", err)
		}
	}

	want := base.Add(writers * time.Second)
	visitedAt, ok := tracker.LastVisited(worktreePath)
	if !ok || !visitedAt.Equal(want) {
		t.Fatalf("LastVisited() = %v, %v, want newest concurrent visit %v", visitedAt, ok, want)
	}
	older := base.Add(-time.Hour)
	if err := tracker.markVisitedAt(worktreePath, older); err != nil {
		t.Fatal(err)
	}
	if visitedAt, _ := tracker.LastVisited(worktreePath); !visitedAt.Equal(want) {
		t.Fatalf("delayed older visit regressed marker to %v, want %v", visitedAt, want)
	}
}

func TestTrackerLockContentionIsBounded(t *testing.T) {
	tracker := New(t.TempDir())
	if err := os.MkdirAll(tracker.directory, 0700); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(tracker.directory, ".lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	startedAt := time.Now()
	err := tracker.MarkVisited(filepath.Join(t.TempDir(), "worktree"))
	if err == nil || !strings.Contains(err.Error(), "locking recency markers") {
		t.Fatalf("MarkVisited() error = %v, want bounded lock error", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("MarkVisited() blocked for %v, want bounded optional state update", elapsed)
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
