package shadow

import (
	"fmt"
	"strings"

	"grove/internal/tmux"
)

const Prefix = "gs"

func Name(paneID, typ string) string {
	id := strings.TrimPrefix(paneID, "%")
	return fmt.Sprintf("%s/%s/%s", Prefix, typ, id)
}

// Ensure creates or re-creates a shadow session for the given pane.
// If the session exists but its cwd doesn't match paneCwd, it is
// killed and recreated so the shadow always follows the pane's project.
func Ensure(sessionName, paneCwd, typ, paneID string) error {
	if tmux.SessionExists(sessionName) {
		storedCwd, _ := tmux.GetSessionVar(sessionName, "shadow_cwd")
		if storedCwd == paneCwd {
			return nil
		}
		tmux.KillSession(sessionName)
	}

	if err := tmux.NewSession(sessionName, paneCwd); err != nil {
		return fmt.Errorf("creating shadow session: %w", err)
	}
	tmux.SetSessionVar(sessionName, "shadow_cwd", paneCwd)

	if typ == "vim" {
		tmux.SetEnv(sessionName, "GROVE_AGENT_PANE", paneID)
		tmux.SendKeys(sessionName, "nvim")
	}

	return nil
}

// CleanupOrphans kills shadow sessions whose parent pane no longer exists.
func CleanupOrphans() error {
	sessions, err := tmux.ListSessionsByPrefix(Prefix + "/")
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		parts := strings.Split(sess, "/")
		if len(parts) != 3 {
			continue
		}
		paneID := "%" + parts[2]
		if !tmux.PaneExists(paneID) {
			tmux.KillSession(sess)
		}
	}
	return nil
}
