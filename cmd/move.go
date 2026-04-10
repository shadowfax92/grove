package cmd

import (
	"fmt"
	"os"

	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(moveCmd)
}

var moveCmd = &cobra.Command{
	Use:         "move <target-session>",
	Aliases:     []string{"mv"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Move the current window to another session",
	Long: `Move the current tmux window to a different session.
Creates the target session if it doesn't exist.

  grove move admin   — move current window to "admin" session
  grove mv g/ops     — same, with alias`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.IsInsideTmux() {
			return fmt.Errorf("grove move must run inside tmux")
		}

		target := args[0]
		session, err := tmux.CurrentSession()
		if err != nil {
			return fmt.Errorf("reading current session: %w", err)
		}
		if session == target {
			return fmt.Errorf("current window is already in %q", target)
		}

		created := false
		if !tmux.SessionExists(target) {
			home, _ := os.UserHomeDir()
			if home == "" {
				home = "/"
			}
			if err := tmux.NewSession(target, home); err != nil {
				return fmt.Errorf("creating session %q: %w", target, err)
			}
			created = true
		}

		if err := tmux.MoveCurrentWindow(target); err != nil {
			return fmt.Errorf("moving window: %w", err)
		}

		// If we just created the session, kill its placeholder window
		if created {
			_ = tmux.KillWindow("=" + target + ":1")
		}

		fmt.Printf("moved window to %s\n", target)
		return nil
	},
}
