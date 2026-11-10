package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"grove/internal/config"
	"grove/internal/inventory"
	"grove/internal/picker"
	"grove/internal/trash"

	"github.com/spf13/cobra"
)

type removeOutput struct {
	Version         int            `json:"version"`
	DryRun          bool           `json:"dry_run"`
	Removed         []removeResult `json:"removed"`
	WouldRemove     []removeResult `json:"would_remove"`
	ReturnPath      string         `json:"return_path,omitempty"`
	PendingDeletion []trash.Entry  `json:"pending_deletion,omitempty"`
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
	wait      bool
	olderThan cleanupAge
}

func (a *application) removeCommand() *cobra.Command {
	var discard, merged, missing, dryRun, wait bool
	var olderThanValue string
	command := &cobra.Command{
		Use:   "rm [selector...]",
		Short: "Remove worktrees",
		Long:  "Remove worktrees and clean up config entries for missing repository directories after successful removal. Bulk dry runs preview both kinds of cleanup.",
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
			if dryRun && wait {
				return fmt.Errorf("--wait cannot be used with --dry-run")
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
				wait:      wait,
				olderThan: olderThan,
			})
		},
	}
	command.Flags().BoolVar(&discard, "discard", false, "Discard all contents, including uncommitted files and unregistered nested repositories")
	command.Flags().BoolVar(&merged, "merged", false, "Remove all clean worktrees merged into their default branches")
	command.Flags().BoolVar(&missing, "missing", false, "Prune worktree registrations whose directories are gone")
	command.Flags().StringVar(&olderThanValue, "older-than", "", "Remove worktrees older than a duration such as 14d or 4w")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for physical deletion before returning")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview bulk removal without removing anything")
	return command
}

func (a *application) runRemove(cmd *cobra.Command, args []string, options removeOptions) (resultErr error) {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	// Cleanup runs after every successful mode, including an empty bulk pass.
	// Cancellation and removal failures leave config untouched; checking after
	// removal also catches profiles pointing into the worktree just deleted.
	defer func() {
		if resultErr == nil {
			a.pruneMissingRepos(cmd, options.dryRun)
		}
	}()
	if options.merged {
		return a.removeMerged(cmd, context, options.dryRun, options.wait)
	}
	if options.olderThan.duration != 0 {
		return a.removeOlderThan(cmd, context, options.olderThan, options.discard, options.dryRun, options.wait)
	}
	if options.missing {
		if options.wait {
			if _, err := finishRemoval(cmd, true, options.dryRun); err != nil {
				return err
			}
		}
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
	checks := checkRemoveEntries(entries, func(entry *inventory.Entry) removalCheck {
		return removalCheck{err: validateRemoveEntry(context.inventory, entry, options.discard)}
	})
	for i, check := range checks {
		if check.err != nil {
			return fmt.Errorf("%s: %w", entries[i].Selector(), check.err)
		}
	}
	removed := make([]removeResult, 0, len(entries))
	for _, entry := range entries {
		if err := entry.Repository.Git.RemoveWorktree(entry.Worktree.Path, options.discard); err != nil {
			return fmt.Errorf("%s: %w", entry.Selector(), err)
		}
		removed = append(removed, removeResult{Selector: entry.Selector(), Path: entry.Worktree.Path})
	}
	pending, err := finishRemoval(cmd, options.wait, false)
	if err != nil {
		return err
	}
	if a.jsonOutput {
		return writeJSON(cmd, removeOutput{
			Version:         1,
			Removed:         removed,
			WouldRemove:     []removeResult{},
			ReturnPath:      returnPath,
			PendingDeletion: pending,
		})
	}
	return a.writePath(cmd, returnPath)
}

func (a *application) pruneMissingRepos(cmd *cobra.Command, dryRun bool) {
	path, err := config.DefaultConfigPath()
	var removed []config.RepoConfig
	if err == nil {
		removed, err = config.PruneMissingRepos(path, dryRun)
	}
	// Config housekeeping must not turn a completed Git removal into a failure.
	// Use stderr so path/NUL output remains usable by gv and JSON stays valid.
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: cleaning stale repository entries: %v\n", err)
		return
	}
	if len(removed) == 0 {
		return
	}
	action := "Pruned"
	if dryRun {
		action = "Would prune"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s from config:\n", action, worktreeCount(len(removed), "stale repository entry", "stale repository entries"))
	for _, repo := range removed {
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%s)\n", repo.Name, repo.Path)
	}
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

func (a *application) removeOlderThan(cmd *cobra.Command, context *commandContext, threshold cleanupAge, discard, dryRun, wait bool) error {
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
		candidates = append(candidates, removeCandidate{entry: entry, age: age})
	}
	// Tree walks and Git status dominate large previews. Eight candidates at a
	// time bound subprocess and filesystem pressure; aggregation stays in inventory
	// order so warnings and machine-readable output remain deterministic.
	entries := make([]*inventory.Entry, len(candidates))
	for i, candidate := range candidates {
		entries[i] = candidate.entry
	}
	checks := checkRemoveEntries(entries, func(entry *inventory.Entry) removalCheck {
		if !discard {
			dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
			if err != nil {
				return removalCheck{err: err}
			}
			if dirty {
				return removalCheck{dirty: true}
			}
		}
		if descendants := context.inventory.Descendants(entry.Worktree.Path); len(descendants) != 0 {
			return removalCheck{err: fmt.Errorf("contains registered worktree %s", descendants[0].Worktree.Path)}
		}
		return removalCheck{err: entry.Repository.Git.ValidateWorktreeRemoval(entry.Worktree.Path, discard)}
	})
	filtered := candidates[:0]
	for i, check := range checks {
		if check.dirty {
			skips.dirty++
			continue
		}
		if check.err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", entries[i].Selector(), check.err)
			skips.unsafe++
			continue
		}
		filtered = append(filtered, candidates[i])
	}
	candidates = filtered

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

	pending, finishErr := finishRemoval(cmd, wait, dryRun)
	if finishErr != nil {
		failures = append(failures, finishErr)
	}

	if a.jsonOutput {
		output := removeOutput{Version: 1, DryRun: dryRun, Removed: []removeResult{}, WouldRemove: []removeResult{}, PendingDeletion: pending}
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

func (a *application) removeMerged(cmd *cobra.Command, context *commandContext, dryRun, wait bool) error {
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
		candidates = append(candidates, removeCandidate{entry: entry})
	}
	entries := make([]*inventory.Entry, len(candidates))
	for i, candidate := range candidates {
		entries[i] = candidate.entry
	}
	checks := checkRemoveEntries(entries, func(entry *inventory.Entry) removalCheck {
		if descendants := context.inventory.Descendants(entry.Worktree.Path); len(descendants) != 0 {
			return removalCheck{err: fmt.Errorf("contains registered worktree %s", descendants[0].Worktree.Path)}
		}
		dirty, err := entry.Repository.Git.Dirty(entry.Worktree.Path)
		if err != nil {
			return removalCheck{err: err}
		}
		if dirty {
			return removalCheck{dirty: true}
		}
		merged, _, err := entry.Repository.Git.BranchMerged(entry.Worktree.Branch, entry.Repository.DefaultBranch)
		if err != nil {
			return removalCheck{err: err}
		}
		if !merged {
			return removalCheck{skip: true}
		}
		return removalCheck{err: entry.Repository.Git.ValidateWorktreeRemoval(entry.Worktree.Path, false)}
	})
	filtered := candidates[:0]
	for i, check := range checks {
		if check.err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", entries[i].Selector(), check.err)
			continue
		}
		if !check.dirty && !check.skip {
			filtered = append(filtered, candidates[i])
		}
	}
	candidates = filtered

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

	pending, finishErr := finishRemoval(cmd, wait, dryRun)
	if finishErr != nil {
		failures = append(failures, finishErr)
	}

	if a.jsonOutput {
		output := removeOutput{Version: 1, DryRun: dryRun, Removed: []removeResult{}, WouldRemove: []removeResult{}, PendingDeletion: pending}
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

// removalCheck carries worker results back to the output owner. Workers never
// write command streams or shared counters, which keeps bulk diagnostics stable.
type removalCheck struct {
	dirty, skip bool
	err         error
}

func checkRemoveEntries(entries []*inventory.Entry, check func(*inventory.Entry) removalCheck) []removalCheck {
	results := make([]removalCheck, len(entries))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i] = check(entries[i])
			}
		}()
	}
	for i := range entries {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return results
}

func finishRemoval(cmd *cobra.Command, wait, dryRun bool) ([]trash.Entry, error) {
	if dryRun {
		return nil, nil
	}
	manager, err := trash.Default()
	if err != nil {
		if wait {
			return nil, err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: pending deletion: %v\n", err)
		return nil, nil
	}
	if wait {
		if err = manager.Reap(cmd.Context()); err != nil {
			return nil, err
		}
	}
	pending, err := manager.Pending()
	// Optional housekeeping must not corrupt path or JSON stdout.
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: pending deletion: %v\n", err)
	}
	return pending, nil
}
