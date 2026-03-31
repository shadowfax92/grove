package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"
	"grove/internal/workspaces"

	"github.com/spf13/cobra"
)

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
		inv, err := workspaces.Build(st, nil)
		if err != nil {
			return err
		}

		var targets []workspaces.RemoveTarget
		if len(args) == 0 {
			targets, err = pickRemoveTargetsFzf(inv.RemoveCandidates())
			if err != nil {
				return err
			}
		} else {
			targets, err = inv.ResolveRemoveTargets(args)
			if err != nil {
				return err
			}
		}

		if !force {
			if len(targets) == 1 {
				fmt.Printf("Remove %q? [y/N] ", targets[0].Label())
			} else {
				fmt.Printf("Remove %d sessions?\n", len(targets))
				for _, t := range targets {
					fmt.Printf("  %s\n", t.Label())
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

		workspaces.RemoveManagedEntries(st, targets)
		if err := mgr.Save(st); err != nil {
			return err
		}

		var failed []state.Workspace
		for _, t := range targets {
			if tmux.SessionExists(t.SessionName) {
				_ = tmux.KillSession(t.SessionName)
			}
			if t.Kind == workspaces.RemoveManagedWorkspace && t.Workspace.Type == "worktree" && t.Workspace.WorktreePath != t.Workspace.RepoPath {
				if _, statErr := os.Stat(t.Workspace.WorktreePath); statErr == nil {
					if err := git.RemoveWorktree(t.Workspace.RepoPath, t.Workspace.WorktreePath); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v\n", t.Workspace.WorktreePath, err)
						failed = append(failed, t.Workspace)
					}
				}
			}
			fmt.Printf("Removed %q\n", t.Label())
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

func pickRemoveTargetsFzf(candidates []workspaces.RemoveTarget) ([]workspaces.RemoveTarget, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no tmux sessions to remove")
	}

	var lines []string
	for i, candidate := range candidates {
		kind := "tmux"
		status := "running"
		if candidate.Kind == workspaces.RemoveManagedWorkspace {
			kind = "workspace"
			if !candidate.Running {
				status = "stopped"
			}
		}
		lines = append(lines, fmt.Sprintf("%d\t%-10s\t%-30s\t%s", i, kind, candidate.Label(), status))
	}

	fzfCmd := exec.Command(
		"fzf",
		"--multi",
		"--prompt", "remove > ",
		"--header", "Select Grove workspaces or tmux sessions to remove",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "2,3,4",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	var selected []workspaces.RemoveTarget
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			parts := strings.SplitN(line, "\t", 4)
			idx, convErr := strconv.Atoi(parts[0])
			if convErr != nil || idx < 0 || idx >= len(candidates) {
				continue
			}
			selected = append(selected, candidates[idx])
		}
	}

	if len(selected) == 0 {
		return nil, ErrCancelled
	}

	return selected, nil
}
