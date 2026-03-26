package cmd

import (
	"fmt"

	"grove/internal/config"
	"grove/internal/shadow"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	shadowCmd.AddCommand(shadowToggleCmd)
	shadowCmd.AddCommand(shadowCleanupCmd)
	rootCmd.AddCommand(shadowCmd)
}

var shadowCmd = &cobra.Command{
	Use:         "shadow",
	Aliases:     []string{"sh"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Toggle persistent popup sessions (vim, shell) for the current pane",
}

var shadowToggleCmd = &cobra.Command{
	Use:   "toggle <vim|shell> <client_name> <session_name> <pane_id>",
	Short: "Toggle a shadow popup for the current tmux client",
	Args:  cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, err := normalizeShadowType(args[0])
		if err != nil {
			return err
		}

		cfg, err := config.LoadFast()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		clientName := args[1]
		currentSession := args[2]
		activePane := args[3]

		popupClient, err := shadow.PopupClient(currentSession, clientName)
		if err != nil {
			return err
		}

		parentPane, err := shadow.ParentPane(currentSession, activePane)
		if err != nil {
			return err
		}

		targetSession := shadow.Name(parentPane, typ)
		if shadow.IsSession(currentSession) {
			if err := tmux.ClosePopup(popupClient); err != nil {
				return fmt.Errorf("closing popup: %w", err)
			}
			if currentSession == targetSession {
				return nil
			}
		}

		if !tmux.PaneExists(parentPane) {
			return shadow.CleanupOrphans()
		}

		paneCwd, err := tmux.PaneCwd(parentPane)
		if err != nil {
			return fmt.Errorf("getting pane cwd: %w", err)
		}

		if err := shadow.Ensure(targetSession, paneCwd, typ, parentPane); err != nil {
			return err
		}
		if err := tmux.SetSessionVar(targetSession, "shadow_client_name", popupClient); err != nil {
			return fmt.Errorf("storing shadow client: %w", err)
		}

		command := fmt.Sprintf("exec tmux attach-session -t '=%s'", targetSession)
		return tmux.DisplayPopup(popupClient, cfg.Shadow.Popup.Width, cfg.Shadow.Popup.Height, command)
	},
}

var shadowCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove shadow sessions for panes that no longer exist",
	RunE: func(cmd *cobra.Command, args []string) error {
		return shadow.CleanupOrphans()
	},
}

func normalizeShadowType(typ string) (string, error) {
	switch typ {
	case "vim":
		return "vim", nil
	case "shell", "sh":
		return "sh", nil
	default:
		return "", fmt.Errorf("invalid shadow type %q", typ)
	}
}
