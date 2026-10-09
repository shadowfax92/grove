// Package panereport publishes Grove's selected worktree to the calling Herdr
// pane. Herdr owns this disposable context; Git remains the worktree authority.
package panereport

import (
	"context"
	"os"
	"os/exec"
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
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		binary = "herdr"
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	// The installed CLI expects its positional pane before flags. A dedicated
	// source preserves other plugins' tokens; argv keeps paths opaque (no shell).
	process := exec.CommandContext(ctx, binary, "pane", "report-metadata", pane,
		"--source", "grove", "--token", "grove_worktree="+path)
	// Nil streams connect to the null device, preserving Grove's byte-level output
	// and avoiding pipe-copy goroutines that could outlive the command deadline.
	_ = process.Run()
}
