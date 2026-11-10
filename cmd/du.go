package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"grove/internal/catalog"
	"grove/internal/footprint"
	"grove/internal/inventory"

	"github.com/spf13/cobra"
)

// duDocument is independent of list's JSON contract. Totals include only linked
// worktrees; main checkouts are metadata-only unless --include-main is requested.
type duDocument struct {
	Version      int             `json:"version"`
	Repositories []*duRepository `json:"repositories"`
	Bytes        int64           `json:"bytes"`
	Worktrees    int             `json:"worktree_count"`
}
type duRepository struct {
	Name      string        `json:"name"`
	Aliases   []string      `json:"aliases"`
	Path      string        `json:"path"`
	Bytes     int64         `json:"bytes"`
	Count     int           `json:"worktree_count"`
	Worktrees []*duWorktree `json:"worktrees"`
}
type duWorktree struct {
	Branch         string           `json:"branch,omitempty"`
	Path           string           `json:"path"`
	Main           bool             `json:"main"`
	Locked         bool             `json:"locked"`
	Missing        bool             `json:"missing"`
	CreatedAt      *time.Time       `json:"created_at,omitempty"`
	Footprint      *footprint.Entry `json:"footprint,omitempty"`
	Stale          bool             `json:"stale"`
	MeasurementAge float64          `json:"measurement_age_seconds,omitempty"`
	Error          string           `json:"error,omitempty"`
}
type duOptions struct {
	refresh, background, includeMain, privateSize bool
	maxAge                                        time.Duration
	jobs                                          int
}
type duTask struct {
	worktree *duWorktree
	request  footprint.Request
}

func (a *application) duCommand() *cobra.Command {
	options := duOptions{}
	command := &cobra.Command{
		Use: "du [repository ...]", Short: "Show cached worktree disk usage, largest first",
		Long: "Show allocated disk usage grouped by repository. Cached measurements expire after 15 minutes; stale results print immediately and refresh in a detached process. Use --refresh to wait for fresh measurements. Main worktrees are excluded from totals. Private bytes, when available, estimate unshared file data, not guaranteed reclaimed volume space.",
		RunE: func(cmd *cobra.Command, args []string) error { return a.runDU(cmd, args, options) },
	}
	command.Flags().BoolVar(&options.refresh, "refresh", false, "Wait for fresh measurements")
	command.Flags().DurationVar(&options.maxAge, "max-age", footprint.DefaultMaxAge, "Maximum cache age before background refresh")
	command.Flags().IntVar(&options.jobs, "jobs", 4, "Maximum simultaneous worktree scans")
	command.Flags().BoolVar(&options.privateSize, "private-size", false, "Measure private file bytes when supported (macOS with cgo)")
	command.Flags().BoolVar(&options.includeMain, "include-main", false, "Measure main worktrees for JSON (excluded from displayed totals)")
	command.Flags().BoolVar(&options.background, "background-refresh", false, "Run a detached cache refresh")
	_ = command.Flags().MarkHidden("background-refresh")
	return command
}

func (a *application) runDU(cmd *cobra.Command, names []string, options duOptions) error {
	if options.jobs < 1 || options.jobs > 32 {
		return fmt.Errorf("--jobs must be between 1 and 32")
	}
	if options.maxAge < 0 {
		return fmt.Errorf("--max-age must not be negative")
	}
	if a.nullOutput {
		return fmt.Errorf("du does not support --null; use --json")
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cache, err := footprint.Default()
	if err != nil {
		return err
	}

	data, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	selected := map[*catalog.Repository]bool{}
	for _, name := range names {
		repo, _, err := data.catalog.FindRepository(name)
		if err != nil {
			return err
		}
		selected[repo] = true
	}
	if options.background {
		// Lease by canonical repository scope and scan policy. A global lease would
		// silently drop a second caller's refresh for an unrelated repository.
		scope := []string{}
		for _, repo := range data.catalog.Repositories {
			if len(names) == 0 || selected[repo] {
				scope = append(scope, repo.Git.CommonDir)
			}
		}
		sort.Strings(scope)
		key := fmt.Sprintf("%t:%t:%s:%s", options.privateSize, options.includeMain, options.maxAge, strings.Join(scope, "\x00"))
		release, acquired := cache.BackgroundLease(key)
		if !acquired {
			return nil
		}
		defer release()
	}
	document := duDocument{Version: 1, Repositories: []*duRepository{}}
	byRepository := map[*catalog.Repository]*duRepository{}
	for _, repo := range data.catalog.Repositories {
		if len(names) > 0 && !selected[repo] {
			continue
		}
		row := &duRepository{Name: repo.Name, Aliases: repo.Aliases(), Path: repo.Git.MainPath, Worktrees: []*duWorktree{}}
		document.Repositories = append(document.Repositories, row)
		byRepository[repo] = row
	}
	roots := make([]string, 0, len(data.inventory.Entries))
	for _, entry := range data.inventory.Entries {
		if !entry.Worktree.Prunable {
			roots = append(roots, entry.Worktree.Path)
		}
	}
	var tasks []duTask
	refreshNeeded := false
	now := time.Now()
	for _, entry := range data.inventory.Entries {
		repo := byRepository[entry.Repository]
		if repo == nil {
			continue
		}
		row := duRow(entry)
		repo.Worktrees = append(repo.Worktrees, row)
		if row.Missing || (row.Main && !options.includeMain) {
			continue
		}
		request := footprint.Request{Path: row.Path, Exclude: footprint.Exclusions(row.Path, roots), PrivateSize: options.privateSize}
		measured, ok := cache.Load(request)
		if ok {
			row.Footprint = &measured
			row.Stale = measured.Stale(now, options.maxAge)
			row.MeasurementAge = now.Sub(measured.ScannedAt).Seconds()
		}
		if options.refresh || !ok || options.background && row.Stale {
			tasks = append(tasks, duTask{row, request})
		} else if row.Stale {
			refreshNeeded = true
		}
	}
	if len(tasks) > 0 {
		// Workers own distinct output rows. Only the foreground collector writes
		// progress, keeping JSON and concurrent diagnostics off the stdout stream.
		jobs := make(chan duTask)
		completed := make(chan struct{}, len(tasks))
		var workers sync.WaitGroup
		for i := 0; i < options.jobs; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for task := range jobs {
					measured, err := cache.Measure(ctx, task.request, options.refresh, options.maxAge, 4)
					if err != nil {
						task.worktree.Error = err.Error()
					}
					if measured.Version != 0 {
						task.worktree.Footprint = &measured
						task.worktree.Stale = false
						task.worktree.MeasurementAge = time.Since(measured.ScannedAt).Seconds()
					}
					completed <- struct{}{}
				}
			}()
		}
		go func() {
			defer close(jobs)
			for _, task := range tasks {
				jobs <- task
			}
		}()
		done := 0
		started := time.Now()
		ticker := time.NewTicker(time.Second)
		fmt.Fprintf(cmd.ErrOrStderr(), "Scanning worktree footprints: 0/%d\n", len(tasks))
		for done < len(tasks) {
			select {
			case <-completed:
				done++
			case <-ticker.C:
				fmt.Fprintf(cmd.ErrOrStderr(), "Scanning worktree footprints: %d/%d (%s)\n", done, len(tasks), time.Since(started).Round(time.Second))
			}
		}
		ticker.Stop()
		workers.Wait()
		fmt.Fprintf(cmd.ErrOrStderr(), "Scanned %d worktrees in %s\n", done, time.Since(started).Round(time.Millisecond))
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	cache.Prune()
	if options.background {
		return nil
	}
	finishDUTotals(&document)
	if a.jsonOutput {
		err = writeJSON(cmd, document)
	} else {
		err = a.printDU(cmd, document)
	}
	if err != nil {
		return err
	}
	if refreshNeeded {
		args := []string{"-C", data.directory, "du", "--background-refresh", fmt.Sprintf("--max-age=%s", options.maxAge), fmt.Sprintf("--jobs=%d", options.jobs), fmt.Sprintf("--private-size=%t", options.privateSize), fmt.Sprintf("--include-main=%t", options.includeMain)}
		// Repository names after -- remain positional even when an alias starts with a dash.
		args = append(args, "--")
		args = append(args, names...)
		launch := a.dependencies.startFootprintRefresh
		if launch == nil {
			launch = startFootprintRefresh
		}
		if err := launch(args); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not start footprint refresh: %v; use grove du --refresh\n", err)
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), "Refreshing stale footprints in the background.")
		}
	}
	return nil
}

func duRow(entry *inventory.Entry) *duWorktree {
	wt := entry.Worktree
	row := &duWorktree{Branch: wt.Branch, Path: wt.Path, Main: wt.Main, Locked: wt.Locked, Missing: wt.Prunable}
	if !wt.Main {
		if created, ok := worktreeCreatedAt(wt.Path); ok {
			row.CreatedAt = &created
		}
	}
	return row
}
func finishDUTotals(document *duDocument) {
	for _, repo := range document.Repositories {
		for _, row := range repo.Worktrees {
			if !row.Main && !row.Missing {
				repo.Count++
				if row.Footprint != nil {
					repo.Bytes += row.Footprint.Bytes
				}
			}
		}
		document.Bytes += repo.Bytes
		document.Worktrees += repo.Count
		sort.SliceStable(repo.Worktrees, func(i, j int) bool { return duBytes(repo.Worktrees[i]) > duBytes(repo.Worktrees[j]) })
	}
	sort.SliceStable(document.Repositories, func(i, j int) bool {
		if document.Repositories[i].Bytes == document.Repositories[j].Bytes {
			return document.Repositories[i].Name < document.Repositories[j].Name
		}
		return document.Repositories[i].Bytes > document.Repositories[j].Bytes
	})
}
func duBytes(row *duWorktree) int64 {
	if row.Main || row.Footprint == nil {
		return -1
	}
	return row.Footprint.Bytes
}
func diskSize(bytes int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(bytes)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}
func (a *application) printDU(cmd *cobra.Command, document duDocument) error {
	style := a.style(cmd.OutOrStdout())
	now := time.Now()
	for _, repo := range document.Repositories {
		if repo.Count == 0 {
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %d worktrees\n", style.heading(repo.Name), diskSize(repo.Bytes), repo.Count)
		for _, row := range repo.Worktrees {
			if row.Main {
				continue
			}
			label := row.Branch
			if label == "" {
				label = row.Path
			}
			if row.Footprint == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s  unavailable  %s\n", label, row.Error)
				continue
			}
			m := row.Footprint
			largest := ""
			if len(m.Largest) > 0 {
				largest = fmt.Sprintf("  %s (%s)", m.Largest[0].Path, diskSize(m.Largest[0].Bytes))
			}
			measured := "measured " + relativeAge(now.Sub(m.ScannedAt)) + " ago"
			if row.Stale {
				measured += "; stale"
			}
			age := ""
			if row.CreatedAt != nil {
				age = "  created " + relativeAge(now.Sub(*row.CreatedAt)) + " ago"
			}
			detail := ""
			if m.PrivateBytes != nil {
				detail = "  private ~" + diskSize(*m.PrivateBytes)
			}
			if m.IssueCount > 0 {
				detail += fmt.Sprintf("  partial (%d read errors)", m.IssueCount)
			}
			if row.Error != "" {
				detail += "  warning: " + row.Error
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  %9s  %s%s%s  %s%s\n", diskSize(m.Bytes), style.branch(label), largest, age, measured, detail)
		}
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "\nTotal: %s in %d linked worktrees (main excluded)\n", diskSize(document.Bytes), document.Worktrees)
	return err
}

// The child owns its session and has no inherited terminal pipes. It refreshes
// disposable cache only; it cannot keep a shell substitution or caller waiting.
func startFootprintRefresh(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command := exec.Command(executable, args...)
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
