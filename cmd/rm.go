package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"grove/internal/git"
	"grove/internal/shadow"
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
			targets, err = pickRemoveTargetsFzf(inv)
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

func pickRemoveTargetsFzf(inv *workspaces.Inventory) ([]workspaces.RemoveTarget, error) {
	managed := removePickerTargets(inv, false)
	all := removePickerTargets(inv, true)
	if len(all) == 0 {
		return nil, fmt.Errorf("no tmux sessions to remove")
	}

	lookup := make(map[string]workspaces.RemoveTarget, len(all))
	managedInput := renderRemovePickerInput(managed, lookup)
	allInput := renderRemovePickerInput(all, lookup)

	managedFile, err := os.CreateTemp("", "grove-rm-managed-*.txt")
	if err != nil {
		return nil, err
	}
	defer os.Remove(managedFile.Name())
	if _, err := managedFile.WriteString(managedInput); err != nil {
		managedFile.Close()
		return nil, err
	}
	if err := managedFile.Close(); err != nil {
		return nil, err
	}

	allFile, err := os.CreateTemp("", "grove-rm-all-*.txt")
	if err != nil {
		return nil, err
	}
	defer os.Remove(allFile.Name())
	if _, err := allFile.WriteString(allInput); err != nil {
		allFile.Close()
		return nil, err
	}
	if err := allFile.Close(); err != nil {
		return nil, err
	}

	reloadCmd := fmt.Sprintf(
		`sh -c 'case "$1" in %s/*) cat "$2" ;; *) cat "$3" ;; esac' sh {q} %q %q`,
		shadow.Prefix,
		allFile.Name(),
		managedFile.Name(),
	)

	fzfCmd := exec.Command(
		"fzf",
		"--multi",
		"--prompt", "remove > ",
		"--header", "Blank query shows Grove workspaces. Type gs/ to surface shadow sessions.",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--accept-nth", "1",
		"--with-nth", "2,3,4",
		"--bind", "change:reload:"+reloadCmd,
	)
	fzfCmd.Stdin = strings.NewReader(managedInput)
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	var selected []workspaces.RemoveTarget
	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		target, ok := lookup[id]
		if !ok {
			continue
		}
		selected = append(selected, target)
	}

	if len(selected) == 0 {
		return nil, ErrCancelled
	}

	return selected, nil
}

func removePickerTargets(inv *workspaces.Inventory, includeUnmanaged bool) []workspaces.RemoveTarget {
	targets := make([]workspaces.RemoveTarget, 0, len(inv.Managed)+len(inv.Unmanaged))
	for _, entry := range inv.ManagedByLastUsed() {
		targets = append(targets, workspaces.RemoveTarget{
			Kind:        workspaces.RemoveManagedWorkspace,
			Workspace:   entry.Workspace,
			SessionName: entry.Workspace.SessionName,
			Running:     entry.Running,
		})
	}
	if !includeUnmanaged {
		return targets
	}
	for _, session := range inv.Unmanaged {
		targets = append(targets, workspaces.RemoveTarget{
			Kind:        workspaces.RemoveUnmanagedSession,
			SessionName: session.SessionName,
			Running:     true,
		})
	}
	return targets
}

func shouldExpandRemovePicker(query string) bool {
	return strings.HasPrefix(strings.TrimSpace(query), shadow.Prefix+"/")
}

func renderRemovePickerInput(targets []workspaces.RemoveTarget, lookup map[string]workspaces.RemoveTarget) string {
	sorted := make([]workspaces.RemoveTarget, len(targets))
	copy(sorted, targets)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, tj := sorted[i].Workspace.CreatedAt, sorted[j].Workspace.CreatedAt
		if ti == "" && tj == "" {
			return false
		}
		if ti == "" {
			return false
		}
		if tj == "" {
			return true
		}
		return ti > tj
	})

	maxLabel := 0
	for _, t := range sorted {
		if n := len(t.Label()); n > maxLabel {
			maxLabel = n
		}
	}

	var lines []string
	for _, target := range sorted {
		lookup[target.SessionName] = target
		status := "running"
		if target.Kind == workspaces.RemoveManagedWorkspace && !target.Running {
			status = "stopped"
		}
		created := "—"
		if target.Workspace.CreatedAt != "" {
			created = state.RelativeTime(target.Workspace.CreatedAt) + " ago"
		}
		lines = append(lines, fmt.Sprintf("%s\t%-*s\t%-8s\t%s",
			target.SessionName, maxLabel, target.Label(), status, created))
	}
	return strings.Join(lines, "\n")
}
