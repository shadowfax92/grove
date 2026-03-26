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

type removeTarget struct {
	workspace *state.Workspace // nil for plain tmux sessions
	session   string           // tmux session name (always set)
}

func (t removeTarget) label() string {
	if t.workspace != nil {
		return t.workspace.Name
	}
	return t.session
}

func init() {
	rmCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	rootCmd.AddCommand(rmCmd)
}

var rmCmd = &cobra.Command{
	Use:         "rm [session...]",
	Aliases:     []string{"remove"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove workspaces or tmux sessions",
	Long: `Remove workspaces, tmux sessions, and worktrees (if applicable).

Handles both grove-managed workspaces and plain tmux sessions.

  grove rm                    — pick from all tmux sessions via fzf (Tab to multi-select)
  grove rm <s1> <s2> ...      — remove specific workspaces or tmux sessions`,
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

		var picked []string
		if len(args) == 0 {
			picked, err = pickSessionsFzf()
			if err != nil {
				return err
			}
		}

		targets, err := resolveRemoveTargets(mgr, st, args, picked)
		if err != nil {
			return err
		}

		if !force {
			if len(targets) == 1 {
				fmt.Printf("Remove %q? [y/N] ", targets[0].label())
			} else {
				fmt.Printf("Remove %d sessions?\n", len(targets))
				for _, t := range targets {
					fmt.Printf("  %s\n", t.label())
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

		// Remove grove workspaces from state
		for _, t := range targets {
			if t.workspace != nil {
				mgr.RemoveWorkspace(st, t.workspace.SessionName)
			}
		}
		if err := mgr.Save(st); err != nil {
			return err
		}

		var failed []state.Workspace
		for _, t := range targets {
			if tmux.SessionExists(t.session) {
				_ = tmux.KillSession(t.session)
			}
			if t.workspace != nil && t.workspace.Type == "worktree" && t.workspace.WorktreePath != t.workspace.RepoPath {
				if _, statErr := os.Stat(t.workspace.WorktreePath); statErr == nil {
					if err := git.RemoveWorktree(t.workspace.RepoPath, t.workspace.WorktreePath); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v\n", t.workspace.WorktreePath, err)
						failed = append(failed, *t.workspace)
					}
				}
			}
			fmt.Printf("Removed %q\n", t.label())
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

func pickSessionsFzf() ([]string, error) {
	allSessions, err := tmux.ListSessions()
	if err != nil {
		return nil, err
	}
	if len(allSessions) == 0 {
		return nil, fmt.Errorf("no tmux sessions to remove")
	}

	fzfCmd := exec.Command("fzf", "--multi", "--prompt", "remove > ", "--height", "100%", "--reverse")
	fzfCmd.Stdin = strings.NewReader(strings.Join(allSessions, "\n"))
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

func resolveRemoveTargets(mgr *state.StateManager, st *state.State, args, picked []string) ([]removeTarget, error) {
	var targets []removeTarget

	resolve := func(name string) (removeTarget, error) {
		// Try grove workspace first (by name or session)
		if ws := mgr.FindWorkspace(st, name); ws != nil {
			return removeTarget{workspace: ws, session: ws.SessionName}, nil
		}
		if ws := mgr.FindBySession(st, name); ws != nil {
			return removeTarget{workspace: ws, session: ws.SessionName}, nil
		}
		// Fall back to plain tmux session
		if tmux.SessionExists(name) {
			return removeTarget{session: name}, nil
		}
		return removeTarget{}, fmt.Errorf("session %q not found", name)
	}

	names := args
	if len(names) == 0 {
		names = picked
	}

	for _, name := range names {
		t, err := resolve(name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}
