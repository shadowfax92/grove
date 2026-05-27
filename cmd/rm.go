package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/workspaces"

	"github.com/spf13/cobra"
)

func init() {
	rmCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	rootCmd.AddCommand(rmCmd)
}

var rmCmd = &cobra.Command{
	Use:         "rm [workspace...]",
	Aliases:     []string{"remove"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove workspaces and their worktrees",
	Long: `Remove grove workspaces and their worktrees.

  grove rm                — pick workspaces via fzf (Tab to multi-select)
  grove rm <w1> <w2> ...  — remove specific workspaces`,
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
		} else {
			targets, err = inv.ResolveRemoveTargets(args)
		}
		if err != nil {
			return err
		}

		if !force && !confirmRemove(targets) {
			fmt.Println("Cancelled.")
			return nil
		}

		workspaces.RemoveManagedEntries(st, targets)
		if err := mgr.Save(st); err != nil {
			return err
		}

		var failed []state.Workspace
		for _, t := range targets {
			if err := removeWorktreeForTarget(t); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v\n", t.Workspace.WorktreePath, err)
				failed = append(failed, t.Workspace)
				continue
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

func confirmRemove(targets []workspaces.RemoveTarget) bool {
	if len(targets) == 1 {
		fmt.Printf("Remove %q? [y/N] ", targets[0].Label())
	} else {
		fmt.Printf("Remove %d workspaces?\n", len(targets))
		for _, t := range targets {
			fmt.Printf("  %s\n", t.Label())
		}
		fmt.Print("[y/N] ")
	}
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(answer)) == "y"
}

func removeWorktreeForTarget(t workspaces.RemoveTarget) error {
	ws := t.Workspace
	if ws.Type != "worktree" || ws.WorktreePath == "" || ws.WorktreePath == ws.RepoPath {
		return nil
	}
	if _, statErr := os.Stat(ws.WorktreePath); statErr != nil {
		return nil
	}
	return git.RemoveWorktree(ws.RepoPath, ws.WorktreePath)
}

func pickRemoveTargetsFzf(inv *workspaces.Inventory) ([]workspaces.RemoveTarget, error) {
	targets := inv.RemoveCandidates()
	if len(targets) == 0 {
		return nil, fmt.Errorf("no workspaces to remove")
	}

	lookup := make(map[string]workspaces.RemoveTarget, len(targets))
	input := renderRemovePickerInput(targets, lookup)

	fzfCmd := exec.Command(
		"fzf",
		"--multi",
		"--prompt", "remove > ",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--accept-nth", "1",
		"--with-nth", "2,3",
	)
	fzfCmd.Stdin = strings.NewReader(input)
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
		if target, ok := lookup[id]; ok {
			selected = append(selected, target)
		}
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}

func renderRemovePickerInput(targets []workspaces.RemoveTarget, lookup map[string]workspaces.RemoveTarget) string {
	sorted := make([]workspaces.RemoveTarget, len(targets))
	copy(sorted, targets)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, tj := sorted[i].Workspace.CreatedAt, sorted[j].Workspace.CreatedAt
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
		created := "—"
		if target.Workspace.CreatedAt != "" {
			created = state.RelativeTime(target.Workspace.CreatedAt) + " ago"
		}
		lines = append(lines, fmt.Sprintf("%s\t%-*s\t%s", target.SessionName, maxLabel, target.Label(), created))
	}
	return strings.Join(lines, "\n")
}
