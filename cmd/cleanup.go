package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/workspaces"

	"github.com/spf13/cobra"
)

func init() {
	cleanupCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	cleanupCmd.Flags().Bool("all", false, "Remove all orphaned worktrees without fzf selection")
	rootCmd.AddCommand(cleanupCmd)
}

var cleanupCmd = &cobra.Command{
	Use:         "cleanup",
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove orphaned worktrees not tracked by grove",
	Long: `Find and remove worktrees on disk under .grove/worktrees that grove no longer tracks in state.

  grove cleanup         — pick via fzf (Tab to multi-select)
  grove cleanup --all   — select all orphaned worktrees
  grove cleanup -f      — skip confirmation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		all, _ := cmd.Flags().GetBool("all")

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		st, err := mgr.Load()
		if err != nil {
			return err
		}
		inv, err := workspaces.Build(st, cfg)
		if err != nil {
			return err
		}

		candidates := inv.CleanupTargets()
		if len(candidates) == 0 {
			fmt.Fprintln(os.Stderr, "Nothing to clean up.")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Found %d orphaned worktrees\n", len(candidates))

		var selected []workspaces.CleanupTarget
		if all {
			selected = candidates
		} else {
			selected, err = pickCleanupFzf(candidates)
			if err != nil {
				return err
			}
		}
		if len(selected) == 0 {
			return nil
		}

		if !force && !confirmCleanup(selected) {
			fmt.Println("Cancelled.")
			return nil
		}

		for i, t := range selected {
			fmt.Fprintf(os.Stderr, "[%d/%d] Removing %s…\n", i+1, len(selected), t.Label)
			if _, statErr := os.Stat(t.WorktreePath); statErr != nil {
				continue
			}
			if err := git.RemoveWorktree(t.RepoPath, t.WorktreePath); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: failed to remove worktree %s: %v\n", t.WorktreePath, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "  done\n")
		}
		return nil
	},
}

func confirmCleanup(selected []workspaces.CleanupTarget) bool {
	if len(selected) == 1 {
		fmt.Printf("Remove %s? [y/N] ", selected[0].Label)
	} else {
		fmt.Printf("Remove %d worktrees?\n", len(selected))
		for _, t := range selected {
			fmt.Printf("  %s\n", t.Label)
		}
		fmt.Print("[y/N] ")
	}
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(answer)) == "y"
}

func pickCleanupFzf(candidates []workspaces.CleanupTarget) ([]workspaces.CleanupTarget, error) {
	var lines []string
	for i, c := range candidates {
		tag := c.Detail
		if tag == "" {
			tag = "orphan"
		}
		lines = append(lines, fmt.Sprintf("%d\t%-30s\t%s", i, c.Label, tag))
	}

	fzfCmd := exec.Command("fzf",
		"--multi",
		"--prompt", "cleanup > ",
		"--header", "Select orphaned worktrees to remove (Tab to multi-select)",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "2,3",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w", err)
	}

	var selected []workspaces.CleanupTarget
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		idx, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || idx < 0 || idx >= len(candidates) {
			continue
		}
		selected = append(selected, candidates[idx])
	}
	return selected, nil
}
