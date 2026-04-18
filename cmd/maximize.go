package cmd

import (
	"fmt"
	"strings"

	"grove/internal/config"
	"grove/internal/shadow"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

const (
	shadowPopupModeKey       = "shadow_popup_mode"
	shadowPopupModeNormal    = "normal"
	shadowPopupModeMaximized = "maximized"
	maximizePrefix           = "gm"
	maximizeOriginPaneKey    = "maximize_origin_pane"
	maximizePlaceholderKey   = "maximize_placeholder_pane"
	maximizePopupClientKey   = "maximize_popup_client"
)

func init() {
	rootCmd.AddCommand(maximizeCmd)
}

var maximizeCmd = &cobra.Command{
	Use:         "maximize <client_name> <session_name> <pane_id>",
	Aliases:     []string{"max", "m"},
	Annotations: map[string]string{"group": "Other:"},
	Short:       "Toggle centered maximize popup for tmux panes and shadows",
	Args:        cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clientName := args[0]
		currentSession := args[1]
		activePane := args[2]

		if shadow.IsSession(currentSession) {
			return toggleShadowPopupSize(clientName, currentSession, activePane)
		}
		if isMaximizeSession(currentSession) {
			return restoreMaximizedPane(clientName, currentSession)
		}

		return maximizePaneAsPopup(clientName, activePane)
	},
}

func isMaximizeSession(name string) bool {
	return strings.HasPrefix(name, maximizePrefix+"/")
}

func maximizeName(paneID string) string {
	return fmt.Sprintf("%s/%s", maximizePrefix, strings.TrimPrefix(paneID, "%"))
}

func maximizePaneAsPopup(clientName, activePane string) error {
	cfg, err := config.LoadFast()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	paneCwd, err := tmux.PaneCwd(activePane)
	if err != nil {
		return fmt.Errorf("getting pane cwd: %w", err)
	}

	sessionName := maximizeName(activePane)
	if tmux.SessionExists(sessionName) {
		if err := tmux.KillSession(sessionName); err != nil {
			return fmt.Errorf("removing stale maximize session: %w", err)
		}
	}
	if err := tmux.NewSessionWithCommand(sessionName, paneCwd, nil, "while :; do sleep 3600; done"); err != nil {
		return fmt.Errorf("creating maximize session: %w", err)
	}

	placeholderPane, err := tmux.FirstPaneID(sessionName)
	if err != nil {
		_ = tmux.KillSession(sessionName)
		return fmt.Errorf("finding placeholder pane: %w", err)
	}

	if err := storeMaximizeState(sessionName, activePane, placeholderPane, clientName); err != nil {
		_ = tmux.KillSession(sessionName)
		return err
	}
	if err := tmux.SwapPane(activePane, placeholderPane); err != nil {
		_ = tmux.KillSession(sessionName)
		return fmt.Errorf("moving pane into maximize popup: %w", err)
	}

	command := fmt.Sprintf("exec tmux attach-session -t '=%s'", sessionName)
	if err := tmux.DisplayPopup(clientName, cfg.Shadow.Popup.MaxWidth, cfg.Shadow.Popup.MaxHeight, command); err != nil {
		_ = tmux.SwapPane(activePane, placeholderPane)
		_ = tmux.KillSession(sessionName)
		return fmt.Errorf("opening maximize popup: %w", err)
	}

	return nil
}

func storeMaximizeState(sessionName, originPane, placeholderPane, popupClient string) error {
	if err := tmux.SetSessionVar(sessionName, maximizeOriginPaneKey, originPane); err != nil {
		return fmt.Errorf("storing maximize origin pane: %w", err)
	}
	if err := tmux.SetSessionVar(sessionName, maximizePlaceholderKey, placeholderPane); err != nil {
		return fmt.Errorf("storing maximize placeholder pane: %w", err)
	}
	if err := tmux.SetSessionVar(sessionName, maximizePopupClientKey, popupClient); err != nil {
		return fmt.Errorf("storing maximize popup client: %w", err)
	}
	return nil
}

func restoreMaximizedPane(clientName, currentSession string) error {
	popupClient, err := tmux.GetSessionVar(currentSession, maximizePopupClientKey)
	if err != nil || popupClient == "" {
		popupClient = clientName
	}

	originPane, err := tmux.GetSessionVar(currentSession, maximizeOriginPaneKey)
	if err != nil {
		return fmt.Errorf("getting maximize origin pane: %w", err)
	}
	placeholderPane, err := tmux.GetSessionVar(currentSession, maximizePlaceholderKey)
	if err != nil {
		return fmt.Errorf("getting maximize placeholder pane: %w", err)
	}

	if err := tmux.ClosePopup(popupClient); err != nil {
		return fmt.Errorf("closing popup: %w", err)
	}
	if !tmux.PaneExists(originPane) || !tmux.PaneExists(placeholderPane) {
		return tmux.KillSession(currentSession)
	}
	if err := tmux.SwapPane(originPane, placeholderPane); err != nil {
		return fmt.Errorf("restoring pane from maximize popup: %w", err)
	}
	if err := tmux.KillSession(currentSession); err != nil {
		return fmt.Errorf("removing maximize session: %w", err)
	}
	return nil
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
