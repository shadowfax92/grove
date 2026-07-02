package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/workspaces"

	"github.com/spf13/cobra"
)

func init() {
	rmCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	rmCmd.Flags().Bool("yes", false, "Skip confirmation")
	rmCmd.Flags().String("path", "", "Remove the workspace with this worktree path")
	rmCmd.Flags().IntP("jobs", "j", defaultRemoveJobs, "Max worktrees to remove in parallel (lower it if deletion strains the machine)")
	rootCmd.AddCommand(rmCmd)
}

var (
	ErrRemovePathNotFound = errors.New("worktree path not found")
	ErrRemoveFailed       = errors.New("worktree removal failed")
)

var rmCmd = &cobra.Command{
	Use:         "rm [workspace...]",
	Aliases:     []string{"remove"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Remove workspaces and their worktrees",
	Long: `Remove grove workspaces and their worktrees.

  grove rm                — pick workspaces via fzf (Tab to multi-select)
  grove rm <w1> <w2> ...  — remove specific workspaces
  grove rm --path <path> --yes
                          — remove one workspace by worktree path without prompts`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		path, _ := cmd.Flags().GetString("path")
		jobs, _ := cmd.Flags().GetInt("jobs")
		confirmed := force || yes

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
		inv, err := workspaces.Build(st, nil)
		if err != nil {
			return err
		}

		if path != "" {
			if err := validateRemovePathMode(path, yes, args); err != nil {
				return err
			}
			return runRemovePath(mgr, st, path, jobs, removeWorktreeForTarget)
		}

		var targets []workspaces.RemoveTarget
		if len(args) == 0 {
			targets, err = pickRemoveTargetsFzf(inv)
		} else {
			targets, err = inv.ResolveRemoveTargets(args)
		}
		if err != nil {
			return err
		}

		if !confirmed && !confirmRemove(targets) {
			fmt.Println("Cancelled.")
			return nil
		}

		_, err = removeSelectedTargets(mgr, st, targets, jobs, removeWorktreeForTarget, os.Stdout, os.Stderr)
		return err
	},
}

func validateRemovePathMode(path string, confirmed bool, args []string) error {
	if path == "" {
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("--path cannot be combined with workspace arguments")
	}
	if !confirmed {
		return fmt.Errorf("--path requires --yes")
	}
	return nil
}

func runRemovePath(mgr *state.StateManager, st *state.State, path string, jobs int, remove func(workspaces.RemoveTarget) error) error {
	inv, err := workspaces.Build(st, nil)
	if err != nil {
		return err
	}
	target, ok := resolveRemoveTargetByWorktreePath(inv, path)
	if !ok {
		return fmt.Errorf("%w: %s", ErrRemovePathNotFound, path)
	}
	failed, err := removeSelectedTargets(mgr, st, []workspaces.RemoveTarget{target}, jobs, remove, os.Stderr, os.Stderr)
	if err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrRemoveFailed, path)
	}
	return nil
}

func resolveRemoveTargetByWorktreePath(inv *workspaces.Inventory, path string) (workspaces.RemoveTarget, bool) {
	targetPath := cleanAbsPath(path)
	for _, entry := range inv.Managed {
		ws := entry.Workspace
		if ws.WorktreePath == "" {
			continue
		}
		if cleanAbsPath(ws.WorktreePath) == targetPath {
			return workspaces.RemoveTarget{
				Workspace:   ws,
				SessionName: ws.SessionName,
			}, true
		}
	}
	return workspaces.RemoveTarget{}, false
}

func removeSelectedTargets(
	mgr *state.StateManager,
	st *state.State,
	targets []workspaces.RemoveTarget,
	jobs int,
	remove func(workspaces.RemoveTarget) error,
	out io.Writer,
	errOut io.Writer,
) ([]state.Workspace, error) {
	originalWorkspaces := append([]state.Workspace(nil), st.Workspaces...)
	workspaces.RemoveManagedEntries(st, targets)
	if err := mgr.Save(st); err != nil {
		st.Workspaces = originalWorkspaces
		return nil, err
	}

	failed := removeWorktreesWithOutput(targets, jobs, remove, out, errOut)

	if len(failed) > 0 {
		st.Workspaces = append(st.Workspaces[:0], originalWorkspaces...)
		if err := mgr.Save(st); err != nil {
			return failed, fmt.Errorf("%w: restoring state: %v", ErrRemoveFailed, err)
		}
	}
	return failed, nil
}

func confirmRemove(targets []workspaces.RemoveTarget) bool {
	if len(targets) == 1 {
		fmt.Printf("Remove %q? [y/N] ", targets[0].Label())
	} else {
		fmt.Printf("Remove %d workspaces?\n", len(targets))
		for _, t := range targets {
			fmt.Printf("  %s\n", t.Label())
		}
		fmt.Print("[y/N] ")
	}
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(answer)) == "y"
}

func removeWorktreeForTarget(t workspaces.RemoveTarget) error {
	ws := t.Workspace
	if ws.Type != "worktree" || ws.WorktreePath == "" || ws.WorktreePath == ws.RepoPath {
		return nil
	}
	if _, statErr := os.Stat(ws.WorktreePath); statErr != nil {
		return nil
	}
	return git.RemoveWorktree(ws.RepoPath, ws.WorktreePath)
}

const defaultRemoveJobs = 8

// removeWorktrees deletes each target's worktree through a bounded pool of at
// most `jobs` workers, printing live progress as each removal starts (→) and
// finishes (✓/✗). Bulk removals of large worktrees take minutes and pound the OS
// — every unlink is an fsevent — so both the visible progress and the cap on
// concurrent deletions matter (turn `jobs` down when the machine strains).
//
// We do NOT group by repo: grove's RepoPath is an unreliable identity for the
// underlying git repo (the same checkout can be configured under several names,
// e.g. with/without a trailing slash), so string grouping silently fails to
// serialize same-repo work. Instead we rely on `git worktree remove` of distinct
// worktrees being safe to run concurrently even within one repo — each only
// touches its own $GIT_DIR/worktrees/<id> admin dir — while the repo-wide
// `worktree prune` fallback is serialized inside git.RemoveWorktree.
//
// Returns the workspaces whose removal failed so the caller can restore them.
func removeWorktrees(targets []workspaces.RemoveTarget, jobs int, remove func(workspaces.RemoveTarget) error) []state.Workspace {
	return removeWorktreesWithOutput(targets, jobs, remove, os.Stdout, os.Stderr)
}

func removeWorktreesWithOutput(targets []workspaces.RemoveTarget, jobs int, remove func(workspaces.RemoveTarget) error, out io.Writer, errOut io.Writer) []state.Workspace {
	total := len(targets)
	if total == 0 {
		return nil
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > total {
		jobs = total
	}

	fmt.Fprintf(out, "Removing %d workspaces (up to %d in parallel)…\n", total, jobs)

	queue := make(chan workspaces.RemoveTarget)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex // guards done counter, failed slice, and ordered output
		done   int
		failed []state.Workspace
	)

	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			for t := range queue {
				mu.Lock()
				fmt.Fprintf(out, "  → %s\n", t.Label())
				mu.Unlock()

				err := remove(t)

				mu.Lock()
				done++
				if err != nil {
					fmt.Fprintf(errOut, "  ✗ %s: %v (%d/%d)\n", t.Label(), err, done, total)
					failed = append(failed, t.Workspace)
				} else {
					fmt.Fprintf(out, "  ✓ %s (%d/%d)\n", t.Label(), done, total)
				}
				mu.Unlock()
			}
		}()
	}

	for _, t := range targets {
		queue <- t
	}
	close(queue)
	wg.Wait()

	if n := len(failed); n > 0 {
		fmt.Fprintf(out, "Removed %d of %d workspaces; %d failed.\n", total-n, total, n)
	} else {
		fmt.Fprintf(out, "Removed %d workspaces.\n", total)
	}
	return failed
}

func pickRemoveTargetsFzf(inv *workspaces.Inventory) ([]workspaces.RemoveTarget, error) {
	targets := inv.RemoveCandidates()
	if len(targets) == 0 {
		return nil, fmt.Errorf("no workspaces to remove")
	}

	lookup := make(map[string]workspaces.RemoveTarget, len(targets))
	input := renderRemovePickerInput(targets, lookup)

	fzfCmd := exec.Command(
		"fzf",
		"--multi",
		"--prompt", "remove > ",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--accept-nth", "1",
		"--with-nth", "2,3",
	)
	fzfCmd.Stdin = strings.NewReader(input)
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	var selected []workspaces.RemoveTarget
	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if target, ok := lookup[id]; ok {
			selected = append(selected, target)
		}
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}

func renderRemovePickerInput(targets []workspaces.RemoveTarget, lookup map[string]workspaces.RemoveTarget) string {
	sorted := make([]workspaces.RemoveTarget, len(targets))
	copy(sorted, targets)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, tj := sorted[i].Workspace.CreatedAt, sorted[j].Workspace.CreatedAt
		if ti == "" {
			return false
		}
		if tj == "" {
			return true
		}
		return ti > tj
	})

	maxLabel := 0
	for _, t := range sorted {
		if n := len(t.Label()); n > maxLabel {
			maxLabel = n
		}
	}

	var lines []string
	for _, target := range sorted {
		lookup[target.SessionName] = target
		created := "—"
		if target.Workspace.CreatedAt != "" {
			created = state.RelativeTime(target.Workspace.CreatedAt) + " ago"
		}
		lines = append(lines, fmt.Sprintf("%s\t%-*s\t%s", target.SessionName, maxLabel, target.Label(), created))
	}
	return strings.Join(lines, "\n")
}
