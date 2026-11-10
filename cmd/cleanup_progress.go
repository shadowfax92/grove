package cmd

import (
	"fmt"
	"time"

	"grove/internal/cleanup"
	"grove/internal/inventory"

	"github.com/spf13/cobra"
)

// The command owns stderr throughout collection, including silent subprocess
// waits. Completion joins every collector before handing immutable rows to fzf;
// JSON/path stdout and background sweep logs remain independent of progress.
func (a *application) collectCleanupSignals(cmd *cobra.Command, inv *inventory.Inventory, entries []*inventory.Entry, forge, show bool) []cleanup.Row {
	if !show {
		return cleanup.Collect(cmd.Context(), inv, entries, forge, a.dependencies.inUse, nil)
	}
	progress := &cleanup.Progress{}
	fmt.Fprintf(cmd.ErrOrStderr(), "Collecting cleanup signals for %d worktrees...\n", len(entries))
	started := time.Now()
	done := make(chan []cleanup.Row, 1)
	go func() { done <- cleanup.Collect(cmd.Context(), inv, entries, forge, a.dependencies.inUse, progress) }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case rows := <-done:
			state := progress.Snapshot()
			fmt.Fprintf(cmd.ErrOrStderr(), "Collected cleanup signals in %s (processes %s; activity/cached sizes %s; merges %s, overlapping).\n", time.Since(started).Round(time.Millisecond), state.ProcessDuration.Round(time.Millisecond), state.ActivityDuration.Round(time.Millisecond), state.MergeDuration.Round(time.Millisecond))
			return rows
		case <-ticker.C:
			state := progress.Snapshot()
			process := "checking processes"
			if state.ProcessesDone {
				process = "processes checked"
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Collecting cleanup signals: %s; activity %d/%d worktrees; merges %d/%d repositories (%s).\n", process, state.WorktreesDone, state.Worktrees, state.RepositoriesDone, state.Repositories, time.Since(started).Round(time.Second))
		}
	}
}
