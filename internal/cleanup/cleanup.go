// Package cleanup joins disposable measurements to live Git inventory. Preview
// facts never authorize deletion: selected trees must pass fresh removal checks.
package cleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"grove/internal/activity"
	"grove/internal/catalog"
	"grove/internal/footprint"
	"grove/internal/inuse"
	"grove/internal/inventory"
	"grove/internal/merged"
)

// Row keeps uncertainty explicit; absent sizes/status never mean empty or clean.
// Entry pins Git's inventory identity while usage is a single live cwd snapshot.
type Row struct {
	Entry     *inventory.Entry `json:"-"`
	Footprint *footprint.Entry `json:"footprint,omitempty"`
	Stale     bool             `json:"stale"`
	Activity  activity.Result  `json:"activity"`
	InUse     inuse.Result     `json:"in_use"`
	Merged    merged.Result    `json:"merged"`
	CreatedAt time.Time        `json:"created_at"`
}

func (r Row) Bytes() int64 {
	if r.Footprint != nil {
		return r.Footprint.Bytes
	}
	return 0
}
func (r Row) Idle(now time.Time, threshold time.Duration) bool {
	return r.Activity.Error == "" && !r.Activity.LastActive.IsZero() && !r.Activity.LastActive.After(now.Add(-threshold))
}

// Preselected never includes protected, live, or unknown-usage trees, even when
// merge evidence is positive. The picker may still show them for deliberate use.
func (r Row) Preselected(now time.Time, idle time.Duration, current string, inv *inventory.Inventory) bool {
	return Protected(r.Entry, current, inv) == "" && r.InUse.State == inuse.Free && (r.Idle(now, idle) || r.Merged.Merged)
}
func Protected(e *inventory.Entry, current string, inv *inventory.Inventory) string {
	switch {
	case e.Worktree.Main:
		return "main worktree"
	case e.Worktree.Locked:
		return "locked worktree"
	case e.Worktree.Prunable:
		return "missing worktree"
	case e.Worktree.Path == current:
		return "current worktree"
	case e.Worktree.Branch == "":
		return "detached worktree"
	case len(inv.Descendants(e.Worktree.Path)) > 0:
		return "contains registered worktree"
	}
	return ""
}

// Selection combines repository scope (handled before collection) with ANDed
// age/activity/merge predicates. Unknown process visibility is an error in bulk
// mode, unless explicitly overridden; known live processes are always skipped.
type Selection struct {
	OlderThan, InactiveFor time.Duration
	Merged, AllowUnknown   bool
	Current                string
}

func Select(rows []Row, inv *inventory.Inventory, now time.Time, s Selection) ([]Row, error) {
	selected := []Row{}
	for _, r := range rows {
		if Protected(r.Entry, s.Current, inv) != "" {
			continue
		}
		if s.OlderThan > 0 && (r.CreatedAt.IsZero() || r.CreatedAt.After(now.Add(-s.OlderThan))) {
			continue
		}
		if s.InactiveFor > 0 && !r.Idle(now, s.InactiveFor) {
			continue
		}
		if s.Merged && !r.Merged.Merged {
			continue
		}
		if r.InUse.State == inuse.InUse {
			continue
		}
		if r.InUse.State != inuse.Free && !s.AllowUnknown {
			return nil, fmt.Errorf("in-use check unavailable for %s: %s; use --allow-unknown-in-use to proceed", r.Entry.Selector(), r.InUse.Error)
		}
		selected = append(selected, r)
	}
	return selected, nil
}

// Collect reads cached sizes only. Activity visits Git's non-ignored file list,
// never ignored build output; merge queries are shared by repository. Dirty and
// nested-repository walks belong exclusively to the post-selection stage.
func Collect(ctx context.Context, inv *inventory.Inventory, entries []*inventory.Entry, forge bool, lookup func(string) inuse.Result) []Row {
	rows := make([]Row, len(entries))
	cache, _ := footprint.Default()
	roots := []string{}
	for _, e := range inv.Entries {
		if !e.Worktree.Prunable {
			roots = append(roots, e.Worktree.Path)
		}
	}
	if lookup == nil {
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		snapshot := inuse.Take(bounded)
		cancel()
		lookup = snapshot.Lookup
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				e := entries[i]
				r := Row{Entry: e, InUse: lookup(e.Worktree.Path)}
				if info, err := os.Stat(filepath.Join(e.Worktree.Path, ".git")); err == nil {
					r.CreatedAt = info.ModTime()
				}
				if !e.Worktree.Prunable {
					bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
					r.Activity = activity.Measure(bounded, e.Worktree.Path)
					cancel()
					if cache != nil {
						m, invalidated, ok := cache.LoadForDisplay(footprint.Request{Path: e.Worktree.Path, Exclude: footprint.Exclusions(e.Worktree.Path, roots)})
						if ok {
							r.Footprint = &m
							r.Stale = invalidated || m.Stale(time.Now(), footprint.DefaultMaxAge)
						}
					}
				} else {
					r.Activity.Error = "worktree unavailable"
				}
				rows[i] = r
			}
		}()
	}
	for i := range entries {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	byRepo := map[*catalog.Repository][]int{}
	for i, r := range rows {
		if !r.Entry.Worktree.Main && !r.Entry.Worktree.Prunable && r.Entry.Worktree.Branch != "" {
			byRepo[r.Entry.Repository] = append(byRepo[r.Entry.Repository], i)
		}
	}
	repos := make(chan *catalog.Repository)
	cacheDir, _ := merged.DefaultCacheDir()
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for repo := range repos {
				branches := []merged.Branch{}
				for _, i := range byRepo[repo] {
					r := rows[i]
					branches = append(branches, merged.Branch{Name: r.Entry.Worktree.Branch, Tip: r.Entry.Worktree.Head, CreatedAt: r.CreatedAt})
				}
				results := merged.Check(ctx, repo.Git.MainPath, repo.DefaultBranch, branches, merged.Options{Forge: forge, CacheDir: cacheDir})
				for _, i := range byRepo[repo] {
					rows[i].Merged = results[rows[i].Entry.Worktree.Branch]
				}
			}
		}()
	}
	for repo := range byRepo {
		repos <- repo
	}
	close(repos)
	workers.Wait()
	return rows
}
func SortByActivity(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Activity.LastActive.Before(rows[j].Activity.LastActive) })
}
func Processes(result inuse.Result) string {
	names := []string{}
	for _, p := range result.Processes {
		names = append(names, fmt.Sprintf("%s (pid %d)", p.Command, p.PID))
	}
	return strings.Join(names, ", ")
}

// Duration accepts Go durations plus whole days/weeks, shared by flags/config.
func Duration(value string) (time.Duration, error) {
	if len(value) > 1 {
		unit := time.Duration(0)
		switch value[len(value)-1] {
		case 'd':
			unit = 24 * time.Hour
		case 'w':
			unit = 7 * 24 * time.Hour
		}
		if unit > 0 {
			n, err := strconv.ParseUint(value[:len(value)-1], 10, 64)
			if err == nil && n > 0 && n <= uint64((1<<63-1)/unit) {
				return time.Duration(n) * unit, nil
			}
			return 0, fmt.Errorf("invalid duration %q; use a positive duration such as 12h or 3d", value)
		}
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q; use a positive duration such as 12h or 3d", value)
	}
	return d, nil
}
