package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/names"
	"grove/internal/state"

	"github.com/spf13/cobra"
)

func init() {
	recycleCmd.Flags().Bool("force", false, "Recycle even when the current branch has not reached origin/default")
	recycleCmd.Flags().Bool("json", false, "Print workspace metadata as JSON")
	rootCmd.AddCommand(recycleCmd)
}

var recycleCmd = &cobra.Command{
	Use:         "recycle [workspace] [branch]",
	Aliases:     []string{"rec"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Reuse a warm worktree for a fresh branch",
	Long: `Reuse an existing worktree while rotating it onto a fresh branch.

The worktree must be clean. Grove fetches origin, verifies that the current
branch is reachable from origin/<default>, creates the new branch there, and
updates the workspace state without running prepare or setup hooks. --force
bypasses only the merged-branch check.

  grove recycle                     — recycle the current workspace, auto-name the branch
  grove recycle feat/next-task      — recycle the current workspace onto this branch
  grove recycle mono/feat/old       — recycle an explicit workspace, auto-name the branch
  grove recycle mono/feat/old feat/next-task
                                    — recycle an explicit workspace onto this branch`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		jsonOut, _ := cmd.Flags().GetBool("json")

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
		ws, branch, err := resolveRecycleTarget(mgr, st, args)
		if err != nil {
			return err
		}
		result, err := recycleWorkspace(cfg, mgr, st, ws, branch, force)
		if err != nil {
			return err
		}
		return printRecycleResult(cmd.OutOrStdout(), result, jsonOut)
	},
}

var (
	recycleNow               = time.Now
	recycleCurrentBranch     = git.CurrentBranch
	recycleWorktreeClean     = gitWorktreeClean
	recycleMergedIntoDefault = gitMergedIntoDefault
	recycleFetchOrigin       = gitFetchOrigin
	recycleSwitchBranch      = gitSwitchNewBranch
	recycleRenameSession     = renameTmuxSessionIfLive
)

func resolveRecycleTarget(mgr *state.StateManager, st *state.State, args []string) (*state.Workspace, string, error) {
	if len(args) == 2 {
		ws := findWorkspaceRef(mgr, st, args[0])
		if ws == nil {
			return nil, "", fmt.Errorf("workspace %q not found", args[0])
		}
		return ws, args[1], nil
	}
	if len(args) == 1 {
		if ws := findWorkspaceRef(mgr, st, args[0]); ws != nil {
			return ws, "", nil
		}
		ws, err := recycleWorkspaceFromCwd(st)
		if err != nil {
			return nil, "", err
		}
		return ws, args[0], nil
	}
	ws, err := recycleWorkspaceFromCwd(st)
	return ws, "", err
}

func recycleWorkspaceFromCwd(st *state.State) (*state.Workspace, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}
	return findWorkspaceByCwd(st, cwd)
}

func recycleWorkspace(
	cfg *config.Config,
	mgr *state.StateManager,
	st *state.State,
	ws *state.Workspace,
	branch string,
	force bool,
) (*newWorkspaceResult, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	if ws.Type != "worktree" || ws.WorktreePath == "" || ws.RepoPath == "" || ws.WorktreePath == ws.RepoPath {
		return nil, fmt.Errorf("workspace %q is not a recyclable worktree", ws.Name)
	}

	currentBranch := recycleCurrentBranch(ws.WorktreePath)
	if currentBranch == "" {
		return nil, fmt.Errorf("could not determine the current branch for workspace %q", ws.Name)
	}
	if ws.Branch == "" || currentBranch != ws.Branch {
		return nil, fmt.Errorf("workspace %q branch mismatch: state=%q worktree=%q", ws.Name, ws.Branch, currentBranch)
	}

	clean, err := recycleWorktreeClean(ws.WorktreePath)
	if err != nil {
		return nil, fmt.Errorf("checking worktree status: %w", err)
	}
	if !clean {
		return nil, fmt.Errorf("workspace %q has a dirty worktree; commit, stash, or remove changes (including untracked files) before recycling", ws.Name)
	}

	defaultBranch := defaultBranchForReap(cfg, *ws)
	if defaultBranch == "" {
		return nil, fmt.Errorf("could not determine the default branch for workspace %q", ws.Name)
	}
	if err := recycleFetchOrigin(ws.RepoPath); err != nil {
		return nil, err
	}
	if !force {
		merged, err := recycleMergedIntoDefault(ws.RepoPath, ws.WorktreePath, defaultBranch)
		if err != nil {
			return nil, fmt.Errorf("checking whether %q reached origin/%s: %w", currentBranch, defaultBranch, err)
		}
		if !merged {
			return nil, fmt.Errorf("branch %q is not reachable from origin/%s; merge and push it first or use --force", currentBranch, defaultBranch)
		}
	}

	if branch == "" {
		branch = names.GenerateBranch(existingWorktreeNames(st, ws.Repo))
	}
	newSessionName := fmt.Sprintf("g/%s/%s", ws.Repo, branch)
	if existing := mgr.FindBySession(st, newSessionName); existing != nil {
		return nil, fmt.Errorf("workspace %q already exists", existing.Name)
	}
	if err := recycleSwitchBranch(ws.WorktreePath, branch, "origin/"+defaultBranch); err != nil {
		return nil, err
	}

	oldSessionName := ws.SessionName
	ws.Name = fmt.Sprintf("%s/%s", ws.Repo, branch)
	ws.Branch = branch
	ws.SessionName = newSessionName
	ws.LastUsedAt = recycleNow().UTC().Format(time.RFC3339)
	if err := mgr.Save(st); err != nil {
		return nil, fmt.Errorf("saving state: %w", err)
	}
	if err := recycleRenameSession(oldSessionName, newSessionName); err != nil {
		return nil, fmt.Errorf("renaming tmux session: %w", err)
	}

	return &newWorkspaceResult{
		Path:      workspaceDirWithConfig(ws, cfg),
		Workspace: *ws,
	}, nil
}

func gitFetchOrigin(repoPath string) error {
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fetching origin: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func gitSwitchNewBranch(worktreePath, branch, startPoint string) error {
	cmd := exec.Command("git", "switch", "--no-track", "-c", branch, startPoint)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("creating branch %q from %s: %s (%w)", branch, startPoint, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func renameTmuxSessionIfLive(oldName, newName string) error {
	if oldName == "" || oldName == newName {
		return nil
	}
	active, err := tmuxSessionActive(oldName)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	cmd := exec.Command("tmux", "rename-session", "-t", oldName, newName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s (%w)", strings.TrimSpace(string(out)), err)
		}
		return err
	}
	return nil
}

func printRecycleResult(w io.Writer, result *newWorkspaceResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(newWorkspaceJSON(result.Workspace))
	}
	_, err := fmt.Fprintln(w, result.Path)
	return err
}
