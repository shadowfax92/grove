package cmd

import (
	"fmt"
	"os"

	"grove/internal/git"
	"grove/internal/state"

	"github.com/spf13/cobra"
)

func init() {
	doneCmd.Flags().Bool("cd", false, "(deprecated) no-op — done always prints the path; kept so old keybinds don't error")
	rootCmd.AddCommand(doneCmd)
}

var doneCmd = &cobra.Command{
	Use:         "done [workspace]",
	Aliases:     []string{"d"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Finish a workspace and remove its worktree",
	Long: `Finish a workspace: remove its worktree and state entry, then print $HOME.

  grove done               — finish the workspace for the current directory
  grove done <workspace>   — finish a specific workspace

Designed for the "branch merged, I'm done" workflow. Pair with a shell wrapper
that cd's into the printed path.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDone(args)
	},
}

func runDone(args []string) error {
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
	current, err := resolveDoneWorkspace(mgr, st, args)
	if err != nil {
		return err
	}

	removed := *current
	home, _ := os.UserHomeDir()
	mgr.RemoveWorkspace(st, current.SessionName)
	if err := mgr.Save(st); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	removeDoneWorktree(removed)
	fmt.Println(home)
	return nil
}

func resolveDoneWorkspace(mgr *state.StateManager, st *state.State, args []string) (*state.Workspace, error) {
	if len(args) == 1 {
		ws := findWorkspaceRef(mgr, st, args[0])
		if ws == nil {
			return nil, fmt.Errorf("workspace %q not found", args[0])
		}
		return ws, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}
	return findWorkspaceByCwd(st, cwd)
}

func removeDoneWorktree(removed state.Workspace) {
	if removed.Type != "worktree" || removed.WorktreePath == removed.RepoPath {
		return
	}
	if _, statErr := os.Stat(removed.WorktreePath); statErr == nil {
		if err := git.RemoveWorktree(removed.RepoPath, removed.WorktreePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: worktree removal failed: %v\n", err)
		}
	}
}
