package workspaces

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
)

type ManagedEntry struct {
	Workspace state.Workspace
}

type OrphanWorktree struct {
	RepoName string
	RepoPath string
	Path     string
	Branch   string
}

type RemoveTarget struct {
	Workspace   state.Workspace
	SessionName string
}

func (t RemoveTarget) Label() string {
	return t.Workspace.Name
}

// CleanupTarget is an orphan worktree on disk that grove no longer tracks.
type CleanupTarget struct {
	RepoPath     string
	WorktreePath string
	Label        string
	Detail       string
}

type Inventory struct {
	Managed []ManagedEntry
	Orphans []OrphanWorktree

	managedBySession map[string]int
	managedByName    map[string]int
}

var listWorktrees = git.ListWorktrees

func Build(st *state.State, cfg *config.Config) (*Inventory, error) {
	inv := &Inventory{
		Managed:          make([]ManagedEntry, 0, len(st.Workspaces)),
		managedBySession: make(map[string]int, len(st.Workspaces)),
		managedByName:    make(map[string]int, len(st.Workspaces)),
	}

	for _, ws := range st.Workspaces {
		idx := len(inv.Managed)
		inv.Managed = append(inv.Managed, ManagedEntry{Workspace: ws})
		inv.managedBySession[ws.SessionName] = idx
		if _, ok := inv.managedByName[ws.Name]; !ok {
			inv.managedByName[ws.Name] = idx
		}
	}

	orphans, err := buildOrphans(st, cfg)
	if err != nil {
		return nil, err
	}
	inv.Orphans = orphans
	return inv, nil
}

func (inv *Inventory) FindManaged(ref string) (ManagedEntry, bool) {
	if idx, ok := inv.managedBySession[ref]; ok {
		return inv.Managed[idx], true
	}
	if idx, ok := inv.managedByName[ref]; ok {
		return inv.Managed[idx], true
	}
	if !strings.HasPrefix(ref, "g/") {
		if idx, ok := inv.managedBySession["g/"+ref]; ok {
			return inv.Managed[idx], true
		}
	}
	return ManagedEntry{}, false
}

func (inv *Inventory) FindManagedBySession(sessionName string) (ManagedEntry, bool) {
	idx, ok := inv.managedBySession[sessionName]
	if !ok {
		return ManagedEntry{}, false
	}
	return inv.Managed[idx], true
}

func (inv *Inventory) ManagedByLastUsed() []ManagedEntry {
	sorted := make([]ManagedEntry, len(inv.Managed))
	copy(sorted, inv.Managed)
	sort.Slice(sorted, func(i, j int) bool {
		left := sorted[i].Workspace.LastUsedAt
		right := sorted[j].Workspace.LastUsedAt
		if left == "" && right == "" {
			return sorted[i].Workspace.Name < sorted[j].Workspace.Name
		}
		if left == "" {
			return false
		}
		if right == "" {
			return true
		}
		if left == right {
			return sorted[i].Workspace.Name < sorted[j].Workspace.Name
		}
		return left > right
	})
	return sorted
}

func (inv *Inventory) RemoveCandidates() []RemoveTarget {
	candidates := make([]RemoveTarget, 0, len(inv.Managed))
	for _, entry := range inv.ManagedByLastUsed() {
		candidates = append(candidates, RemoveTarget{
			Workspace:   entry.Workspace,
			SessionName: entry.Workspace.SessionName,
		})
	}
	return candidates
}

func (inv *Inventory) ResolveRemoveTargets(refs []string) ([]RemoveTarget, error) {
	targets := make([]RemoveTarget, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		entry, ok := inv.FindManaged(ref)
		if !ok {
			return nil, fmt.Errorf("workspace %q not found", ref)
		}
		sessionName := entry.Workspace.SessionName
		if seen[sessionName] {
			continue
		}
		targets = append(targets, RemoveTarget{
			Workspace:   entry.Workspace,
			SessionName: sessionName,
		})
		seen[sessionName] = true
	}
	return targets, nil
}

func RemoveManagedEntries(st *state.State, targets []RemoveTarget) {
	if len(targets) == 0 {
		return
	}
	removeSet := make(map[string]bool, len(targets))
	for _, target := range targets {
		removeSet[target.SessionName] = true
	}
	filtered := st.Workspaces[:0]
	for _, ws := range st.Workspaces {
		if !removeSet[ws.SessionName] {
			filtered = append(filtered, ws)
		}
	}
	st.Workspaces = filtered
}

func (inv *Inventory) CleanupTargets() []CleanupTarget {
	targets := make([]CleanupTarget, 0, len(inv.Orphans))
	for _, orphan := range inv.Orphans {
		targets = append(targets, CleanupTarget{
			RepoPath:     orphan.RepoPath,
			WorktreePath: orphan.Path,
			Label:        fmt.Sprintf("%s/%s", orphan.RepoName, orphan.Branch),
			Detail:       "orphan",
		})
	}
	return targets
}

func buildOrphans(st *state.State, cfg *config.Config) ([]OrphanWorktree, error) {
	if cfg == nil {
		return nil, nil
	}

	trackedPaths := make(map[string]bool, len(st.Workspaces))
	for _, ws := range st.Workspaces {
		if ws.WorktreePath != "" {
			trackedPaths[cleanPath(ws.WorktreePath)] = true
		}
	}

	var orphans []OrphanWorktree
	for _, repo := range cfg.Repos {
		if repo.Type != "" && repo.Type != "worktree" {
			continue
		}
		worktrees, err := listWorktrees(repo.Path)
		if err != nil {
			continue
		}
		for _, wt := range worktrees {
			if wt.Bare || trackedPaths[cleanPath(wt.Path)] {
				continue
			}
			if !repoOwnsWorktreePath(cfg, repo, wt.Path) {
				continue
			}
			branch := wt.Branch
			if branch == "" {
				branch = filepath.Base(wt.Path)
			}
			orphans = append(orphans, OrphanWorktree{
				RepoName: repo.Name,
				RepoPath: repo.Path,
				Path:     wt.Path,
				Branch:   branch,
			})
		}
	}
	sort.Slice(orphans, func(i, j int) bool {
		if orphans[i].RepoName == orphans[j].RepoName {
			return orphans[i].Path < orphans[j].Path
		}
		return orphans[i].RepoName < orphans[j].RepoName
	})
	return orphans, nil
}

func repoOwnsWorktreePath(cfg *config.Config, repo config.RepoConfig, path string) bool {
	legacyRoot := filepath.Join(repo.Path, ".grove", "worktrees")
	if pathWithinClean(legacyRoot, path) {
		return true
	}
	root := ""
	if cfg != nil {
		root = cfg.EffectiveWorktreeRoot(&repo)
	}
	if root == "" {
		return false
	}
	return pathWithinClean(root, path)
}

func pathWithinClean(root, path string) bool {
	rel, err := filepath.Rel(cleanPath(root), cleanPath(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func cleanPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}
