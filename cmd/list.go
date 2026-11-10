package cmd

import (
	"context"
	"fmt"
	"grove/internal/activity"
	"grove/internal/inuse"
	"grove/internal/merged"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"grove/internal/catalog"
	"grove/internal/inventory"

	"github.com/spf13/cobra"
)

type listDocument struct {
	Version      int              `json:"version"`
	Repositories []listRepository `json:"repositories"`
}

type listRepository struct {
	Name          string         `json:"name"`
	Aliases       []string       `json:"aliases"`
	Path          string         `json:"path"`
	DefaultBranch string         `json:"default_branch"`
	Worktrees     []listWorktree `json:"worktrees"`
}

type listWorktree struct {
	Branch        string         `json:"branch,omitempty"`
	Head          string         `json:"head"`
	Path          string         `json:"path"`
	Main          bool           `json:"main"`
	Detached      bool           `json:"detached"`
	Locked        bool           `json:"locked"`
	LockReason    string         `json:"lock_reason,omitempty"`
	Prunable      bool           `json:"prunable"`
	Dirty         *bool          `json:"dirty,omitempty"`
	Ahead         *int           `json:"ahead,omitempty"`
	Behind        *int           `json:"behind,omitempty"`
	StatusError   string         `json:"status_error,omitempty"`
	LastActive    *time.Time     `json:"last_active,omitempty"`
	ActivityError string         `json:"activity_error,omitempty"`
	InUse         *inuse.Result  `json:"in_use,omitempty"`
	Merged        *merged.Result `json:"merged,omitempty"`
}

func (a *application) listCommand() *cobra.Command {
	var status, forge bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List repositories and worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runList(cmd, status || forge, forge)
		},
	}
	command.Flags().BoolVar(&status, "status", false, "Check dirty, ahead/behind, activity, processes, and local merge status")
	command.Flags().BoolVar(&forge, "forge", false, "Include optional forge merge checks (implies --status)")
	return command
}

func (a *application) runList(cmd *cobra.Command, includeStatus, forge bool) error {
	loaded, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	document := buildListDocument(cmd.Context(), loaded.catalog, loaded.inventory, includeStatus, forge)
	if a.jsonOutput {
		return writeJSON(cmd, document)
	}
	if len(document.Repositories) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No repositories. Run 'grove new' inside a Git repository.")
		return nil
	}
	style := a.style(cmd.OutOrStdout())
	now := time.Now()
	shownRepositories := 0
	for _, repository := range document.Repositories {
		worktrees := make([]listWorktree, 0, len(repository.Worktrees))
		for _, worktree := range repository.Worktrees {
			if !worktree.Main {
				worktrees = append(worktrees, worktree)
			}
		}
		if len(worktrees) == 0 {
			continue
		}
		sortWorktreesNewestFirst(worktrees)
		if shownRepositories > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		shownRepositories++
		aliases := aliasesSuffix(repository.Name, repository.Aliases)
		fmt.Fprintf(cmd.OutOrStdout(), "%s%s  %s\n", style.heading(repository.Name), style.muted(aliases), style.muted(repository.Path))
		for worktreeIndex, worktree := range worktrees {
			connector := "├──"
			if worktreeIndex == len(worktrees)-1 {
				connector = "└──"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s%s%s\n", style.muted(connector), styledWorktreeLabel(style, worktree), styledStatusSuffix(style, worktree)+signalSuffix(worktree, now), style.muted(createdSuffix(worktree, now)))
		}
	}
	if shownRepositories == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No linked worktrees.")
	}
	return nil
}

func sortWorktreesNewestFirst(worktrees []listWorktree) {
	// Git has no worktree creation field. Grove uses the linked worktree's .git
	// mtime for age labels and cleanup, so the human list must use that same proxy.
	// Entries without readable metadata stay in Git's order after dated entries.
	createdAt := make(map[string]time.Time, len(worktrees))
	for _, worktree := range worktrees {
		if timestamp, ok := worktreeCreatedAt(worktree.Path); ok {
			createdAt[worktree.Path] = timestamp
		}
	}
	sort.SliceStable(worktrees, func(left, right int) bool {
		leftCreatedAt, leftKnown := createdAt[worktrees[left].Path]
		rightCreatedAt, rightKnown := createdAt[worktrees[right].Path]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if !leftKnown {
			return false
		}
		return leftCreatedAt.After(rightCreatedAt)
	})
}

func styledWorktreeLabel(style outputStyle, worktree listWorktree) string {
	label := worktree.Branch
	if label == "" {
		label = "@" + shortSHA(worktree.Head)
	}
	label = style.branch(label)
	if worktree.Main {
		label += style.muted(" [main]")
	}
	if worktree.Locked {
		label += style.attention(" [locked]")
	}
	if worktree.Prunable {
		label += style.danger(" [missing]")
	}
	return label
}

func styledStatusSuffix(style outputStyle, worktree listWorktree) string {
	if worktree.Dirty == nil && worktree.StatusError == "" {
		return ""
	}
	var labels []string
	if worktree.Dirty != nil && *worktree.Dirty {
		labels = append(labels, style.attention("dirty"))
	}
	if worktree.Ahead != nil && *worktree.Ahead > 0 {
		labels = append(labels, style.info(fmt.Sprintf("↑%d", *worktree.Ahead)))
	}
	if worktree.Behind != nil && *worktree.Behind > 0 {
		labels = append(labels, style.info(fmt.Sprintf("↓%d", *worktree.Behind)))
	}
	if worktree.StatusError != "" {
		labels = append(labels, style.danger("status unavailable"))
	}
	if len(labels) == 0 {
		labels = append(labels, style.muted("clean"))
	}
	return "  " + style.muted("[") + strings.Join(labels, style.muted(", ")) + style.muted("]")
}

func createdSuffix(worktree listWorktree, now time.Time) string {
	if worktree.Main || worktree.Prunable {
		return ""
	}
	label := worktreeCreationLabel(worktree.Path, now)
	if label == "" {
		return ""
	}
	return "  " + label
}

func worktreeCreationLabel(path string, now time.Time) string {
	createdAt, ok := worktreeCreatedAt(path)
	if !ok {
		return ""
	}
	age := now.Sub(createdAt)
	if age < time.Minute {
		return "created just now"
	}
	return "created " + relativeAge(age) + " ago"
}

func worktreeCreatedAt(path string) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func relativeAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(age/(24*time.Hour)))
	}
}

func buildListDocument(ctx context.Context, cat *catalog.Catalog, inv *inventory.Inventory, includeStatus, forge bool) listDocument {
	document := listDocument{Version: 1, Repositories: make([]listRepository, 0, len(cat.Repositories))}
	byRepository := make(map[*catalog.Repository][]*inventory.Entry)
	for _, entry := range inv.Entries {
		byRepository[entry.Repository] = append(byRepository[entry.Repository], entry)
	}
	for _, repository := range cat.Repositories {
		item := listRepository{
			Name:          repository.Name,
			Aliases:       repository.Aliases(),
			Path:          repository.Git.MainPath,
			DefaultBranch: repository.DefaultBranch,
			Worktrees:     []listWorktree{},
		}
		for _, entry := range byRepository[repository] {
			worktree := listWorktree{
				Branch:     entry.Worktree.Branch,
				Head:       entry.Worktree.Head,
				Path:       entry.Worktree.Path,
				Main:       entry.Worktree.Main,
				Detached:   entry.Worktree.Detached,
				Locked:     entry.Worktree.Locked,
				LockReason: entry.Worktree.LockReason,
				Prunable:   entry.Worktree.Prunable,
			}
			item.Worktrees = append(item.Worktrees, worktree)
		}
		document.Repositories = append(document.Repositories, item)
	}
	if includeStatus {
		populateListStatus(ctx, &document, cat, forge)
	}
	return document
}

func aliasesSuffix(name string, aliases []string) string {
	others := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if alias != name {
			others = append(others, alias)
		}
	}
	if len(others) == 0 {
		return ""
	}
	return " (" + strings.Join(others, ", ") + ")"
}

func shortSHA(head string) string {
	if len(head) > 8 {
		return head[:8]
	}
	return head
}

// The ordinary list only joins existing inventory fields. All subprocesses and
// source scans live behind --status; one immutable cwd snapshot serves every tree.
// Workers own disjoint slice elements, preserving Git/catalog output order.
func populateListStatus(ctx context.Context, document *listDocument, cat *catalog.Catalog, forge bool) {
	snapshotContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	snapshot := inuse.Take(snapshotContext)
	cancel()
	type job struct {
		worktree   *listWorktree
		repository *catalog.Repository
	}
	jobs := make(chan job)
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for task := range jobs {
				worktree, repository := task.worktree, task.repository
				usage := snapshot.Lookup(worktree.Path)
				worktree.InUse = &usage
				if worktree.Prunable {
					worktree.ActivityError = "worktree unavailable"
					continue
				}
				activityContext, cancel := context.WithTimeout(ctx, 10*time.Second)
				active := activity.Measure(activityContext, worktree.Path)
				cancel()
				if !active.LastActive.IsZero() {
					worktree.LastActive = &active.LastActive
				}
				worktree.ActivityError = active.Error
				dirty, err := repository.Git.Dirty(worktree.Path)
				if err != nil {
					worktree.StatusError = err.Error()
					continue
				}
				worktree.Dirty = &dirty
				ahead, behind, err := repository.Git.AheadBehind(worktree.Path, repository.DefaultBranch)
				if err != nil {
					worktree.StatusError = err.Error()
					continue
				}
				worktree.Ahead, worktree.Behind = &ahead, &behind
			}
		}()
	}
	for i, repository := range cat.Repositories {
		for j := range document.Repositories[i].Worktrees {
			jobs <- job{&document.Repositories[i].Worktrees[j], repository}
		}
	}
	close(jobs)
	workers.Wait()
	// A repository owns a single forge request and isolated trial-merge object
	// store. Repository concurrency is bounded separately from per-tree scans.
	repositories := make(chan int)
	cacheDir, cacheErr := merged.DefaultCacheDir()
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range repositories {
				repository := cat.Repositories[i]
				var branches []merged.Branch
				for _, worktree := range document.Repositories[i].Worktrees {
					if !worktree.Main && !worktree.Detached && !worktree.Prunable {
						branches = append(branches, merged.Branch{Name: worktree.Branch, Tip: worktree.Head})
					}
				}
				results := map[string]merged.Result{}
				if len(branches) > 0 {
					results = merged.Check(ctx, repository.Git.MainPath, repository.DefaultBranch, branches, merged.Options{Forge: forge, CacheDir: cacheDir})
				}
				for j := range document.Repositories[i].Worktrees {
					worktree := &document.Repositories[i].Worktrees[j]
					result, ok := results[worktree.Branch]
					if !ok {
						result = merged.Result{Tier: "not_applicable", Reason: "main, detached, or unavailable worktree"}
					}
					if forge && cacheErr != nil {
						result.Skipped = append(result.Skipped, "cache: "+cacheErr.Error())
					}
					worktree.Merged = &result
				}
			}
		}()
	}
	for i := range cat.Repositories {
		repositories <- i
	}
	close(repositories)
	workers.Wait()
}

func signalSuffix(worktree listWorktree, now time.Time) string {
	if worktree.InUse == nil {
		return ""
	}
	var labels []string
	if worktree.ActivityError != "" {
		labels = append(labels, "last active unknown")
	} else if worktree.LastActive != nil {
		labels = append(labels, "last active "+relativeAge(now.Sub(*worktree.LastActive))+" ago")
	}
	switch worktree.InUse.State {
	case inuse.InUse:
		labels = append(labels, fmt.Sprintf("in use (%d processes)", len(worktree.InUse.Processes)))
	case inuse.Free:
		labels = append(labels, "not in use")
	default:
		labels = append(labels, "in use unknown")
	}
	if result := worktree.Merged; result != nil && result.Tier != "not_applicable" {
		label := result.Reason
		if result.Error != "" {
			label = "merge status unknown"
		}
		if result.Merged {
			label = "merged: " + result.Tier
		}
		if result.PR != 0 {
			label += fmt.Sprintf(" PR #%d", result.PR)
		}
		if len(result.Skipped) > 0 {
			label += "; " + strings.Join(result.Skipped, "; ")
		}
		labels = append(labels, label)
	}
	return "  [" + strings.Join(labels, ", ") + "]"
}
