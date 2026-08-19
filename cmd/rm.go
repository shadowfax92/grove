package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"grove/internal/inventory"
	"grove/internal/picker"

	"github.com/spf13/cobra"
)

type removeOutput struct {
	Version     int            `json:"version"`
	DryRun      bool           `json:"dry_run"`
	Removed     []removeResult `json:"removed"`
	WouldRemove []removeResult `json:"would_remove"`
	ReturnPath  string         `json:"return_path,omitempty"`
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
		Use:   "rm [selector...]",
		Short: "Remove worktrees",
		Args:  cobra.ArbitraryArgs,
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
			if merged && a.nullOutput {
				return fmt.Errorf("--null is only valid for single-worktree removal")
			}
			return a.runRemove(cmd, args, discard, merged, dryRun)
		},
	}
	command.Flags().BoolVar(&discard, "discard", false, "Discard uncommitted files in selected worktrees")
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
	var entries []*inventory.Entry
	if len(args) != 0 {
		for _, selector := range args {
			entry, resolveErr := context.inventory.Resolve(selector, context.directory)
			if resolveErr != nil {
				return resolveErr
			}
			entries = appendUniqueEntry(entries, entry)
		}
	} else {
		entries, err = a.pickWorktreesForRemoval(context)
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return picker.ErrCancelled
	}
	returnPath := removeReturnPath(context, entries)
	if err := a.validatePathOutput(returnPath); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := validateRemoveEntry(context.inventory, entry, discard); err != nil {
			return fmt.Errorf("%s: %w", entry.Selector(), err)
		}
	}
	removed := make([]removeResult, 0, len(entries))
	for _, entry := range entries {
		if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, discard); err != nil {
			return fmt.Errorf("%s: %w", entry.Selector(), err)
		}
		removed = append(removed, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
	}
	if a.jsonOutput {
		return writeJSON(cmd, removeOutput{
			Version:     1,
			Removed:     removed,
			WouldRemove: []removeResult{},
			ReturnPath:  returnPath,
		})
	}
	return a.writePath(cmd, returnPath)
}

func removeReturnPath(context *commandContext, entries []*inventory.Entry) string {
	current, err := context.inventory.Resolve(".", context.directory)
	if err != nil {
		return entries[0].Repository.Git.MainPath
	}
	for _, entry := range entries {
		if entry.Worktree.Path == current.Worktree.Path {
			return current.Repository.Git.MainPath
		}
	}
	return current.Worktree.Path
}

func (a *application) pickWorktreesForRemoval(context *commandContext) ([]*inventory.Entry, error) {
	if a.noInput || !a.dependencies.interactive() {
		return nil, fmt.Errorf("selector is required in non-interactive mode")
	}
	items := make([]picker.Item, 0, len(context.inventory.Entries))
	now := time.Now()
	for _, entry := range context.inventory.Entries {
		if entry.Worktree.Main || entry.Worktree.Prunable {
			continue
		}
		label := entry.Selector()
		if age := worktreeCreationLabel(entry.Worktree.Path, now); age != "" {
			label = fmt.Sprintf("%-36s %s", label, age)
		}
		if entry.Worktree.Locked {
			label += "  [locked]"
		}
		items = append(items, picker.Item{Key: entry.Worktree.Path, Label: label})
	}
	paths, err := a.dependencies.pickMany("remove > ", items)
	if err != nil {
		return nil, err
	}
	entries := make([]*inventory.Entry, 0, len(paths))
	for _, path := range paths {
		entry, resolveErr := context.inventory.Resolve(path, context.directory)
		if resolveErr != nil {
			return nil, resolveErr
		}
		entries = appendUniqueEntry(entries, entry)
	}
	return entries, nil
}

func appendUniqueEntry(entries []*inventory.Entry, candidate *inventory.Entry) []*inventory.Entry {
	for _, entry := range entries {
		if entry.Worktree.Path == candidate.Worktree.Path {
			return entries
		}
	}
	return append(entries, candidate)
}

func validateRemoveEntry(inv *inventory.Inventory, entry *inventory.Entry, discard bool) error {
	if entry.Worktree.Main {
		return fmt.Errorf("refusing to remove the main worktree")
	}
	if entry.Worktree.Locked {
		return lockedError(entry)
	}
	if descendants := inv.Descendants(entry.Worktree.Path); len(descendants) != 0 {
		return fmt.Errorf("refusing to remove %s because it contains registered worktree %s", entry.Worktree.Path, descendants[0].Worktree.Path)
	}
	if err := entry.Repository.Git.ValidateWorktreeRemoval(entry.Worktree.Path); err != nil {
		return err
	}
	dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
	if err != nil {
		return err
	}
	if dirty && !discard {
		return fmt.Errorf("worktree has uncommitted files; use --discard to remove it")
	}
	return nil
}

func (a *application) removeMerged(cmd *cobra.Command, context *commandContext, dryRun bool) error {
	currentPath := ""
	if current, err := context.inventory.Resolve(".", context.directory); err == nil {
		currentPath = current.Worktree.Path
	}
	var candidates []removeCandidate
	for _, entry := range context.inventory.Entries {
		if context.catalog.Current == entry.Repository && !context.catalog.CurrentRegistered {
			continue
		}
		if mergedSkipReason(entry, currentPath) != "" {
			continue
		}
		if descendants := context.inventory.Descendants(entry.Worktree.Path); len(descendants) != 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: contains registered worktree %s\n", entry.Selector(), descendants[0].Worktree.Path)
			continue
		}
		dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", entry.Selector(), err)
			continue
		}
		if dirty {
			continue
		}
		merged, _, err := entry.Repository.Git.BranchMerged(entry.Worktree.Branch, entry.Repository.DefaultBranch)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", entry.Selector(), err)
			continue
		}
		if !merged {
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
		if mergeErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", entry.Selector(), mergeErr))
			continue
		}
		if dirty || !merged {
			continue
		}
		if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, false); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", entry.Selector(), err))
			continue
		}
		results = append(results, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
	}

	if a.jsonOutput {
		output := removeOutput{Version: 1, DryRun: dryRun, Removed: []removeResult{}, WouldRemove: []removeResult{}}
		if dryRun {
			output.WouldRemove = results
		} else {
			output.Removed = results
		}
		if err := writeJSON(cmd, output); err != nil {
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
