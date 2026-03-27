package cmd

import (
	"fmt"
	"os"
	"sort"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	doneCmd.Flags().Bool("tmux", false, "Finish the current tmux workspace")
	doneCmd.Flags().Bool("cd", false, "Finish the cwd-backed workspace and print the next path")
	rootCmd.AddCommand(doneCmd)
}

var doneCmd = &cobra.Command{
	Use:         "done [workspace]",
	Aliases:     []string{"d"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Finish a workspace and move to the next one",
	Long: `Finish a workspace.

  grove done --tmux             — switch tmux client to next workspace, then remove current workspace
  grove done --cd               — remove workspace for current cwd and print next path
  grove done --cd <workspace>   — remove specific workspace and print next path

Designed for the "branch merged, I'm done" workflow.

Bind in tmux.conf:
  bind-key D display-popup -E "grove done --tmux"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tmuxMode, _ := cmd.Flags().GetBool("tmux")
		cdMode, _ := cmd.Flags().GetBool("cd")
		if err := validateDoneMode(tmuxMode, cdMode); err != nil {
			return err
		}
		if err := validateDoneArgs(args, tmuxMode); err != nil {
			return err
		}
		return runDone(args, tmuxMode)
	},
}

func validateDoneMode(tmuxMode, cdMode bool) error {
	if tmuxMode == cdMode {
		return fmt.Errorf("choose exactly one mode: --tmux or --cd")
	}
	return nil
}

func validateDoneArgs(args []string, tmuxMode bool) error {
	if tmuxMode && len(args) > 0 {
		return fmt.Errorf("workspace arguments are only supported with --cd")
	}
	return nil
}

func runDone(args []string, tmuxMode bool) error {
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

	current, next, err := resolveDoneWorkspaces(mgr, st, args, tmuxMode)
	if err != nil {
		return err
	}

	if tmuxMode {
		if err := ensureDoneSwitchTarget(next); err != nil {
			return err
		}
		if err := tmux.SwitchClient(next.SessionName); err != nil {
			return fmt.Errorf("switching to %s: %w", next.SessionName, err)
		}
	}

	removed := *current
	nextPath := workspaceDir(next)
	mgr.TouchWorkspace(st, next.SessionName)
	st.LastActive = next.SessionName
	mgr.RemoveWorkspace(st, current.SessionName)
	if err := mgr.Save(st); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	cleanupDoneWorkspace(removed)

	if !tmuxMode {
		fmt.Println(nextPath)
		return nil
	}
	fmt.Printf("Done with %q → switched to %s\n", removed.Name, next.SessionName)
	return nil
}

func resolveDoneWorkspaces(mgr *state.StateManager, st *state.State, args []string, tmuxMode bool) (*state.Workspace, *state.Workspace, error) {
	current, err := resolveDoneWorkspace(mgr, st, args, tmuxMode)
	if err != nil {
		return nil, nil, err
	}
	nextSession := findNextSession(st, current.SessionName)
	if nextSession == "" {
		return nil, nil, fmt.Errorf("no other workspace to switch to")
	}
	next := mgr.FindBySession(st, nextSession)
	if next == nil {
		return nil, nil, fmt.Errorf("next workspace %q not found", nextSession)
	}
	return current, next, nil
}

func resolveDoneWorkspace(mgr *state.StateManager, st *state.State, args []string, tmuxMode bool) (*state.Workspace, error) {
	if tmuxMode {
		return resolveTmuxDoneWorkspace(mgr, st)
	}
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

func resolveTmuxDoneWorkspace(mgr *state.StateManager, st *state.State) (*state.Workspace, error) {
	current, err := tmux.CurrentSession()
	if err != nil {
		return nil, fmt.Errorf("not inside tmux")
	}
	ws := mgr.FindBySession(st, current)
	if ws == nil {
		return nil, fmt.Errorf("current session %q is not a grove workspace", current)
	}
	return ws, nil
}

func ensureDoneSwitchTarget(next *state.Workspace) error {
	if tmux.SessionExists(next.SessionName) {
		return nil
	}
	if err := tmux.NewSession(next.SessionName, workspaceDir(next)); err != nil {
		return fmt.Errorf("recreating target session: %w", err)
	}
	return nil
}

func cleanupDoneWorkspace(removed state.Workspace) {
	if tmux.SessionExists(removed.SessionName) {
		_ = tmux.KillSession(removed.SessionName)
	}
	if removed.Type != "worktree" || removed.WorktreePath == removed.RepoPath {
		return
	}
	if _, statErr := os.Stat(removed.WorktreePath); statErr == nil {
		if err := git.RemoveWorktree(removed.RepoPath, removed.WorktreePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: worktree removal failed: %v\n", err)
		}
	}
}

func findNextSession(st *state.State, exclude string) string {
	// Try LastActive first
	if st.LastActive != "" && st.LastActive != exclude {
		return st.LastActive
	}

	// Fall back to most recently used workspace
	sorted := make([]state.Workspace, 0, len(st.Workspaces))
	for _, ws := range st.Workspaces {
		if ws.SessionName != exclude {
			sorted = append(sorted, ws)
		}
	}
	if len(sorted) == 0 {
		return ""
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LastUsedAt > sorted[j].LastUsedAt
	})
	return sorted[0].SessionName
}
