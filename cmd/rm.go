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
	Use:   "rm [workspace]",
	Short: "Remove a workspace",
	Long: `Remove a workspace, its tmux session, and worktree (if applicable).

  grove rm             — pick workspace via fzf
  grove rm <workspace> — remove specific workspace`,
	Args: cobra.MaximumNArgs(1),
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

		var ws *state.Workspace
		if len(args) == 1 {
			ws = mgr.FindWorkspace(st, args[0])
			if ws == nil {
				return fmt.Errorf("workspace %q not found", args[0])
			}
		} else {
			picked, err := pickWorkspaceFzf(st)
			if err != nil {
				return err
			}
			ws = mgr.FindBySession(st, picked)
			if ws == nil {
				return fmt.Errorf("workspace not found")
			}
		}

		if !force {
			fmt.Printf("Remove workspace %q? [y/N] ", ws.Name)
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if tmux.SessionExists(ws.SessionName) {
			if err := tmux.KillSession(ws.SessionName); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to kill session: %v\n", err)
			}
		}

		if ws.Type == "worktree" && ws.WorktreePath != ws.RepoPath {
			if err := git.RemoveWorktree(ws.RepoPath, ws.WorktreePath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove worktree: %v\n", err)
			}
		}

		mgr.RemoveWorkspace(st, ws.SessionName)
		if err := mgr.Save(st); err != nil {
			return err
		}

		fmt.Printf("Removed workspace %q\n", ws.Name)
		return nil
	},
}

func pickWorkspaceFzf(st *state.State) (string, error) {
	if len(st.Workspaces) == 0 {
		return "", fmt.Errorf("no workspaces to remove")
	}

	var lines []string
	for _, ws := range st.Workspaces {
		lines = append(lines, ws.SessionName)
	}

	fzfCmd := exec.Command("fzf", "--prompt", "remove > ", "--height", "~40%", "--reverse")
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
