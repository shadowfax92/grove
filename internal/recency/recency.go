// Package recency stores disposable navigation timestamps for worktree picker ranking.
// Git remains the inventory authority; removing this state only resets presentation order.
package recency

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Tracker records one independent marker per canonical worktree path. Independent
// files avoid a shared read-modify-write database when several shells navigate at once.
type Tracker struct {
	directory string
}

func Default() (*Tracker, error) {
	stateRoot := os.Getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolving home directory: %w", err)
		}
		stateRoot = filepath.Join(home, ".local", "state")
	} else if !filepath.IsAbs(stateRoot) {
		return nil, fmt.Errorf("XDG_STATE_HOME must be absolute")
	}
	return New(filepath.Join(stateRoot, "grove", "recent")), nil
}

func New(directory string) *Tracker {
	return &Tracker{directory: filepath.Clean(directory)}
}

func (t *Tracker) LastVisited(path string) (time.Time, bool) {
	if path == "" {
		return time.Time{}, false
	}
	info, err := os.Stat(t.markerPath(path))
	if err != nil || !info.Mode().IsRegular() {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func (t *Tracker) MarkVisited(path string) error {
	if path == "" {
		return fmt.Errorf("worktree path is required")
	}
	if err := os.MkdirAll(t.directory, 0700); err != nil {
		return fmt.Errorf("creating recency directory: %w", err)
	}

	// The temporary file and destination share a directory, so rename is atomic.
	// Concurrent navigations to one worktree therefore leave one complete marker.
	temporary, err := os.CreateTemp(t.directory, ".recent-*")
	if err != nil {
		return fmt.Errorf("creating recency marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return fmt.Errorf("securing recency marker: %w", err)
	}
	if _, err := fmt.Fprintln(temporary, filepath.Clean(path)); err != nil {
		temporary.Close()
		return fmt.Errorf("writing recency marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing recency marker: %w", err)
	}
	if err := os.Rename(temporaryPath, t.markerPath(path)); err != nil {
		return fmt.Errorf("publishing recency marker: %w", err)
	}
	return nil
}

func (t *Tracker) markerPath(path string) string {
	identity := sha256.Sum256([]byte(filepath.Clean(path)))
	return filepath.Join(t.directory, fmt.Sprintf("%x", identity))
}
