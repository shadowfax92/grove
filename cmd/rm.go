package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	contextpkg "context"
	"grove/internal/cleanup"
	"grove/internal/config"
	"grove/internal/footprint"
	gitx "grove/internal/git"
	"grove/internal/inuse"
	"grove/internal/inventory"
	"grove/internal/merged"
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
	Bytes           int64          `json:"bytes,omitempty"`
	UnknownSizes    int            `json:"unknown_sizes,omitempty"`
}

type removeResult struct {
	Selector   string           `json:"selector"`
	Path       string           `json:"path"`
	Footprint  *footprint.Entry `json:"footprint,omitempty"`
	LastActive *time.Time       `json:"last_active,omitempty"`
	InUse      *inuse.Result    `json:"in_use,omitempty"`
	Merged     *merged.Result   `json:"merged,omitempty"`
	Unchecked  []string         `json:"unchecked,omitempty"`
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
	o := cleanupOptions{}
	missing := false
	command := &cobra.Command{Use: "rm [selector...]", Short: "Remove worktrees", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bulk := o.merged || o.inactive != "" || o.older != "" || len(o.repos) > 0
			if missing && (bulk || o.discard) {
				return fmt.Errorf("--missing and other removal selectors cannot be used together (including --discard)")
			}
			if (bulk || missing) && len(args) > 0 {
				return fmt.Errorf("bulk removal does not accept selectors")
			}
			if o.wait && o.dryRun {
				return fmt.Errorf("--wait cannot be used with --dry-run")
			}
			if o.dryRun && !bulk && !missing {
				return fmt.Errorf("--dry-run requires --merged, --older-than, --inactive-for, --repo, or --missing")
			}
			if (bulk || missing) && a.nullOutput {
				return fmt.Errorf("--null is only valid for single-worktree removal")
			}
			if bulk {
				return a.runCleanup(cmd, o)
			}
			return a.runRemove(cmd, args, removeOptions{discard: o.discard, missing: missing, dryRun: o.dryRun, wait: o.wait})
		}}
	cleanupFlags(command, &o)
	command.Flags().BoolVar(&missing, "missing", false, "Prune worktree registrations whose directories are gone")
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
	snapshotContext, cancel := contextpkg.WithTimeout(cmd.Context(), 3*time.Second)
	snapshot := inuse.Take(snapshotContext)
	cancel()
	lookup := a.dependencies.inUse
	if lookup == nil {
		lookup = snapshot.Lookup
	}
	checks := checkRemoveEntries(entries, func(entry *inventory.Entry) removalCheck {
		removal, err := entry.Repository.Git.PrepareWorktreeRemoval(entry.Worktree.Path, options.discard)
		if err == nil && len(context.inventory.Descendants(entry.Worktree.Path)) > 0 {
			err = fmt.Errorf("contains registered worktree %s", context.inventory.Descendants(entry.Worktree.Path)[0].Worktree.Path)
		}
		if err == nil && !options.discard {
			dirty, e := entry.Repository.Git.Dirty(entry.Worktree.Path)
			err = e
			if dirty {
				err = fmt.Errorf("worktree has uncommitted files; use --discard to remove it")
			}
		}
		return removalCheck{err: err, removal: removal}
	})
	for i, check := range checks {
		if check.err != nil {
			return fmt.Errorf("%s: %w", entries[i].Selector(), check.err)
		}
	}
	for _, entry := range entries {
		usage := lookup(entry.Worktree.Path)
		if usage.State == inuse.InUse {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: removing deliberately selected %s in use by %s\n", entry.Selector(), cleanup.Processes(usage))
		} else if usage.State != inuse.Free {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: in-use check unavailable for %s: %s\n", entry.Selector(), usage.Error)
		}
	}
	removed := make([]removeResult, 0, len(entries))
	for i, entry := range entries {
		if err := checks[i].removal.Remove(); err != nil {
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
	entries := []*inventory.Entry{}
	for _, entry := range context.inventory.Entries {
		if !entry.Worktree.Main && !entry.Worktree.Prunable {
			entries = append(entries, entry)
		}
	}
	rows := cleanup.Collect(contextpkg.Background(), context.inventory, entries, true, a.dependencies.inUse)
	cleanup.SortByActivity(rows)
	current := ""
	if e, err := context.inventory.Resolve(".", context.directory); err == nil {
		current = e.Worktree.Path
	}
	items := []picker.Item{}
	for _, r := range rows {
		items = append(items, picker.Item{Key: r.Entry.Worktree.Path, Label: cleanupLabel(r, time.Now()), Selected: r.Preselected(time.Now(), 72*time.Hour, current, context.inventory)})
	}
	paths, err := a.dependencies.pickMany("remove > ", items)
	if err != nil {
		return nil, err
	}
	entries = make([]*inventory.Entry, 0, len(paths))
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
	removal     *gitx.Removal
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
