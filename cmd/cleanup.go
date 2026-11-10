package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"grove/internal/activity"
	"grove/internal/catalog"
	"grove/internal/cleanup"
	gitx "grove/internal/git"
	"grove/internal/inuse"
	"grove/internal/inventory"
	"grove/internal/picker"
	"grove/internal/trash"

	"github.com/spf13/cobra"
)

// cleanupOptions describes both guided and headless selection. Predicates are
// intersected; repository scope is resolved before any activity/forge work.
type cleanupOptions struct {
	repos                                                     []string
	automatic                                                 bool
	caller                                                    string
	inactive, older, idle                                     string
	merged, forge, dryRun, discard, wait, allowUnknown, sweep bool
}

func (a *application) cleanupCommand() *cobra.Command {
	o := cleanupOptions{}
	c := &cobra.Command{Use: "cleanup", Short: "Choose projects and worktrees to clear, or preview a filtered cleanup", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.runCleanup(cmd, o) }}
	cleanupFlags(c, &o)
	c.Flags().BoolVar(&o.automatic, "automatic", false, "Detached sweep worker")
	c.Flags().StringVar(&o.caller, "caller-directory", "", "Protect the launching worktree")
	_ = c.Flags().MarkHidden("automatic")
	_ = c.Flags().MarkHidden("caller-directory")
	c.Flags().StringVar(&o.idle, "idle-after", "", "Preselect worktrees idle this long (config cleanup.idle_after; default 3d)")
	c.Flags().BoolVar(&o.sweep, "sweep", false, "Use automatic cleanup rules now (merged, clean, idle past grace period)")
	return c
}
func cleanupFlags(c *cobra.Command, o *cleanupOptions) {
	c.Flags().StringSliceVar(&o.repos, "repo", nil, "Limit to repository names/aliases (repeat or comma-separate)")
	c.Flags().StringVar(&o.inactive, "inactive-for", "", "Select worktrees inactive for a duration such as 3d")
	c.Flags().StringVar(&o.older, "older-than", "", "Select worktrees created at least this long ago, such as 14d")
	c.Flags().BoolVar(&o.merged, "merged", false, "Select worktrees merged at their current commit")
	c.Flags().BoolVar(&o.forge, "forge", true, "Include cached/GitHub merged PR evidence")
	c.Flags().BoolVar(&o.dryRun, "dry-run", false, "Preview from cheap/cached facts; defer dirty and nested-repository checks")
	c.Flags().BoolVar(&o.discard, "discard", false, "Discard uncommitted files and unregistered nested repositories")
	c.Flags().BoolVar(&o.wait, "wait", false, "Wait for physical deletion")
	c.Flags().BoolVar(&o.allowUnknown, "allow-unknown-in-use", false, "Allow bulk selection when process visibility is unknown (live worktrees still skipped)")
}
func (a *application) runCleanup(cmd *cobra.Command, o cleanupOptions) (resultErr error) {
	if a.nullOutput {
		return fmt.Errorf("cleanup does not support --null; use --json")
	}
	if o.dryRun && o.wait {
		return fmt.Errorf("--wait cannot be used with --dry-run")
	}
	data, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	if o.automatic {
		return a.runAutomaticSweep(cmd, data, o)
	}
	defer func() {
		if resultErr == nil {
			a.pruneMissingRepos(cmd, o.dryRun)
		}
	}()
	return a.cleanupWithContext(cmd, data, o)
}
func (a *application) cleanupWithContext(cmd *cobra.Command, data *commandContext, o cleanupOptions) error {
	selection, err := cleanupSelection(data, o)
	if err != nil {
		return err
	}
	entries, err := scopedCleanupEntries(data, o.repos)
	if err != nil {
		return err
	}
	if o.caller != "" {
		if current, err := data.inventory.Resolve(o.caller, data.directory); err == nil {
			kept := []*inventory.Entry{}
			for _, e := range entries {
				if e.Worktree.Path != current.Worktree.Path {
					kept = append(kept, e)
				}
			}
			entries = kept
		}
	}
	rows := cleanup.Collect(cmd.Context(), data.inventory, entries, o.forge, a.dependencies.inUse)
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	// Cache misses and stale entries never put a footprint walk in front of a
	// picker. Dry runs do not launch even cache-only children, keeping them read-only.
	if !o.automatic {
		a.refreshCleanupSizes(cmd, data, rows, o.dryRun)
	}
	guided := !o.sweep && !o.merged && o.inactive == "" && o.older == "" && !o.dryRun && !a.noInput && a.dependencies.interactive()
	if guided {
		idle := 72 * time.Hour
		value := o.idle
		if value == "" {
			value = data.config.Cleanup.IdleAfter
		}
		if value != "" {
			idle, err = cleanup.Duration(value)
			if err != nil {
				return err
			}
		}
		rows, err = a.pickCleanupRows(data, rows, idle, len(o.repos) == 0)
		if err != nil {
			return err
		}
	} else {
		if !o.sweep && !o.merged && o.inactive == "" && o.older == "" && len(o.repos) == 0 {
			// A noninteractive bare cleanup previews/selects idle work, never all trees.
			idle := data.config.Cleanup.IdleAfter
			if idle == "" {
				idle = "3d"
			}
			selection.InactiveFor, err = cleanup.Duration(idle)
			if err != nil {
				return err
			}
		}
		rows, err = cleanup.Select(rows, data.inventory, time.Now(), selection)
		if err != nil {
			return err
		}
	}
	if o.dryRun {
		if o.older != "" && !a.jsonOutput {
			fmt.Fprintf(cmd.OutOrStdout(), "Worktrees older than %s:\n", o.older)
		}
		return a.writeCleanupResult(cmd, rows, nil, true)
	}
	return a.removeCleanupRows(cmd, data, rows, o, guided)
}
func cleanupSelection(data *commandContext, o cleanupOptions) (cleanup.Selection, error) {
	s := cleanup.Selection{Merged: o.merged, AllowUnknown: o.allowUnknown}
	if current, err := data.inventory.Resolve(".", data.directory); err == nil {
		s.Current = current.Worktree.Path
	}
	var err error
	if o.inactive != "" {
		s.InactiveFor, err = cleanup.Duration(o.inactive)
		if err != nil {
			return s, err
		}
	}
	if o.older != "" {
		s.OlderThan, err = cleanup.Duration(o.older)
		if err != nil {
			return s, fmt.Errorf("invalid --older-than: %w", err)
		}
	}
	if o.sweep {
		if o.discard || o.allowUnknown {
			return s, fmt.Errorf("--sweep never allows --discard or --allow-unknown-in-use")
		}
		settings, err := cleanupSettings(data.config.Cleanup)
		if err != nil {
			return s, err
		}
		s.Merged = true
		if s.InactiveFor < settings.grace {
			s.InactiveFor = settings.grace
		}
	}
	return s, nil
}
func scopedCleanupEntries(data *commandContext, names []string) ([]*inventory.Entry, error) {
	selected := map[*catalog.Repository]bool{}
	for _, name := range names {
		repo, _, err := data.catalog.FindRepository(name)
		if err != nil {
			return nil, err
		}
		selected[repo] = true
	}
	entries := []*inventory.Entry{}
	for _, e := range data.inventory.Entries {
		if len(names) > 0 && !selected[e.Repository] {
			continue
		}
		if data.catalog.Current == e.Repository && !data.catalog.CurrentRegistered {
			continue
		}
		if !e.Worktree.Main && !e.Worktree.Prunable {
			entries = append(entries, e)
		}
	}
	return entries, nil
}
func (a *application) refreshCleanupSizes(cmd *cobra.Command, data *commandContext, rows []cleanup.Row, dry bool) {
	if dry {
		return
	}
	needed := false
	repos := map[string]bool{}
	for _, r := range rows {
		repos[r.Entry.Repository.Name] = true
		if r.Footprint == nil || r.Stale {
			needed = true
		}
	}
	if !needed {
		return
	}
	args := []string{"-C", data.directory, "du", "--background-refresh", "--"}
	names := []string{}
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	args = append(args, names...)
	launch := a.dependencies.startFootprintRefresh
	if launch == nil {
		launch = startFootprintRefresh
	}
	if err := launch(args); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: background size refresh: %v\n", err)
	} else {
		fmt.Fprintln(cmd.ErrOrStderr(), "Missing/stale sizes are refreshing in the background; cached sizes are estimates.")
	}
}
func (a *application) pickCleanupRows(data *commandContext, rows []cleanup.Row, idle time.Duration, projects bool) ([]cleanup.Row, error) {
	if projects {
		type project struct {
			name, key              string
			count, merged, unknown int
			bytes, idle            int64
		}
		groups := map[string]*project{}
		for _, r := range rows {
			key := r.Entry.Repository.Git.CommonDir
			p := groups[key]
			if p == nil {
				p = &project{name: r.Entry.Repository.Name, key: key}
				groups[key] = p
			}
			p.count++
			p.bytes += r.Bytes()
			if r.Idle(time.Now(), idle) && r.InUse.State == inuse.Free {
				p.idle += r.Bytes()
			}
			if r.Merged.Merged {
				p.merged++
			}
			if r.Footprint == nil {
				p.unknown++
			}
		}
		sorted := []*project{}
		for _, p := range groups {
			sorted = append(sorted, p)
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].bytes > sorted[j].bytes })
		items := []picker.Item{}
		for _, p := range sorted {
			items = append(items, picker.Item{Key: p.key, Label: fmt.Sprintf("%-24s %3d worktrees  %10s cached  %10s idle  %d merged  %d sizes unknown", p.name, p.count, diskSize(p.bytes), diskSize(p.idle), p.merged, p.unknown)})
		}
		keys, err := a.dependencies.pickMany("projects > ", items)
		if err != nil {
			return nil, err
		}
		chosen := map[string]bool{}
		for _, key := range keys {
			chosen[key] = true
		}
		scoped := []cleanup.Row{}
		for _, r := range rows {
			if chosen[r.Entry.Repository.Git.CommonDir] {
				scoped = append(scoped, r)
			}
		}
		rows = scoped
	}
	cleanup.SortByActivity(rows)
	current := ""
	if e, err := data.inventory.Resolve(".", data.directory); err == nil {
		current = e.Worktree.Path
	}
	items := []picker.Item{}
	for _, r := range rows {
		items = append(items, picker.Item{Key: r.Entry.Worktree.Path, Label: cleanupLabel(r, time.Now()), Selected: r.Preselected(time.Now(), idle, current, data.inventory)})
	}
	paths, err := a.dependencies.pickMany("worktrees > ", items)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, p := range paths {
		selected[p] = true
	}
	out := []cleanup.Row{}
	for _, r := range rows {
		if selected[r.Entry.Worktree.Path] {
			if reason := cleanup.Protected(r.Entry, current, data.inventory); reason != "" {
				return nil, fmt.Errorf("%s: %s", r.Entry.Selector(), reason)
			}
			out = append(out, r)
		}
	}
	return out, nil
}
func ago(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return relativeAge(d) + " ago"
}
func cleanupLabel(r cleanup.Row, now time.Time) string {
	size := "size unknown"
	if r.Footprint != nil {
		size = diskSize(r.Bytes()) + " measured " + ago(now.Sub(r.Footprint.ScannedAt))
		if r.Stale {
			size += "; stale"
		}
	}
	active := "last active unknown"
	if r.Activity.Error == "" && !r.Activity.LastActive.IsZero() {
		active = "last active " + ago(now.Sub(r.Activity.LastActive))
	}
	created := "creation unknown"
	if !r.CreatedAt.IsZero() {
		created = "created " + ago(now.Sub(r.CreatedAt))
	}
	flags := []string{"uncommitted unchecked", "nested repositories unchecked"}
	if r.InUse.State == inuse.InUse {
		flags = append(flags, "in use: "+cleanup.Processes(r.InUse))
	} else if r.InUse.State != inuse.Free {
		flags = append(flags, "in use unknown")
	}
	if r.Merged.Merged {
		flags = append(flags, "merged: "+r.Merged.Reason)
	} else if len(r.Merged.Skipped) > 0 {
		flags = append(flags, strings.Join(r.Merged.Skipped, "; "))
	}
	if r.Entry.Worktree.Locked {
		flags = append(flags, "locked")
	}
	return fmt.Sprintf("%-36s %s  %s  %s  [%s]", r.Entry.Selector(), size, active, created, strings.Join(flags, "; "))
}

// Only selected worktrees get authoritative checks. Workers report into ordered
// slots; the command owns progress and prompts, so JSON/path stdout stays clean.
type preparedCleanup struct {
	row     cleanup.Row
	dirty   bool
	removal *gitx.Removal
	err     error
}

func prepareCleanup(cmd *cobra.Command, data *commandContext, rows []cleanup.Row, discard bool) []preparedCleanup {
	results := make([]preparedCleanup, len(rows))
	jobs := make(chan int)
	done := make(chan struct{}, len(rows))
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				r := rows[i]
				p := preparedCleanup{row: r}
				if cmd.Context().Err() != nil {
					p.err = cmd.Context().Err()
				} else if len(data.inventory.Descendants(r.Entry.Worktree.Path)) > 0 {
					p.err = fmt.Errorf("contains registered worktree")
				} else {
					p.dirty, p.err = r.Entry.Repository.Git.Dirty(r.Entry.Worktree.Path)
					if discard {
						p.err = nil
					}
					// Check nested repositories even when dirty, before asking permission. Dirty
					// confirmation can then authorize discard, rather than bypass a failed walk.
					if p.err == nil {
						p.removal, p.err = r.Entry.Repository.Git.PrepareWorktreeRemoval(r.Entry.Worktree.Path, discard)
					}
				}
				results[i] = p
				done <- struct{}{}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range rows {
			jobs <- i
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	completed := 0
	if len(rows) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Checking selected worktrees: 0/%d (uncommitted files and nested repositories)\n", len(rows))
	}
	for completed < len(rows) {
		select {
		case <-done:
			completed++
		case <-ticker.C:
			fmt.Fprintf(cmd.ErrOrStderr(), "Checking selected worktrees: %d/%d\n", completed, len(rows))
		}
	}
	workers.Wait()
	return results
}
func (a *application) confirmCleanup(cmd *cobra.Command, rows []cleanup.Row, dirty int, discard bool) error {
	var bytes int64
	unknown := 0
	for _, r := range rows {
		bytes += r.Bytes()
		if r.Footprint == nil {
			unknown++
		}
	}
	prompt := fmt.Sprintf("Remove %d worktrees (~%s cached; %d sizes unknown; %d with uncommitted files), keeping branches?", len(rows), diskSize(bytes), unknown, dirty)
	if dirty > 0 && !discard {
		prompt += " This discards their uncommitted files."
	}
	confirm := a.dependencies.confirm
	if confirm != nil {
		ok, err := confirm(prompt)
		if err != nil {
			return err
		}
		if !ok {
			return picker.ErrCancelled
		}
		return nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), prompt+" [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(strings.ToLower(line)) != "yes" && strings.TrimSpace(strings.ToLower(line)) != "y" {
		return picker.ErrCancelled
	}
	return nil
}
func (a *application) removeCleanupRows(cmd *cobra.Command, data *commandContext, rows []cleanup.Row, o cleanupOptions, guided bool) error {
	checks := prepareCleanup(cmd, data, rows, o.discard)
	dirty := 0
	ready := []preparedCleanup{}
	for _, p := range checks {
		if p.err != nil {
			if guided {
				return fmt.Errorf("%s: %w", p.row.Entry.Selector(), p.err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", p.row.Entry.Selector(), p.err)
			continue
		}
		if p.dirty {
			dirty++
			if !guided && !o.discard {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: uncommitted files (use --discard)\n", p.row.Entry.Selector())
				continue
			}
		}
		ready = append(ready, p)
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	rows = []cleanup.Row{}
	for _, p := range ready {
		rows = append(rows, p.row)
	}
	if guided && len(rows) > 0 {
		if err := a.confirmCleanup(cmd, rows, dirty, o.discard); err != nil {
			return err
		}
	}
	// Explicit confirmation of dirty work is the only interactive equivalent of
	// --discard. Keep clean worktrees on the conservative removal path.
	for i, p := range ready {
		if p.dirty && !o.discard {
			var err error
			ready[i].removal, err = p.row.Entry.Repository.Git.PrepareWorktreeRemoval(p.row.Entry.Worktree.Path, true)
			if err != nil {
				return err
			}
		}
	}
	lookup := a.dependencies.inUse
	if lookup == nil {
		snapshotCtx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
		snapshot := inuse.Take(snapshotCtx)
		cancel()
		lookup = snapshot.Lookup
	}
	// Resolve the final snapshot for the whole selection before the first
	// detach. A collector failure must fail bulk removal, not become a successful
	// empty pass that can still prune config.
	usageByPath := map[string]inuse.Result{}
	for _, p := range ready {
		usage := lookup(p.row.Entry.Worktree.Path)
		usageByPath[p.row.Entry.Worktree.Path] = usage
		if !guided && usage.State != inuse.Free && usage.State != inuse.InUse && !o.allowUnknown {
			return fmt.Errorf("in-use check unavailable for %s: %s; use --allow-unknown-in-use to proceed", p.row.Entry.Selector(), usage.Error)
		}
	}
	removed := []cleanup.Row{}
	var failures []error
	for _, p := range ready {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		usage := usageByPath[p.row.Entry.Worktree.Path]
		if !guided && (usage.State == inuse.InUse || usage.State != inuse.Free && !o.allowUnknown) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: in-use state %s %s\n", p.row.Entry.Selector(), usage.State, cleanup.Processes(usage))
			continue
		}
		if guided && usage.State != inuse.Free {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: deliberately selected %s: in-use state %s %s\n", p.row.Entry.Selector(), usage.State, cleanup.Processes(usage))
		}
		if o.sweep {
			settings, err := cleanupSettings(data.config.Cleanup)
			if err != nil {
				return err
			}
			active := activity.Measure(cmd.Context(), p.row.Entry.Worktree.Path)
			if active.Error != "" || active.LastActive.IsZero() || active.LastActive.After(time.Now().Add(-settings.grace)) {
				continue
			}
		}
		if o.merged || o.sweep {
			if err := revalidateCleanupTip(p.row); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		row := p.row
		p.removal.Guard(func() error {
			// Another detach can hold the root lock for a long time. Recheck
			// destructive policy here, after that wait, rather than trusting
			// the earlier preview/preflight snapshot across an async handoff.
			if !guided {
				usage := inuse.Result{}
				if a.dependencies.inUse != nil {
					usage = a.dependencies.inUse(row.Entry.Worktree.Path)
				} else {
					bounded, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
					fresh := inuse.Take(bounded)
					cancel()
					usage = fresh.Lookup(row.Entry.Worktree.Path)
				}
				if usage.State == inuse.InUse {
					return fmt.Errorf("worktree became in use: %s", cleanup.Processes(usage))
				}
				if usage.State != inuse.Free && !o.allowUnknown {
					return fmt.Errorf("in-use check unavailable: %s", usage.Error)
				}
			}
			if o.merged || o.sweep {
				if err := revalidateCleanupTip(row); err != nil {
					return err
				}
			}
			if o.sweep {
				settings, err := cleanupSettings(data.config.Cleanup)
				if err != nil {
					return err
				}
				active := activity.Measure(cmd.Context(), row.Entry.Worktree.Path)
				if active.Error != "" || active.LastActive.IsZero() || active.LastActive.After(time.Now().Add(-settings.grace)) {
					return fmt.Errorf("worktree is no longer idle past the grace period")
				}
			}
			return cmd.Context().Err()
		})
		if err := p.removal.Remove(); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", p.row.Entry.Selector(), err))
			continue
		}
		removed = append(removed, p.row)
	}
	pending, err := finishRemoval(cmd, o.wait, false)
	if err != nil {
		failures = append(failures, err)
	}
	if err := a.writeCleanupResult(cmd, removed, pending, false); err != nil {
		return err
	}
	return errors.Join(failures...)
}
func revalidateCleanupTip(row cleanup.Row) error {
	worktrees, err := row.Entry.Repository.Git.Worktrees()
	if err != nil {
		return err
	}
	for _, w := range worktrees {
		if w.Path == row.Entry.Worktree.Path && w.Head == row.Entry.Worktree.Head && w.Branch == row.Entry.Worktree.Branch {
			return nil
		}
	}
	return fmt.Errorf("%s: branch changed since merge check", row.Entry.Selector())
}

func (a *application) writeCleanupResult(cmd *cobra.Command, rows []cleanup.Row, pending []trash.Entry, dry bool) error {
	results := make([]removeResult, 0, len(rows))
	var bytes int64
	unknown := 0
	for _, r := range rows {
		result := removeResult{Selector: r.Entry.Selector(), Path: r.Entry.Worktree.Path, Footprint: r.Footprint, LastActive: &r.Activity.LastActive, InUse: &r.InUse, Merged: &r.Merged}
		if dry {
			result.Unchecked = []string{"uncommitted_files", "nested_repositories"}
		}
		results = append(results, result)
		bytes += r.Bytes()
		if r.Footprint == nil {
			unknown++
		}
	}
	if a.jsonOutput {
		out := removeOutput{Version: 1, DryRun: dry, Removed: []removeResult{}, WouldRemove: []removeResult{}, PendingDeletion: pending, Bytes: bytes, UnknownSizes: unknown}
		if dry {
			out.WouldRemove = results
		} else {
			out.Removed = results
		}
		return writeJSON(cmd, out)
	}
	verb := "Removed"
	space := "being reclaimed asynchronously"
	if dry {
		verb = "Would remove"
		space = "estimated from cached measurements"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %d worktrees (~%s %s; %d sizes unknown; branches kept).\n", verb, len(rows), diskSize(bytes), space, unknown)
	for _, r := range rows {
		label := cleanupLabel(r, time.Now())
		if !dry {
			label = strings.Replace(label, "uncommitted unchecked; nested repositories unchecked", "safety checks passed", 1)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "  "+label)
	}
	if dry {
		fmt.Fprintln(cmd.ErrOrStderr(), "Preview only: uncommitted-file and nested-repository checks have not run; selected trees are checked before removal.")
	}
	return nil
}

// The worker re-reads enabled config, takes a whole-sweep lease, and appends its
// outcome even on failure. It never runs config pruning or interactive prompts.
func (a *application) runAutomaticSweep(cmd *cobra.Command, data *commandContext, o cleanupOptions) (resultErr error) {
	if !o.sweep || o.dryRun || !data.config.Cleanup.Enabled {
		return nil
	}
	schedule, err := cleanup.DefaultSchedule()
	if err != nil {
		return err
	}
	release, held, err := schedule.Lease()
	if err != nil || !held {
		return err
	}
	defer release()
	report := cleanup.SweepReport{Started: time.Now(), Removed: []cleanup.SweepRemoval{}}
	oldOut := cmd.OutOrStdout()
	var output bytes.Buffer
	cmd.SetOut(&output)
	defer cmd.SetOut(oldOut)
	oldJSON := a.jsonOutput
	a.jsonOutput = true
	defer func() { a.jsonOutput = oldJSON }()
	defer func() {
		report.Finished = time.Now()
		if resultErr != nil {
			report.Error = resultErr.Error()
		}
		if err := schedule.Record(report); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	resultErr = a.cleanupWithContext(cmd, data, o)
	var result removeOutput
	if json.Unmarshal(output.Bytes(), &result) == nil {
		for _, r := range result.Removed {
			item := cleanup.SweepRemoval{Selector: r.Selector, Path: r.Path}
			if r.Footprint != nil {
				b := r.Footprint.Bytes
				item.Bytes = &b
			}
			if r.Merged != nil {
				item.Reason = r.Merged.Reason
			}
			report.Removed = append(report.Removed, item)
		}
	}
	return resultErr
}
