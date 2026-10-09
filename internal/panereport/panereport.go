// Package panereport persists worktree handles and publishes them to the calling
// Herdr pane. Git owns worktree inventory; the durable handles let consumers
// recover full roots from Herdr's length-limited pane tokens.
package panereport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Worktree reports an absolute worktree root after its output succeeds. Reporting
// is synchronous so Grove's exit cannot race publication, but has a short deadline
// and no error channel so an unavailable Herdr cannot fail navigation or creation.
func Worktree(ctx context.Context, path string) {
	pane := os.Getenv("HERDR_PANE_ID")
	if os.Getenv("HERDR_ENV") != "1" || pane == "" || os.Getenv("GROVE_PANE_REPORT") == "0" {
		return
	}
	// Publish the full root before the token: Scratch may resolve the handle as
	// soon as Herdr accepts it. A failed store write must leave the old token alone.
	handle, err := storeWorktree(path)
	if err != nil {
		return
	}
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		binary = "herdr"
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	// The installed CLI expects its positional pane before flags. A dedicated
	// source preserves other plugins' tokens; the 64-character handle fits Herdr's
	// 80-character value limit regardless of the root's length.
	process := exec.CommandContext(ctx, binary, "pane", "report-metadata", pane,
		"--source", "grove", "--token", "grove_worktree="+handle)
	// Nil streams connect to the null device, preserving Grove's byte-level output
	// and avoiding pipe-copy goroutines that could outlive the command deadline.
	_ = process.Run()
}

// storeWorktree commits an immutable root-to-hash mapping in Grove's durable
// state, separate from disposable recency markers. Concurrent writers for the
// same root publish identical bytes; same-directory rename prevents partial reads.
func storeWorktree(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("worktree root must be absolute")
	}
	stateRoot := os.Getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		stateRoot = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(stateRoot) {
		return "", errors.New("worktree handle state root must be absolute")
	}
	directory := filepath.Join(stateRoot, "grove", "worktrees")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	identity := sha256.Sum256([]byte(path))
	handle := hex.EncodeToString(identity[:])
	temporary, err := os.CreateTemp(directory, ".worktree-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.WriteString(path + "\n"); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporary.Name(), filepath.Join(directory, handle)); err != nil {
		return "", err
	}
	return handle, nil
}
