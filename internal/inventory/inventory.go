package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"grove/internal/catalog"
	gitx "grove/internal/git"
)

type Failure struct {
	Repository string
	Err        error
}

func (f Failure) Error() string {
	return fmt.Sprintf("repo %s: %v", f.Repository, f.Err)
}

type Entry struct {
	Repository *catalog.Repository
	Worktree   gitx.WorktreeInfo
}

func (e *Entry) Selector() string {
	if e.Worktree.Main {
		return e.Repository.Name + ":"
	}
	if e.Worktree.Branch != "" {
		return e.Repository.Name + ":" + e.Worktree.Branch
	}
	return e.Repository.Name + ":@" + shortHead(e.Worktree.Head)
}

type Inventory struct {
	Catalog *catalog.Catalog
	Entries []*Entry
	byRepo  map[*catalog.Repository][]*Entry
}

func Build(cat *catalog.Catalog) (*Inventory, []Failure) {
	inventory := &Inventory{Catalog: cat, byRepo: make(map[*catalog.Repository][]*Entry)}
	var failures []Failure
	for _, repository := range cat.Repositories {
		worktrees, err := repository.Git.Worktrees()
		if err != nil {
			failures = append(failures, Failure{Repository: repository.Name, Err: err})
			continue
		}
		for _, worktree := range worktrees {
			entry := &Entry{Repository: repository, Worktree: worktree}
			inventory.Entries = append(inventory.Entries, entry)
			inventory.byRepo[repository] = append(inventory.byRepo[repository], entry)
		}
	}
	return inventory, failures
}

func (i *Inventory) Resolve(selector, baseDir string) (*Entry, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("worktree selector is required")
	}
	if isPathSelector(selector) {
		return i.resolvePath(selector, baseDir)
	}
	if strings.Contains(selector, ":") {
		parts := strings.SplitN(selector, ":", 2)
		if parts[0] == "" {
			return nil, fmt.Errorf("repository name is required before ':'")
		}
		repository, _, err := i.Catalog.FindRepository(parts[0])
		if err != nil {
			return nil, err
		}
		return i.resolveInRepository(repository, parts[1])
	}
	if i.Catalog.Current == nil {
		return nil, fmt.Errorf("not inside a configured Git repository; use repo:branch")
	}
	return i.resolveInRepository(i.Catalog.Current, selector)
}

func (i *Inventory) Descendants(path string) []*Entry {
	var descendants []*Entry
	for _, entry := range i.Entries {
		if entry.Worktree.Prunable || !pathStrictlyContains(path, entry.Worktree.Path) {
			continue
		}
		descendants = append(descendants, entry)
	}
	return descendants
}

func (i *Inventory) resolveInRepository(repository *catalog.Repository, branch string) (*Entry, error) {
	var matches []*Entry
	for _, entry := range i.byRepo[repository] {
		matchesTarget := branch == "" && entry.Worktree.Main || branch != "" && entry.Worktree.Branch == branch
		if !matchesTarget {
			continue
		}
		matches = append(matches, entry)
	}
	if len(matches) > 1 {
		paths := make([]string, 0, len(matches))
		for _, match := range matches {
			paths = append(paths, match.Worktree.Path)
		}
		return nil, fmt.Errorf("branch %q is checked out in multiple worktrees: %s; use an absolute path", branch, strings.Join(paths, ", "))
	}
	if len(matches) == 1 {
		if matches[0].Worktree.Prunable {
			return nil, fmt.Errorf("worktree %q is prunable and unavailable", matches[0].Selector())
		}
		return matches[0], nil
	}
	if branch == "" {
		return nil, fmt.Errorf("main worktree for repository %q not found", repository.Name)
	}
	return nil, fmt.Errorf("worktree for branch %q not found in repository %q", branch, repository.Name)
}

func pathStrictlyContains(parent, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (i *Inventory) resolvePath(selector, baseDir string) (*Entry, error) {
	path, err := expandPath(selector, baseDir)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolving worktree path %s: %w", path, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, err
	}
	var best *Entry
	bestLength := -1
	for _, entry := range i.Entries {
		if entry.Worktree.Prunable {
			continue
		}
		rel, err := filepath.Rel(entry.Worktree.Path, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(entry.Worktree.Path) > bestLength {
			best = entry
			bestLength = len(entry.Worktree.Path)
		}
	}
	if best == nil {
		return nil, fmt.Errorf("path is not inside a known worktree: %s", resolved)
	}
	return best, nil
}

func isPathSelector(selector string) bool {
	return selector == "." || selector == ".." || filepath.IsAbs(selector) || strings.HasPrefix(selector, "./") || strings.HasPrefix(selector, "../") || selector == "~" || strings.HasPrefix(selector, "~/")
}

func expandPath(path, baseDir string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(filepath.Join(baseDir, path))
}

func shortHead(head string) string {
	if len(head) > 8 {
		return head[:8]
	}
	return head
}
