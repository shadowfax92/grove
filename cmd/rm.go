package cmd

import (
	"errors"
	"fmt"
	"strconv"
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
	age   time.Duration
}

type cleanupAge struct {
	duration time.Duration
	label    string
}

type cleanupSkips struct {
	dirty      int
	locked     int
	missing    int
	current    int
	detached   int
	unsafe     int
	unknownAge int
}

type removeOptions struct {
	discard   bool
	merged    bool
	missing   bool
	dryRun    bool
	olderThan cleanupAge
}

func (a *application) removeCommand() *cobra.Command {
	var discard, merged, missing, dryRun bool
	var olderThanValue string
	command := &cobra.Command{
		Use:   "rm [selector...]",
		Short: "Remove worktrees",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			olderThan, err := parseOlderThan(olderThanValue)
			if err != nil {
				return err
			}
			bulkModes := 0
			if merged {
				bulkModes++
			}
			if olderThan.duration != 0 {
				bulkModes++
			}
			if missing {
				bulkModes++
			}
			if bulkModes > 1 {
				return fmt.Errorf("--merged, --older-than, and --missing cannot be used together")
			}
			if dryRun && bulkModes == 0 {
				return fmt.Errorf("--dry-run requires --merged, --older-than, or --missing")
			}
			if bulkModes != 0 && len(args) != 0 {
				return fmt.Errorf("bulk removal does not accept selectors")
			}
			if discard && (merged || missing) {
				return fmt.Errorf("--discard can only be used with selectors or --older-than")
			}
			if bulkModes != 0 && a.nullOutput {
				return fmt.Errorf("--null is only valid for single-worktree removal")
			}
			return a.runRemove(cmd, args, removeOptions{
				discard:   discard,
				merged:    merged,
				missing:   missing,
				dryRun:    dryRun,
				olderThan: olderThan,
			})
		},
	}
	command.Flags().BoolVar(&discard, "discard", false, "Discard all contents, including uncommitted files and unregistered nested repositories")
	command.Flags().BoolVar(&merged, "merged", false, "Remove all clean worktrees merged into their default branches")
	command.Flags().BoolVar(&missing, "missing", false, "Prune worktree registrations whose directories are gone")
	command.Flags().StringVar(&olderThanValue, "older-than", "", "Remove worktrees older than a duration such as 14d or 4w")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview bulk removal without removing anything")
	return command
}

func (a *application) runRemove(cmd *cobra.Command, args []string, options removeOptions) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	if options.merged {
		return a.removeMerged(cmd, context, options.dryRun)
	}
	if options.olderThan.duration != 0 {
		return a.removeOlderThan(cmd, context, options.olderThan, options.discard, options.dryRun)
	}
	if options.missing {
		return a.removeMissing(cmd, context, options.dryRun)
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
		if err := validateRemoveEntry(context.inventory, entry, options.discard); err != nil {
			return fmt.Errorf("%s: %w", entry.Selector(), err)
		}
	}
	removed := make([]removeResult, 0, len(entries))
	for _, entry := range entries {
		if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, options.discard); err != nil {
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

func parseOlderThan(value string) (cleanupAge, error) {
	if value == "" {
		return cleanupAge{}, nil
	}
	if len(value) < 2 {
		return cleanupAge{}, invalidOlderThan(value)
	}
	quantity, err := strconv.ParseUint(value[:len(value)-1], 10, 64)
	if err != nil || quantity == 0 {
		return cleanupAge{}, invalidOlderThan(value)
	}
	var unit time.Duration
	switch value[len(value)-1] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return cleanupAge{}, invalidOlderThan(value)
	}
	if quantity > uint64((1<<63-1)/unit) {
		return cleanupAge{}, invalidOlderThan(value)
	}
	return cleanupAge{duration: time.Duration(quantity) * unit, label: value}, nil
}

func invalidOlderThan(value string) error {
	return fmt.Errorf("invalid --older-than %q; use a positive duration such as 12h, 14d, or 4w", value)
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
	if err := entry.Repository.Git.ValidateWorktreeRemoval(entry.Worktree.Path, discard); err != nil {
		return err
	}
	if discard {
		return nil
	}
	dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("worktree has uncommitted files; use --discard to remove it")
	}
	return nil
}

func (a *application) removeMissing(cmd *cobra.Command, context *commandContext, dryRun bool) error {
	var candidates []removeCandidate
	var skips cleanupSkips
	for _, entry := range context.inventory.Entries {
		if context.catalog.Current == entry.Repository && !context.catalog.CurrentRegistered {
			continue
		}
		if !entry.Worktree.Prunable {
			continue
		}
		if entry.Worktree.Locked {
			skips.locked++
			continue
		}
		candidates = append(candidates, removeCandidate{entry: entry})
	}

	results := make([]removeResult, 0, len(candidates))
	var failures []error
	if dryRun {
		for _, candidate := range candidates {
			entry := candidate.entry
			results = append(results, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
		}
	} else {
		pruned := make(map[string]bool)
		failed := make(map[string]bool)
		for _, candidate := range candidates {
			repository := candidate.entry.Repository
			key := repository.Git.MainPath
			if pruned[key] || failed[key] {
				continue
			}
			if err := repository.Git.PruneWorktrees(); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", repository.Name, err))
				failed[key] = true
				continue
			}
			pruned[key] = true
		}
		for _, candidate := range candidates {
			entry := candidate.entry
			if pruned[entry.Repository.Git.MainPath] {
				results = append(results, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
			}
		}
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
			fmt.Fprintln(cmd.OutOrStdout(), "No missing worktree registrations would be pruned.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "No missing worktree registrations pruned.")
		}
	} else {
		action := "Pruned"
		if dryRun {
			action = "Would prune"
		}
		style := a.style(cmd.OutOrStdout())
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", style.info(action), worktreeCount(len(results), "missing worktree registration", "missing worktree registrations"), style.muted("(branches kept):"))
		for _, result := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", style.branch(result.Selector))
		}
	}
	a.writeCleanupSkips(cmd, skips)
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	return nil
}

func (a *application) removeOlderThan(cmd *cobra.Command, context *commandContext, threshold cleanupAge, discard, dryRun bool) error {
	currentPath := ""
	if current, err := context.inventory.Resolve(".", context.directory); err == nil {
		currentPath = current.Worktree.Path
	}
	now := time.Now()
	var candidates []removeCandidate
	var skips cleanupSkips
	for _, entry := range context.inventory.Entries {
		if context.catalog.Current == entry.Repository && !context.catalog.CurrentRegistered {
			continue
		}
		if entry.Worktree.Main {
			continue
		}
		if entry.Worktree.Prunable {
			skips.missing++
			continue
		}
		createdAt, ok := worktreeCreatedAt(entry.Worktree.Path)
		if !ok {
			skips.unknownAge++
			continue
		}
		age := now.Sub(createdAt)
		if age < threshold.duration {
			continue
		}
		if entry.Worktree.Locked {
			skips.locked++
			continue
		}
		if entry.Worktree.Branch == "" {
			skips.detached++
			continue
		}
		if entry.Worktree.Path == currentPath {
			skips.current++
			continue
		}
		if !discard {
			dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", entry.Selector(), err)
				skips.unsafe++
				continue
			}
			if dirty {
				skips.dirty++
				continue
			}
		}
		if descendants := context.inventory.Descendants(entry.Worktree.Path); len(descendants) != 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: contains registered worktree %s\n", entry.Selector(), descendants[0].Worktree.Path)
			skips.unsafe++
			continue
		}
		if err := entry.Repository.Git.ValidateWorktreeRemoval(entry.Worktree.Path, discard); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", entry.Selector(), err)
			skips.unsafe++
			continue
		}
		candidates = append(candidates, removeCandidate{entry: entry, age: age})
	}

	results := make([]removeResult, 0, len(candidates))
	ages := make(map[string]time.Duration, len(candidates))
	var failures []error
	for _, candidate := range candidates {
		entry := candidate.entry
		if dryRun {
			result := removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path}
			results = append(results, result)
			ages[result.Path] = candidate.age
			continue
		}
		if !discard {
			dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", entry.Selector(), err))
				continue
			}
			if dirty {
				skips.dirty++
				continue
			}
		}
		if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, discard); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", entry.Selector(), err))
			continue
		}
		result := removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path}
		results = append(results, result)
		ages[result.Path] = candidate.age
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
			fmt.Fprintf(cmd.OutOrStdout(), "No worktrees older than %s would be removed.\n", threshold.label)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "No worktrees older than %s removed.\n", threshold.label)
		}
	} else {
		action := "Removed"
		if dryRun {
			action = "Would remove"
		}
		style := a.style(cmd.OutOrStdout())
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s older than %s %s\n", style.info(action), worktreeCount(len(results), "worktree", "worktrees"), threshold.label, style.muted("(branches kept):"))
		for _, result := range results {
			age := "created " + relativeAge(ages[result.Path]) + " ago"
			fmt.Fprintf(cmd.OutOrStdout(), "  %s  %s\n", style.branch(result.Selector), style.muted(age))
		}
	}
	a.writeCleanupSkips(cmd, skips)
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	return nil
}

func worktreeCount(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func (a *application) writeCleanupSkips(cmd *cobra.Command, skips cleanupSkips) {
	style := a.style(cmd.ErrOrStderr())
	parts := make([]string, 0, 7)
	if skips.dirty != 0 {
		parts = append(parts, fmt.Sprintf("%d dirty %s", skips.dirty, style.muted("(use --discard)")))
	}
	if skips.locked != 0 {
		parts = append(parts, fmt.Sprintf("%d locked", skips.locked))
	}
	if skips.missing != 0 {
		parts = append(parts, fmt.Sprintf("%d missing %s", skips.missing, style.muted("(use --missing)")))
	}
	if skips.current != 0 {
		parts = append(parts, fmt.Sprintf("%d current", skips.current))
	}
	if skips.detached != 0 {
		parts = append(parts, fmt.Sprintf("%d detached", skips.detached))
	}
	if skips.unsafe != 0 {
		parts = append(parts, fmt.Sprintf("%d unsafe", skips.unsafe))
	}
	if skips.unknownAge != 0 {
		parts = append(parts, fmt.Sprintf("%d with unknown age", skips.unknownAge))
	}
	if len(parts) != 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", style.attention("Skipped"), strings.Join(parts, style.muted(" · ")))
	}
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
