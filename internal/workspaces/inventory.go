package workspaces

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"
)

type ManagedEntry struct {
	Workspace    state.Workspace
	Running      bool
	ExistsOnDisk bool
}

type UnmanagedSession struct {
	SessionName string
}

type OrphanWorktree struct {
	RepoName string
	RepoPath string
	Path     string
	Branch   string
}

type RemoveTargetKind string

const (
	RemoveManagedWorkspace RemoveTargetKind = "managed_workspace"
	RemoveUnmanagedSession RemoveTargetKind = "unmanaged_session"
)

type RemoveTarget struct {
	Kind        RemoveTargetKind
	Workspace   state.Workspace
	SessionName string
	Running     bool
}

func (t RemoveTarget) Label() string {
	if t.Kind == RemoveManagedWorkspace {
		return t.Workspace.Name
	}
	return t.SessionName
}

type CleanupTargetKind string

const (
	CleanupManagedWorkspace CleanupTargetKind = "managed_workspace"
	CleanupOrphanWorktree   CleanupTargetKind = "orphan_worktree"
)

type CleanupTarget struct {
	Kind         CleanupTargetKind
	Workspace    state.Workspace
	RepoPath     string
	WorktreePath string
	Label        string
	Detail       string
}

type Inventory struct {
	Managed   []ManagedEntry
	Unmanaged []UnmanagedSession
	Orphans   []OrphanWorktree

	managedBySession map[string]int
	managedByName    map[string]int
	unmanagedSet     map[string]bool
}

var listSessions = tmux.ListSessions
var listWorktrees = git.ListWorktrees

func Build(st *state.State, cfg *config.Config) (*Inventory, error) {
	liveSessions, err := listSessions()
	if err != nil {
		return nil, err
	}

	liveSet := make(map[string]bool, len(liveSessions))
	for _, sessionName := range liveSessions {
		liveSet[sessionName] = true
	}

	inv := &Inventory{
		Managed:          make([]ManagedEntry, 0, len(st.Workspaces)),
		managedBySession: make(map[string]int, len(st.Workspaces)),
		managedByName:    make(map[string]int, len(st.Workspaces)),
		unmanagedSet:     make(map[string]bool),
	}

	for _, ws := range st.Workspaces {
		entry := ManagedEntry{
			Workspace:    ws,
			Running:      liveSet[ws.SessionName],
			ExistsOnDisk: workspaceExists(ws),
		}
		idx := len(inv.Managed)
		inv.Managed = append(inv.Managed, entry)
		inv.managedBySession[ws.SessionName] = idx
		if _, ok := inv.managedByName[ws.Name]; !ok {
			inv.managedByName[ws.Name] = idx
		}
		delete(liveSet, ws.SessionName)
	}

	for sessionName := range liveSet {
		inv.Unmanaged = append(inv.Unmanaged, UnmanagedSession{SessionName: sessionName})
		inv.unmanagedSet[sessionName] = true
	}
	sort.Slice(inv.Unmanaged, func(i, j int) bool {
		return inv.Unmanaged[i].SessionName < inv.Unmanaged[j].SessionName
	})

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
	candidates := make([]RemoveTarget, 0, len(inv.Managed)+len(inv.Unmanaged))
	for _, entry := range inv.ManagedByLastUsed() {
		candidates = append(candidates, RemoveTarget{
			Kind:        RemoveManagedWorkspace,
			Workspace:   entry.Workspace,
			SessionName: entry.Workspace.SessionName,
			Running:     entry.Running,
		})
	}
	for _, session := range inv.Unmanaged {
		candidates = append(candidates, RemoveTarget{
			Kind:        RemoveUnmanagedSession,
			SessionName: session.SessionName,
			Running:     true,
		})
	}
	return candidates
}

func (inv *Inventory) ResolveRemoveTargets(refs []string) ([]RemoveTarget, error) {
	targets := make([]RemoveTarget, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if entry, ok := inv.FindManaged(ref); ok {
			sessionName := entry.Workspace.SessionName
			if seen[sessionName] {
				continue
			}
			targets = append(targets, RemoveTarget{
				Kind:        RemoveManagedWorkspace,
				Workspace:   entry.Workspace,
				SessionName: sessionName,
				Running:     entry.Running,
			})
			seen[sessionName] = true
			continue
		}
		if inv.unmanagedSet[ref] {
			if seen[ref] {
				continue
			}
			targets = append(targets, RemoveTarget{
				Kind:        RemoveUnmanagedSession,
				SessionName: ref,
				Running:     true,
			})
			seen[ref] = true
			continue
		}
		return nil, fmt.Errorf("session %q not found", ref)
	}
	return targets, nil
}

func RemoveManagedEntries(st *state.State, targets []RemoveTarget) {
	if len(targets) == 0 {
		return
	}

	removeSet := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target.Kind == RemoveManagedWorkspace {
			removeSet[target.SessionName] = true
		}
	}
	if len(removeSet) == 0 {
		return
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
	targets := make([]CleanupTarget, 0, len(inv.Managed)+len(inv.Orphans))
	for _, entry := range inv.Managed {
		if entry.Running {
			continue
		}
		target := CleanupTarget{
			Kind:      CleanupManagedWorkspace,
			Workspace: entry.Workspace,
			Label:     entry.Workspace.Name,
		}
		if entry.Workspace.Type == "worktree" {
			target.RepoPath = entry.Workspace.RepoPath
			target.WorktreePath = entry.Workspace.WorktreePath
		}
		switch {
		case entry.Workspace.LastUsedAt != "":
			target.Detail = state.RelativeTime(entry.Workspace.LastUsedAt) + " ago"
		case entry.Workspace.CreatedAt != "":
			target.Detail = state.RelativeTime(entry.Workspace.CreatedAt) + " ago"
		default:
			target.Detail = "stopped"
		}
		targets = append(targets, target)
	}

	for _, orphan := range inv.Orphans {
		targets = append(targets, CleanupTarget{
			Kind:         CleanupOrphanWorktree,
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
			trackedPaths[ws.WorktreePath] = true
		}
	}

	groveWorktreePrefix := string(filepath.Separator) + ".grove" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)
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
			if wt.Bare || trackedPaths[wt.Path] {
				continue
			}
			if !strings.Contains(wt.Path, groveWorktreePrefix) {
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

func workspaceExists(ws state.Workspace) bool {
	path := workspacePath(ws)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func workspacePath(ws state.Workspace) string {
	if ws.Type == "worktree" && ws.WorktreePath != "" {
		return ws.WorktreePath
	}
	return ws.Path
}
