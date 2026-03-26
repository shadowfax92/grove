package cmd

import (
	"fmt"

	"grove/internal/config"
	"grove/internal/shadow"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	shadowCmd.AddCommand(shadowVimCmd)
	shadowCmd.AddCommand(shadowShellCmd)
	shadowCmd.AddCommand(shadowCleanupCmd)
	rootCmd.AddCommand(shadowCmd)
}

var shadowCmd = &cobra.Command{
	Use:         "shadow",
	Aliases:     []string{"sh"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Toggle persistent popup sessions (vim, shell) for the current pane",
}

var shadowVimCmd = &cobra.Command{
	Use:   "vim",
	Short: "Toggle vim shadow popup for current pane",
	RunE: func(cmd *cobra.Command, args []string) error {
		return toggleShadow("vim")
	},
}

var shadowShellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Toggle shell shadow popup for current pane",
	RunE: func(cmd *cobra.Command, args []string) error {
		return toggleShadow("sh")
	},
}

var shadowCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove shadow sessions for panes that no longer exist",
	RunE: func(cmd *cobra.Command, args []string) error {
		return shadow.CleanupOrphans()
	},
}

func toggleShadow(typ string) error {
	cfg, err := config.LoadFast()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	paneID, err := tmux.PaneID()
	if err != nil {
		return fmt.Errorf("getting pane id: %w", err)
	}

	paneCwd, err := tmux.PaneCwd()
	if err != nil {
		return fmt.Errorf("getting pane cwd: %w", err)
	}

	sessionName := shadow.Name(paneID, typ)

	if err := shadow.Ensure(sessionName, paneCwd, typ, paneID); err != nil {
		return err
	}

	return tmux.DisplayPopup(sessionName, cfg.Shadow.Popup.Width, cfg.Shadow.Popup.Height)
}
