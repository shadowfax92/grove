package cmd

import (
	"bufio"
	"fmt"
	"os"
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
	Use:   "rm <workspace>",
	Short: "Remove a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
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

		ws := mgr.FindWorkspace(st, name)
		if ws == nil {
			return fmt.Errorf("workspace %q not found", name)
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
