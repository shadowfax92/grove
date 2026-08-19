package cmd

import (
	"errors"
	"fmt"
	"strings"

	"grove/internal/inventory"

	"github.com/spf13/cobra"
)

type removeOutput struct {
	Version    int            `json:"version"`
	Removed    []removeResult `json:"removed"`
	ReturnPath string         `json:"return_path,omitempty"`
}

type removeResult struct {
	Selector string `json:"selector"`
	Path     string `json:"path"`
}

type removeCandidate struct {
	entry *inventory.Entry
}

func (a *application) removeCommand() *cobra.Command {
	var discard, merged, dryRun bool
	command := &cobra.Command{
		Use:   "rm [selector]",
		Short: "Remove a worktree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && !merged {
				return fmt.Errorf("--dry-run requires --merged")
			}
			if merged && len(args) != 0 {
				return fmt.Errorf("--merged does not accept a selector")
			}
			if merged && discard {
				return fmt.Errorf("--discard cannot be used with --merged")
			}
			return a.runRemove(cmd, args, discard, merged, dryRun)
		},
	}
	command.Flags().BoolVar(&discard, "discard", false, "Discard uncommitted files in one worktree")
	command.Flags().BoolVar(&merged, "merged", false, "Remove all clean worktrees merged into their default branches")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview --merged without removing anything")
	return command
}

func (a *application) runRemove(cmd *cobra.Command, args []string, discard, merged, dryRun bool) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	if merged {
		return a.removeMerged(cmd, context, dryRun)
	}
	var entry *inventory.Entry
	if len(args) == 1 {
		entry, err = context.inventory.Resolve(args[0], context.directory)
	} else {
		entry, err = a.pickWorktree(context, "remove > ")
	}
	if err != nil {
		return err
	}
	if entry.Worktree.Main {
		return fmt.Errorf("refusing to remove the main worktree")
	}
	if entry.Worktree.Locked {
		return lockedError(entry)
	}
	dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
	if err != nil {
		return err
	}
	if dirty && !discard {
		return fmt.Errorf("worktree has uncommitted files; use --discard to remove it")
	}
	if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, discard); err != nil {
		return err
	}
	if a.jsonOutput {
		return writeJSON(cmd, removeOutput{
			Version:    1,
			Removed:    []removeResult{{Selector: entry.Selector(), Path: entry.Worktree.Path}},
			ReturnPath: entry.Repository.Git.MainPath,
		})
	}
	fmt.Fprintln(cmd.OutOrStdout(), entry.Repository.Git.MainPath)
	return nil
}

func (a *application) removeMerged(cmd *cobra.Command, context *commandContext, dryRun bool) error {
	currentPath := ""
	if current, err := context.inventory.Resolve(".", context.directory); err == nil {
		currentPath = current.Worktree.Path
	}
	var candidates []removeCandidate
	for _, entry := range context.inventory.Entries {
		if mergedSkipReason(entry, currentPath) != "" {
			continue
		}
		dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
		if err != nil || dirty {
			continue
		}
		merged, _, err := entry.Repository.Git.BranchMerged(entry.Worktree.Branch, entry.Repository.DefaultBranch)
		if err != nil || !merged {
			continue
		}
		candidates = append(candidates, removeCandidate{entry: entry})
	}

	results := make([]removeResult, 0, len(candidates))
	var failures []error
	for _, candidate := range candidates {
		entry := candidate.entry
		if dryRun {
			results = append(results, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
			continue
		}
		dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", entry.Selector(), err))
			continue
		}
		merged, _, mergeErr := entry.Repository.Git.BranchMerged(entry.Worktree.Branch, entry.Repository.DefaultBranch)
		if dirty || mergeErr != nil || !merged {
			continue
		}
		if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, false); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", entry.Selector(), err))
			continue
		}
		results = append(results, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
	}

	if a.jsonOutput {
		if err := writeJSON(cmd, removeOutput{Version: 1, Removed: results}); err != nil {
			return err
		}
	} else if len(results) == 0 {
		if dryRun {
			fmt.Fprintln(cmd.OutOrStdout(), "No merged worktrees would be removed.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "No merged worktrees removed.")
		}
	} else {
		verb := "removed"
		if dryRun {
			verb = "would remove"
		}
		for _, result := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s  %s\n", verb, result.Selector, result.Path)
		}
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	return nil
}

func mergedSkipReason(entry *inventory.Entry, currentPath string) string {
	switch {
	case entry.Worktree.Main:
		return "main worktree"
	case entry.Worktree.Prunable:
		return "missing worktree"
	case entry.Worktree.Locked:
		return "locked worktree"
	case entry.Worktree.Branch == "":
		return "detached worktree"
	case entry.Worktree.Path == currentPath:
		return "current worktree"
	default:
		return ""
	}
}

func lockedError(entry *inventory.Entry) error {
	reason := strings.TrimSpace(entry.Worktree.LockReason)
	if reason == "" {
		return fmt.Errorf("worktree is locked")
	}
	return fmt.Errorf("worktree is locked: %s", reason)
}
