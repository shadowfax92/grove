package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rmCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	rootCmd.AddCommand(rmCmd)
}

var rmCmd = &cobra.Command{
	Use:     "rm [workspace...]",
	Aliases:     []string{"remove"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove one or more workspaces",
	Long: `Remove workspaces, their tmux sessions, and worktrees (if applicable).

  grove rm                    — pick workspaces via fzf (Tab to multi-select)
  grove rm <ws1> <ws2> ...    — remove specific workspaces`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		var targets []*state.Workspace
		if len(args) > 0 {
			for _, arg := range args {
				ws := mgr.FindWorkspace(st, arg)
				if ws == nil {
					return fmt.Errorf("workspace %q not found", arg)
				}
				targets = append(targets, ws)
			}
		} else {
			picked, err := pickWorkspacesFzf(st)
			if err != nil {
				return err
			}
			for _, sessionName := range picked {
				ws := mgr.FindBySession(st, sessionName)
				if ws == nil {
					return fmt.Errorf("workspace not found for session %q", sessionName)
				}
				targets = append(targets, ws)
			}
		}

		if !force {
			if len(targets) == 1 {
				fmt.Printf("Remove workspace %q? [y/N] ", targets[0].Name)
			} else {
				fmt.Printf("Remove %d workspaces?\n", len(targets))
				for _, ws := range targets {
					fmt.Printf("  %s\n", ws.Name)
				}
				fmt.Print("[y/N] ")
			}
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		copies := make([]state.Workspace, len(targets))
		for i, ws := range targets {
			copies[i] = *ws
			mgr.RemoveWorkspace(st, ws.SessionName)
			fmt.Printf("Removed workspace %q\n", ws.Name)
		}
		if err := mgr.Save(st); err != nil {
			return err
		}

		var failed []state.Workspace
		for _, ws := range copies {
			if tmux.SessionExists(ws.SessionName) {
				_ = tmux.KillSession(ws.SessionName)
			}
			if ws.Type == "worktree" && ws.WorktreePath != ws.RepoPath {
				if _, statErr := os.Stat(ws.WorktreePath); statErr == nil {
					if err := git.RemoveWorktree(ws.RepoPath, ws.WorktreePath); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v\n", ws.WorktreePath, err)
						failed = append(failed, ws)
					}
				}
			}
		}

		if len(failed) > 0 {
			for _, ws := range failed {
				mgr.AddWorkspace(st, ws)
			}
			return mgr.Save(st)
		}
		return nil
	},
}

func pickWorkspacesFzf(st *state.State) ([]string, error) {
	if len(st.Workspaces) == 0 {
		return nil, fmt.Errorf("no workspaces to remove")
	}

	var lines []string
	for _, ws := range st.Workspaces {
		lines = append(lines, ws.SessionName)
	}

	fzfCmd := exec.Command("fzf", "--multi", "--prompt", "remove > ", "--height", "~40%", "--reverse")
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	var selected []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			selected = append(selected, line)
		}
	}

	if len(selected) == 0 {
		return nil, ErrCancelled
	}

	return selected, nil
}
