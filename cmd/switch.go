package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(switchCmd)
}

var switchCmd = &cobra.Command{
	Use:     "switch [workspace]",
	Aliases:     []string{"s", "sw"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Switch to a workspace",
	Long: `Switch to a workspace session.

  grove switch             — pick workspace via fzf
  grove switch <workspace> — switch to specific workspace`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := state.NewManager()
		if err != nil {
			return err
		}

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		var ws *state.Workspace
		if len(args) == 1 {
			ws = mgr.FindWorkspace(st, args[0])
			if ws == nil {
				return fmt.Errorf("workspace %q not found", args[0])
			}
		} else {
			picked, err := pickSessionFzf(st)
			if err != nil {
				return err
			}
			ws = mgr.FindBySession(st, picked)
			if ws == nil {
				return fmt.Errorf("workspace not found")
			}
		}

		if !tmux.SessionExists(ws.SessionName) {
			dir := ws.WorktreePath
			if ws.Type == "plain" {
				dir = ws.Path
			}
			if dir == "" {
				dir, _ = os.UserHomeDir()
			}
			if err := tmux.NewSession(ws.SessionName, dir); err != nil {
				return fmt.Errorf("recreating session: %w", err)
			}
		}

		// Update state: clear notifications, touch, set last active
		if err := mgr.Lock(); err != nil {
			return err
		}
		st, err = mgr.Load()
		if err != nil {
			mgr.Unlock()
			return err
		}
		mgr.ClearNotifications(st, ws.SessionName)
		mgr.TouchWorkspace(st, ws.SessionName)
		st.LastActive = ws.SessionName
		_ = mgr.Save(st)
		mgr.Unlock()

		if tmux.IsInsideTmux() {
			return tmux.SwitchClient(ws.SessionName)
		}
		return tmux.Attach(ws.SessionName)
	},
}

func pickSessionFzf(st *state.State) (string, error) {
	if len(st.Workspaces) == 0 {
		return "", fmt.Errorf("no workspaces")
	}

	// Sort by LastUsedAt descending
	sorted := make([]state.Workspace, len(st.Workspaces))
	copy(sorted, st.Workspaces)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LastUsedAt == "" && sorted[j].LastUsedAt == "" {
			return false
		}
		if sorted[i].LastUsedAt == "" {
			return false
		}
		if sorted[j].LastUsedAt == "" {
			return true
		}
		return sorted[i].LastUsedAt > sorted[j].LastUsedAt
	})

	var lines []string
	for _, ws := range sorted {
		badge := "  "
		if len(ws.Notifications) > 0 {
			badge = "* "
		}
		age := ""
		if ws.LastUsedAt != "" {
			age = state.RelativeTime(ws.LastUsedAt)
		}
		display := fmt.Sprintf("%s\t%s%-30s %s", ws.SessionName, badge, ws.Name, age)
		lines = append(lines, display)
	}

	fzfArgs := []string{
		"--prompt", "switch > ",
		"--height", "~40%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "2",
	}

	fzfCmd := exec.Command("fzf", fzfArgs...)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", ErrCancelled
		}
		return "", fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	// fzf returns the full line; extract session name before the tab
	line := strings.TrimSpace(string(out))
	if idx := strings.Index(line, "\t"); idx >= 0 {
		return line[:idx], nil
	}
	return line, nil
}
