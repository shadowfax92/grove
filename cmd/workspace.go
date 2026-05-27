package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"grove/internal/config"
	"grove/internal/state"
	"grove/internal/workspaces"
)

func workspaceDir(ws *state.Workspace) string {
	if ws == nil {
		return ""
	}
	if ws.Path != "" {
		return ws.Path
	}
	if ws.Type == "worktree" && ws.WorktreePath != "" {
		return ws.WorktreePath
	}
	home, _ := os.UserHomeDir()
	return home
}

func workspaceDirWithConfig(ws *state.Workspace, cfg *config.Config) string {
	if ws == nil {
		return ""
	}
	if ws.Type == "worktree" && ws.WorktreePath != "" && cfg != nil {
		if repo := cfg.FindRepo(ws.Repo); repo != nil && repo.Workdir != "" {
			return pathWithWorkdir(ws.WorktreePath, repo.Workdir)
		}
	}
	return workspaceDir(ws)
}

func pathWithWorkdir(root, workdir string) string {
	if workdir == "" {
		return root
	}
	return filepath.Join(root, workdir)
}

func findWorkspaceRef(mgr *state.StateManager, st *state.State, ref string) *state.Workspace {
	if ws := mgr.FindWorkspace(st, ref); ws != nil {
		return ws
	}
	return mgr.FindBySession(st, ref)
}

func findWorkspaceByCwd(st *state.State, cwd string) (*state.Workspace, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolving cwd: %w", err)
	}
	matches := workspaceMatchesByCwd(st, cwd)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no workspace found for cwd %q", cwd)
	}
	tied := workspaceMatchNames(st, matches, len(matches[0].root))
	if len(tied) > 1 {
		return nil, fmt.Errorf("cwd %q matches multiple workspaces: %s", cwd, strings.Join(tied, ", "))
	}
	return &st.Workspaces[matches[0].index], nil
}

type workspaceMatch struct {
	index int
	root  string
}

func workspaceMatchesByCwd(st *state.State, cwd string) []workspaceMatch {
	matches := make([]workspaceMatch, 0, len(st.Workspaces))
	for i := range st.Workspaces {
		root, ok := workspaceRoot(&st.Workspaces[i])
		if !ok || !pathWithin(root, cwd) {
			continue
		}
		matches = append(matches, workspaceMatch{index: i, root: root})
	}
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i].root) > len(matches[j].root)
	})
	return matches
}

func workspaceRoot(ws *state.Workspace) (string, bool) {
	root := ""
	if ws != nil && ws.Type == "worktree" && ws.WorktreePath != "" {
		root = ws.WorktreePath
	} else {
		root = workspaceDir(ws)
	}
	if root == "" {
		return "", false
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	return root, true
}

func pathWithin(root, cwd string) bool {
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func workspaceMatchNames(st *state.State, matches []workspaceMatch, rootLen int) []string {
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match.root) != rootLen {
			break
		}
		names = append(names, st.Workspaces[match.index].Name)
	}
	sort.Strings(names)
	return names
}

// pickSessionFzf renders managed workspaces in an fzf picker and returns the
// selected session name. Shared by `grove cd`.
func pickSessionFzf(prompt string, entries []workspaces.ManagedEntry) (string, error) {
	if len(entries) == 0 {
		return "", fmt.Errorf("no workspaces")
	}

	var lines []string
	for _, entry := range entries {
		ws := entry.Workspace
		age := ""
		if ws.LastUsedAt != "" {
			age = state.RelativeTime(ws.LastUsedAt)
		}
		lines = append(lines, fmt.Sprintf("%s\t%-30s %s", ws.SessionName, ws.Name, age))
	}

	fzfCmd := exec.Command("fzf",
		"--prompt", prompt,
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "2",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", ErrCancelled
		}
		return "", fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	line := strings.TrimSpace(string(out))
	if idx := strings.Index(line, "\t"); idx >= 0 {
		return line[:idx], nil
	}
	return line, nil
}
