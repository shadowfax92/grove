package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

type cleanupTarget struct {
	workspace    *state.Workspace
	repoPath     string
	worktreePath string
	label        string
	detail       string
}

func init() {
	cleanupCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	cleanupCmd.Flags().Bool("all", false, "Clean all stale workspaces without fzf selection")
	rootCmd.AddCommand(cleanupCmd)
}

var cleanupCmd = &cobra.Command{
	Use:         "cleanup",
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove stale workspaces and orphaned worktrees",
	Long: `Find and remove workspaces without running tmux sessions and orphaned worktrees.

Targets:
  • Workspaces in state with no running tmux session (e.g., created via --cd)
  • Orphaned worktrees on disk not tracked in state

  grove cleanup         — pick via fzf (Tab to multi-select)
  grove cleanup --all   — select all stale workspaces
  grove cleanup -f      — skip confirmation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		all, _ := cmd.Flags().GetBool("all")

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "Scanning %d workspaces and %d repos…\n", len(st.Workspaces), len(cfg.Repos))
		candidates := findCleanupCandidates(cfg, st)
		if len(candidates) == 0 {
			fmt.Fprintln(os.Stderr, "Nothing to clean up.")
			return nil
		}
		stale, orphans := 0, 0
		for _, c := range candidates {
			if c.workspace != nil {
				stale++
			} else {
				orphans++
			}
		}
		fmt.Fprintf(os.Stderr, "Found %d stale workspaces, %d orphaned worktrees\n", stale, orphans)

		var selected []cleanupTarget
		if all {
			selected = candidates
		} else {
			selected, err = pickCleanupFzf(candidates)
			if err != nil {
				return err
			}
		}

		if len(selected) == 0 {
			return nil
		}

		if !force {
			if len(selected) == 1 {
				fmt.Printf("Remove %s? [y/N] ", selected[0].label)
			} else {
				fmt.Printf("Remove %d workspaces?\n", len(selected))
				for _, t := range selected {
					fmt.Printf("  %s\n", t.label)
				}
				fmt.Print("[y/N] ")
			}
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		for _, t := range selected {
			if t.workspace != nil {
				mgr.RemoveWorkspace(st, t.workspace.SessionName)
			}
		}
		if err := mgr.Save(st); err != nil {
			return err
		}

		var failed []state.Workspace
		for i, t := range selected {
			fmt.Fprintf(os.Stderr, "[%d/%d] Removing %s…\n", i+1, len(selected), t.label)
			if t.workspace != nil && tmux.SessionExists(t.workspace.SessionName) {
				_ = tmux.KillSession(t.workspace.SessionName)
			}
			if t.worktreePath != "" {
				if _, statErr := os.Stat(t.worktreePath); statErr == nil {
					if err := git.RemoveWorktree(t.repoPath, t.worktreePath); err != nil {
						fmt.Fprintf(os.Stderr, "  warning: failed to remove worktree %s: %v\n", t.worktreePath, err)
						if t.workspace != nil {
							failed = append(failed, *t.workspace)
						}
						continue
					}
				}
			}
			fmt.Fprintf(os.Stderr, "  done\n")
		}

		if len(failed) > 0 {
			for _, ws := range failed {
				mgr.AddWorkspace(st, ws)
			}
			return mgr.Save(st)
		}
		return nil
	},
}

func findCleanupCandidates(cfg *config.Config, st *state.State) []cleanupTarget {
	// Single tmux call to get all live sessions
	liveSessions := make(map[string]bool)
	if sessions, err := tmux.ListSessions(); err == nil {
		for _, s := range sessions {
			liveSessions[s] = true
		}
	}

	var candidates []cleanupTarget
	for i := range st.Workspaces {
		ws := &st.Workspaces[i]
		if liveSessions[ws.SessionName] {
			continue
		}
		t := cleanupTarget{
			workspace: ws,
			label:     ws.Name,
		}
		if ws.Type == "worktree" {
			t.repoPath = ws.RepoPath
			t.worktreePath = ws.WorktreePath
		}
		if ws.LastUsedAt != "" {
			t.detail = state.RelativeTime(ws.LastUsedAt) + " ago"
		} else if ws.CreatedAt != "" {
			t.detail = state.RelativeTime(ws.CreatedAt) + " ago"
		}
		candidates = append(candidates, t)
	}

	trackedPaths := make(map[string]bool)
	for _, ws := range st.Workspaces {
		if ws.WorktreePath != "" {
			trackedPaths[ws.WorktreePath] = true
		}
	}

	// Fan out git worktree list across repos concurrently
	type orphanResult struct {
		targets []cleanupTarget
	}
	var wtRepos []config.RepoConfig
	for _, repo := range cfg.Repos {
		if repo.Type != "" && repo.Type != "worktree" {
			continue
		}
		wtRepos = append(wtRepos, repo)
	}

	results := make([]orphanResult, len(wtRepos))
	var wg sync.WaitGroup
	groveWorktreePrefix := string(filepath.Separator) + ".grove" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)
	for i, repo := range wtRepos {
		wg.Add(1)
		go func(idx int, r config.RepoConfig) {
			defer wg.Done()
			worktrees, err := git.ListWorktrees(r.Path)
			if err != nil {
				return
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
				results[idx].targets = append(results[idx].targets, cleanupTarget{
					repoPath:     r.Path,
					worktreePath: wt.Path,
					label:        fmt.Sprintf("%s/%s", r.Name, branch),
					detail:       "orphan",
				})
			}
		}(i, repo)
	}
	wg.Wait()

	for _, r := range results {
		candidates = append(candidates, r.targets...)
	}

	return candidates
}

func pickCleanupFzf(candidates []cleanupTarget) ([]cleanupTarget, error) {
	var lines []string
	for i, c := range candidates {
		tag := c.detail
		if tag == "" {
			tag = "stopped"
		}
		lines = append(lines, fmt.Sprintf("%d\t%-30s\t%s", i, c.label, tag))
	}

	fzfCmd := exec.Command("fzf",
		"--multi",
		"--prompt", "cleanup > ",
		"--header", "Select workspaces to remove (Tab to multi-select)",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "2,3",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w", err)
	}

	var selected []cleanupTarget
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		idx, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || idx < 0 || idx >= len(candidates) {
			continue
		}
		selected = append(selected, candidates[idx])
	}

	return selected, nil
}
