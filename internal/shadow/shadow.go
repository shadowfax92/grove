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

func IsSession(name string) bool {
	return strings.HasPrefix(name, Prefix+"/")
}

func ParentPane(currentSession, activePane string) (string, error) {
	if !IsSession(currentSession) {
		return activePane, nil
	}
	paneID, err := tmux.GetSessionVar(currentSession, "shadow_parent_pane")
	if err != nil {
		return "", fmt.Errorf("getting shadow parent pane: %w", err)
	}
	if paneID == "" {
		return "", fmt.Errorf("shadow session %s is missing shadow_parent_pane", currentSession)
	}
	return paneID, nil
}

func PopupClient(currentSession, fallback string) (string, error) {
	if !IsSession(currentSession) {
		return fallback, nil
	}
	clientName, err := tmux.GetSessionVar(currentSession, "shadow_client_name")
	if err != nil || clientName == "" {
		return fallback, nil
	}
	return clientName, nil
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

	if typ == "vim" {
		env := []string{fmt.Sprintf("GROVE_AGENT_PANE=%s", paneID)}
		if err := tmux.NewSessionWithCommand(sessionName, paneCwd, env, "nvim"); err != nil {
			return fmt.Errorf("creating shadow session: %w", err)
		}
	} else if err := tmux.NewSession(sessionName, paneCwd); err != nil {
		return fmt.Errorf("creating shadow session: %w", err)
	}
	if err := tmux.SetSessionVar(sessionName, "shadow_cwd", paneCwd); err != nil {
		return fmt.Errorf("storing shadow cwd: %w", err)
	}
	if err := tmux.SetSessionVar(sessionName, "shadow_parent_pane", paneID); err != nil {
		return fmt.Errorf("storing shadow parent pane: %w", err)
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
