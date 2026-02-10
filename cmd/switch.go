package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(switchCmd)
}

var switchCmd = &cobra.Command{
	Use:   "switch [workspace]",
	Short: "Switch to a workspace",
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

	var lines []string
	for _, ws := range st.Workspaces {
		lines = append(lines, ws.SessionName)
	}

	fzfCmd := exec.Command("fzf", "--prompt", "switch > ", "--height", "~40%", "--reverse")
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", fmt.Errorf("cancelled")
		}
		return "", fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	return strings.TrimSpace(string(out)), nil
}
