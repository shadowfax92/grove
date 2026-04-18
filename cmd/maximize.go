package cmd

import (
	"fmt"

	"grove/internal/config"
	"grove/internal/shadow"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

const (
	shadowPopupModeKey       = "shadow_popup_mode"
	shadowPopupModeNormal    = "normal"
	shadowPopupModeMaximized = "maximized"
)

func init() {
	rootCmd.AddCommand(maximizeCmd)
}

var maximizeCmd = &cobra.Command{
	Use:         "maximize <client_name> <session_name> <pane_id>",
	Aliases:     []string{"max", "m"},
	Annotations: map[string]string{"group": "Other:"},
	Short:       "Toggle tmux pane zoom or grove popup fullscreen",
	Args:        cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clientName := args[0]
		currentSession := args[1]
		activePane := args[2]

		if shadow.IsSession(currentSession) {
			return toggleShadowPopupSize(clientName, currentSession, activePane)
		}

		if err := tmux.TogglePaneZoom(activePane); err != nil {
			return fmt.Errorf("toggling pane zoom: %w", err)
		}

		return nil
	},
}

func toggleShadowPopupSize(clientName, currentSession, activePane string) error {
	cfg, err := config.LoadFast()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	popupClient, err := shadow.PopupClient(currentSession, clientName)
	if err != nil {
		return err
	}

	parentPane, err := shadow.ParentPane(currentSession, activePane)
	if err != nil {
		return err
	}
	if !tmux.PaneExists(parentPane) {
		return shadow.CleanupOrphans()
	}

	width := cfg.Shadow.Popup.MaxWidth
	height := cfg.Shadow.Popup.MaxHeight
	nextMode := shadowPopupModeMaximized

	currentMode, _ := tmux.GetSessionVar(currentSession, shadowPopupModeKey)
	if currentMode == shadowPopupModeMaximized {
		width = cfg.Shadow.Popup.Width
		height = cfg.Shadow.Popup.Height
		nextMode = shadowPopupModeNormal
	}

	command := fmt.Sprintf("exec tmux attach-session -t '=%s'", currentSession)

	if err := tmux.ClosePopup(popupClient); err != nil {
		return fmt.Errorf("closing popup: %w", err)
	}
	if err := tmux.SetSessionVar(currentSession, shadowPopupModeKey, nextMode); err != nil {
		return fmt.Errorf("storing popup mode: %w", err)
	}
	if err := tmux.DisplayPopup(popupClient, width, height, command); err != nil {
		return fmt.Errorf("opening popup: %w", err)
	}

	return nil
}
