package cmd

import (
	"fmt"

	"grove/internal/trash"

	"github.com/spf13/cobra"
)

// trashCommand exposes physical deletion independently of Git inventory. Removed
// worktrees disappear from inventory immediately; journals remain until bytes go.
func (a *application) trashCommand() *cobra.Command {
	var wait bool
	command := &cobra.Command{
		Use: "trash", Short: "Show pending worktree deletion, or wait for it to finish", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.nullOutput {
				return fmt.Errorf("--null is only valid for path output")
			}
			manager, err := trash.Default()
			if err != nil {
				return err
			}
			if wait {
				if err = manager.Reap(cmd.Context()); err != nil {
					return err
				}
			}
			pending, err := manager.Pending()
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return writeJSON(cmd, struct {
					Version int           `json:"version"`
					Pending []trash.Entry `json:"pending"`
				}{1, pending})
			}
			if len(pending) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No pending worktree deletion.")
			}
			for _, entry := range pending {
				state := "deleting"
				if !entry.Ready {
					state = "recovering"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", entry.ID, state, entry.Original)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&wait, "wait", false, "Wait for pending deletion and report any entries that could not be deleted")
	return command
}
