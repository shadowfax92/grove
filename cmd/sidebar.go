package cmd

import (
	"fmt"

	"grove/internal/config"
	"grove/internal/state"
	"grove/internal/tmux"
	"grove/internal/tui"

	"github.com/spf13/cobra"
)

func init() {
	sidebarCmd.Hidden = true
	rootCmd.AddCommand(sidebarCmd)
}

var sidebarCmd = &cobra.Command{
	Use:   "sidebar",
	Short: "Launch the sidebar TUI (internal)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.IsInsideTmux() {
			return fmt.Errorf("grove sidebar must be run inside tmux")
		}

		cfg, err := config.LoadFast()
		if err != nil {
			return err
		}

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		cur, _ := tmux.CurrentSession()

		return tui.RunSidebar(cfg, mgr, st, cur)
	},
}
