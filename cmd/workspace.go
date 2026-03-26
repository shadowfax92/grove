package cmd

import (
	"os"

	"grove/internal/state"
)

func workspaceDir(ws *state.Workspace) string {
	if ws == nil {
		return ""
	}
	if ws.Type == "worktree" && ws.WorktreePath != "" {
		return ws.WorktreePath
	}
	if ws.Path != "" {
		return ws.Path
	}
	home, _ := os.UserHomeDir()
	return home
}
