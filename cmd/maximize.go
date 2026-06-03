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
	shadowPopupModeKey        = "shadow_popup_mode"
	shadowPopupModeNormal     = "normal"
	shadowPopupModeMaximized  = "maximized"
	shadowPopupLeftGutterKey  = "shadow_popup_left_gutter"
	shadowPopupRightGutterKey = "shadow_popup_right_gutter"
	maximizePrefix            = "gm"
	maximizeOriginPaneKey     = "maximize_origin_pane"
	maximizePlaceholderKey    = "maximize_placeholder_pane"
	maximizePopupClientKey    = "maximize_popup_client"
	focusPopupWidth           = "100%"
	focusPopupHeight          = "100%"
	focusLeftGutterPercent    = 25
	focusRightGutterPercent   = 33
	blankPaneCommand          = "while :; do sleep 3600; done"
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
	sessionName, placeholderPane, err := createMaximizeSession(activePane)
	if err != nil {
		return err
	}
	if err := storeMaximizeState(sessionName, activePane, placeholderPane, clientName); err != nil {
		_ = tmux.KillSession(sessionName)
		return err
	}
	if err := tmux.SwapPane(activePane, placeholderPane); err != nil {
		_ = tmux.KillSession(sessionName)
		return fmt.Errorf("moving pane into maximize popup: %w", err)
	}
	if err := tmux.SelectPane(activePane); err != nil {
		_ = tmux.SwapPane(activePane, placeholderPane)
		_ = tmux.KillSession(sessionName)
		return fmt.Errorf("selecting maximized pane: %w", err)
	}

	command := fmt.Sprintf("exec tmux attach-session -t '=%s'", sessionName)
	if err := tmux.DisplayPopup(clientName, focusPopupWidth, focusPopupHeight, command); err != nil {
		_ = tmux.SwapPane(activePane, placeholderPane)
		_ = tmux.KillSession(sessionName)
		return fmt.Errorf("opening maximize popup: %w", err)
	}

	return nil
}

func createMaximizeSession(activePane string) (string, string, error) {
	paneCwd, err := tmux.PaneCwd(activePane)
	if err != nil {
		return "", "", fmt.Errorf("getting pane cwd: %w", err)
	}

	sessionName := maximizeName(activePane)
	if tmux.SessionExists(sessionName) {
		if err := tmux.KillSession(sessionName); err != nil {
			return "", "", fmt.Errorf("removing stale maximize session: %w", err)
		}
	}
	if err := tmux.NewSessionWithCommand(sessionName, paneCwd, nil, blankPaneCommand); err != nil {
		return "", "", fmt.Errorf("creating maximize session: %w", err)
	}

	placeholderPane, err := tmux.FirstPaneID(sessionName)
	if err != nil {
		_ = tmux.KillSession(sessionName)
		return "", "", fmt.Errorf("finding placeholder pane: %w", err)
	}
	if err := createCenteredGutters(placeholderPane, paneCwd); err != nil {
		_ = tmux.KillSession(sessionName)
		return "", "", fmt.Errorf("creating maximize gutters: %w", err)
	}
	return sessionName, placeholderPane, nil
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

	width := focusPopupWidth
	height := focusPopupHeight
	nextMode := shadowPopupModeMaximized

	currentMode, _ := tmux.GetSessionVar(currentSession, shadowPopupModeKey)
	if currentMode == shadowPopupModeMaximized {
		width, height = resolvePopupSize(cfg.Shadow.Popup, popupClient)
		nextMode = shadowPopupModeNormal
	}

	command := fmt.Sprintf("exec tmux attach-session -t '=%s'", currentSession)

	if err := tmux.ClosePopup(popupClient); err != nil {
		return fmt.Errorf("closing popup: %w", err)
	}
	if nextMode == shadowPopupModeMaximized {
		if err := prepareShadowFocusLayout(currentSession, activePane); err != nil {
			return err
		}
	} else if err := cleanupShadowFocusLayout(currentSession); err != nil {
		return err
	}
	if err := tmux.SetSessionVar(currentSession, shadowPopupModeKey, nextMode); err != nil {
		return fmt.Errorf("storing popup mode: %w", err)
	}
	if err := tmux.DisplayPopup(popupClient, width, height, command); err != nil {
		return fmt.Errorf("opening popup: %w", err)
	}

	return nil
}

func createCenteredGutters(centerPane, startDir string) error {
	leftGutter, err := tmux.SplitPaneHorizontal(centerPane, startDir, true, focusLeftGutterPercent, blankPaneCommand)
	if err != nil {
		return fmt.Errorf("creating left gutter: %w", err)
	}
	rightGutter, err := tmux.SplitPaneHorizontal(centerPane, startDir, false, focusRightGutterPercent, blankPaneCommand)
	if err != nil {
		_ = tmux.KillPane(leftGutter)
		return fmt.Errorf("creating right gutter: %w", err)
	}
	if err := tmux.SelectPane(centerPane); err != nil {
		_ = tmux.KillPane(leftGutter)
		_ = tmux.KillPane(rightGutter)
		return fmt.Errorf("selecting centered pane: %w", err)
	}
	return nil
}

func prepareShadowFocusLayout(sessionName, centerPane string) error {
	if err := cleanupShadowFocusLayout(sessionName); err != nil {
		return err
	}

	paneCwd, err := tmux.PaneCwd(centerPane)
	if err != nil {
		return fmt.Errorf("getting shadow pane cwd: %w", err)
	}
	leftGutter, rightGutter, err := createTrackedCenteredGutters(centerPane, paneCwd)
	if err != nil {
		return err
	}
	if err := tmux.SetSessionVar(sessionName, shadowPopupLeftGutterKey, leftGutter); err != nil {
		_ = tmux.KillPane(leftGutter)
		_ = tmux.KillPane(rightGutter)
		return fmt.Errorf("storing left shadow gutter: %w", err)
	}
	if err := tmux.SetSessionVar(sessionName, shadowPopupRightGutterKey, rightGutter); err != nil {
		_ = tmux.KillPane(leftGutter)
		_ = tmux.KillPane(rightGutter)
		return fmt.Errorf("storing right shadow gutter: %w", err)
	}
	return nil
}

func createTrackedCenteredGutters(centerPane, startDir string) (string, string, error) {
	leftGutter, err := tmux.SplitPaneHorizontal(centerPane, startDir, true, focusLeftGutterPercent, blankPaneCommand)
	if err != nil {
		return "", "", fmt.Errorf("creating left gutter: %w", err)
	}
	rightGutter, err := tmux.SplitPaneHorizontal(centerPane, startDir, false, focusRightGutterPercent, blankPaneCommand)
	if err != nil {
		_ = tmux.KillPane(leftGutter)
		return "", "", fmt.Errorf("creating right gutter: %w", err)
	}
	if err := tmux.SelectPane(centerPane); err != nil {
		_ = tmux.KillPane(leftGutter)
		_ = tmux.KillPane(rightGutter)
		return "", "", fmt.Errorf("selecting centered pane: %w", err)
	}
	return leftGutter, rightGutter, nil
}

func cleanupShadowFocusLayout(sessionName string) error {
	if err := cleanupShadowGutter(sessionName, shadowPopupLeftGutterKey); err != nil {
		return err
	}
	return cleanupShadowGutter(sessionName, shadowPopupRightGutterKey)
}

func cleanupShadowGutter(sessionName, key string) error {
	paneID, err := tmux.GetSessionVar(sessionName, key)
	if err != nil || paneID == "" {
		return nil
	}
	if tmux.PaneExists(paneID) {
		if err := tmux.KillPane(paneID); err != nil {
			return fmt.Errorf("removing shadow gutter %s: %w", paneID, err)
		}
	}
	if err := tmux.SetSessionVar(sessionName, key, ""); err != nil {
		return fmt.Errorf("clearing shadow gutter state: %w", err)
	}
	return nil
}
