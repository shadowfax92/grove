package cmd

import (
	"fmt"
	"os"
	"sort"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(doneCmd)
}

var doneCmd = &cobra.Command{
	Use:         "done",
	Aliases:     []string{"d"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove current workspace and switch to the last active session",
	Long: `Finish the current workspace: switch to the most recently used
session, then remove this workspace (state, tmux session, worktree).

Designed for the "branch merged, I'm done" workflow.

Bind in tmux.conf:
  bind-key D display-popup -E "grove done"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		current, err := tmux.CurrentSession()
		if err != nil {
			return fmt.Errorf("not inside tmux")
		}

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		ws := mgr.FindBySession(st, current)
		if ws == nil {
			return fmt.Errorf("current session %q is not a grove workspace", current)
		}

		// Find the most recently used other workspace
		switchTo := findNextSession(st, current)
		if switchTo == "" {
			return fmt.Errorf("no other workspace to switch to")
		}

		// Ensure target session exists
		if !tmux.SessionExists(switchTo) {
			target := mgr.FindBySession(st, switchTo)
			if target != nil {
				dir := workspaceDir(target)
				if err := tmux.NewSession(switchTo, dir); err != nil {
					return fmt.Errorf("recreating target session: %w", err)
				}
			}
		}

		// Switch first, then clean up
		if err := tmux.SwitchClient(switchTo); err != nil {
			return fmt.Errorf("switching to %s: %w", switchTo, err)
		}

		// Update state: set new last active, touch target
		mgr.TouchWorkspace(st, switchTo)
		st.LastActive = switchTo

		// Remove the workspace from state
		removed := *ws
		mgr.RemoveWorkspace(st, current)
		if err := mgr.Save(st); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}

		// Kill old session
		if tmux.SessionExists(current) {
			_ = tmux.KillSession(current)
		}

		// Remove worktree if applicable
		if removed.Type == "worktree" && removed.WorktreePath != removed.RepoPath {
			if _, statErr := os.Stat(removed.WorktreePath); statErr == nil {
				if err := git.RemoveWorktree(removed.RepoPath, removed.WorktreePath); err != nil {
					fmt.Fprintf(os.Stderr, "warning: worktree removal failed: %v\n", err)
				}
			}
		}

		fmt.Printf("Done with %q → switched to %s\n", removed.Name, switchTo)
		return nil
	},
}

func findNextSession(st *state.State, exclude string) string {
	// Try LastActive first
	if st.LastActive != "" && st.LastActive != exclude {
		return st.LastActive
	}

	// Fall back to most recently used workspace
	sorted := make([]state.Workspace, 0, len(st.Workspaces))
	for _, ws := range st.Workspaces {
		if ws.SessionName != exclude {
			sorted = append(sorted, ws)
		}
	}
	if len(sorted) == 0 {
		return ""
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LastUsedAt > sorted[j].LastUsedAt
	})
	return sorted[0].SessionName
}
