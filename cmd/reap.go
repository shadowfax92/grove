package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/workspaces"

	"github.com/spf13/cobra"
)

func init() {
	reapCmd.Flags().Bool("dry-run", false, "Show stale workspaces without removing them")
	reapCmd.Flags().String("ttl", "", "Idle threshold override (e.g. 1h, 90m, 1d); defaults to config")
	reapCmd.Flags().IntP("jobs", "j", defaultRemoveJobs, "Max worktrees to remove in parallel")
	rootCmd.AddCommand(reapCmd)
}

var reapCmd = &cobra.Command{
	Use:         "reap",
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Retire stale managed workspaces that are proven safe",
	Long: `Retire stale Grove-managed workspaces after proving they are safe to remove.

A workspace must be idle past reap.ttl, backed by a clean worktree, inactive,
on the expected branch, and already merged into the repo's default branch.
Anything dirty, unmerged, active, recent, non-worktree, or ambiguous is skipped
and reported.

  grove reap              - remove eligible stale workspaces
  grove reap --dry-run    - show selected and skipped workspaces with reasons
  grove reap --ttl 1h     - override the configured idle threshold
  grove reap -j 2         - cap parallel worktree deletion`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := reapOptionsFromFlags(cmd)
		if err != nil {
			return err
		}
		report, err := runReap(opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
		printReapReport(cmd.OutOrStdout(), report, opts)
		return err
	},
}

type reapOptions struct {
	DryRun bool
	TTL    time.Duration
	Jobs   int
	Config *config.Config
	Now    time.Time
}

type reapDecision struct {
	Target        workspaces.RemoveTarget
	LastUsedAt    time.Time
	IdleFor       time.Duration
	Reason        string
	SkipReason    string
	DefaultBranch string
}

type reapReport struct {
	Matched []reapDecision
	Skipped []reapDecision
	Failed  []state.Workspace
}

var (
	reapNow               = time.Now
	reapCurrentBranch     = git.CurrentBranch
	reapWorktreeClean     = gitWorktreeClean
	reapMergedIntoDefault = gitMergedIntoDefault
	reapTmuxSessionActive = tmuxSessionActive
	reapRemoveWorktree    = removeWorktreeForTarget
)

func reapOptionsFromFlags(cmd *cobra.Command) (reapOptions, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	ttlRaw, _ := cmd.Flags().GetString("ttl")
	jobs, _ := cmd.Flags().GetInt("jobs")

	cfg, err := config.Load()
	if err != nil {
		return reapOptions{}, fmt.Errorf("loading config: %w", err)
	}

	ttl := cfg.Reap.TTL.Duration()
	if strings.TrimSpace(ttlRaw) != "" {
		ttl, err = config.ParseTTL(ttlRaw)
		if err != nil {
			return reapOptions{}, err
		}
	}

	return reapOptions{
		DryRun: dryRun,
		TTL:    ttl,
		Jobs:   jobs,
		Config: cfg,
		Now:    reapNow().UTC(),
	}, nil
}

// runReap evaluates managed workspaces under the state lock and removes only selected targets.
func runReap(opts reapOptions, out io.Writer, errOut io.Writer) (reapReport, error) {
	if opts.Now.IsZero() {
		opts.Now = reapNow().UTC()
	}
	mgr, err := state.NewManager()
	if err != nil {
		return reapReport{}, err
	}
	if err := mgr.Lock(); err != nil {
		return reapReport{}, err
	}
	defer mgr.Unlock()

	st, err := mgr.Load()
	if err != nil {
		return reapReport{}, err
	}

	report, err := selectReapTargets(st, opts)
	if err != nil {
		return reapReport{}, err
	}
	if opts.DryRun || len(report.Matched) == 0 {
		return report, nil
	}

	targets := make([]workspaces.RemoveTarget, 0, len(report.Matched))
	for _, decision := range report.Matched {
		targets = append(targets, decision.Target)
	}
	failed, err := removeSelectedTargets(mgr, st, targets, opts.Jobs, reapRemoveWorktree, out, errOut)
	report.Failed = failed
	if err != nil {
		return report, err
	}
	if len(failed) > 0 {
		return report, fmt.Errorf("%w: failed to reap %d workspaces", ErrRemoveFailed, len(failed))
	}
	return report, nil
}

// selectReapTargets classifies every managed workspace so dry-run output can explain both choices and skips.
func selectReapTargets(st *state.State, opts reapOptions) (reapReport, error) {
	inv, err := workspaces.Build(st, nil)
	if err != nil {
		return reapReport{}, err
	}

	report := reapReport{}
	for _, entry := range inv.ManagedByLastUsed() {
		decision := evaluateReapWorkspace(entry.Workspace, opts)
		if decision.SkipReason != "" {
			report.Skipped = append(report.Skipped, decision)
			continue
		}
		report.Matched = append(report.Matched, decision)
	}
	return report, nil
}

// evaluateReapWorkspace returns one selected-or-skipped decision without mutating state.
func evaluateReapWorkspace(ws state.Workspace, opts reapOptions) reapDecision {
	decision := reapDecision{
		Target: workspaces.RemoveTarget{
			Workspace:   ws,
			SessionName: ws.SessionName,
		},
	}

	if ws.Type != "worktree" {
		decision.SkipReason = "not a worktree workspace"
		return decision
	}
	if ws.WorktreePath == "" || ws.RepoPath == "" || ws.WorktreePath == ws.RepoPath {
		decision.SkipReason = "missing worktree metadata"
		return decision
	}

	lastUsed, err := workspaceLastUsed(ws)
	if err != nil {
		decision.SkipReason = err.Error()
		return decision
	}
	decision.LastUsedAt = lastUsed
	decision.IdleFor = opts.Now.Sub(lastUsed)
	if decision.IdleFor < 0 {
		decision.SkipReason = "last used timestamp is in the future"
		return decision
	}
	if decision.IdleFor < opts.TTL {
		decision.SkipReason = fmt.Sprintf("idle %s is below ttl %s", shortDuration(decision.IdleFor), shortDuration(opts.TTL))
		return decision
	}

	active, err := reapTmuxSessionActive(ws.SessionName)
	if err != nil {
		decision.SkipReason = "could not check tmux session: " + err.Error()
		return decision
	}
	if active {
		decision.SkipReason = "active tmux session"
		return decision
	}

	info, err := os.Stat(ws.WorktreePath)
	if err != nil {
		if os.IsNotExist(err) {
			decision.SkipReason = "worktree path is missing"
		} else {
			decision.SkipReason = "could not stat worktree: " + err.Error()
		}
		return decision
	}
	if !info.IsDir() {
		decision.SkipReason = "worktree path is not a directory"
		return decision
	}

	branch := reapCurrentBranch(ws.WorktreePath)
	if branch == "" {
		decision.SkipReason = "could not verify current branch"
		return decision
	}
	if ws.Branch == "" || branch != ws.Branch {
		decision.SkipReason = fmt.Sprintf("branch mismatch: state=%q worktree=%q", ws.Branch, branch)
		return decision
	}

	clean, err := reapWorktreeClean(ws.WorktreePath)
	if err != nil {
		decision.SkipReason = "could not check git status: " + err.Error()
		return decision
	}
	if !clean {
		decision.SkipReason = "dirty worktree"
		return decision
	}

	defaultBranch := defaultBranchForReap(opts.Config, ws)
	if defaultBranch == "" {
		decision.SkipReason = "default branch unknown"
		return decision
	}
	decision.DefaultBranch = defaultBranch
	if branch == defaultBranch {
		decision.SkipReason = "default branch workspace"
		return decision
	}

	merged, err := reapMergedIntoDefault(ws.RepoPath, ws.WorktreePath, defaultBranch)
	if err != nil {
		decision.SkipReason = "could not verify merge status: " + err.Error()
		return decision
	}
	if !merged {
		decision.SkipReason = "unmerged branch"
		return decision
	}

	decision.Reason = fmt.Sprintf("idle %s, clean, merged into %s", shortDuration(decision.IdleFor), defaultBranch)
	return decision
}

func workspaceLastUsed(ws state.Workspace) (time.Time, error) {
	if strings.TrimSpace(ws.LastUsedAt) != "" {
		t, err := time.Parse(time.RFC3339, ws.LastUsedAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid last_used_at")
		}
		return t.UTC(), nil
	}
	if strings.TrimSpace(ws.CreatedAt) != "" {
		t, err := time.Parse(time.RFC3339, ws.CreatedAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid created_at")
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("missing activity timestamp")
}

func defaultBranchForReap(cfg *config.Config, ws state.Workspace) string {
	if cfg != nil && ws.Repo != "" {
		if repo := cfg.FindRepo(ws.Repo); repo != nil && repo.DefaultBranch != "" {
			return repo.DefaultBranch
		}
	}
	if ws.RepoPath == "" {
		return ""
	}
	return git.DefaultBranch(ws.RepoPath)
}

func gitWorktreeClean(worktreePath string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%s (%w)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// gitMergedIntoDefault proves the worktree HEAD is already reachable from origin/default or local default.
func gitMergedIntoDefault(repoPath, worktreePath, defaultBranch string) (bool, error) {
	refs := defaultBranchRefs(repoPath, defaultBranch)
	if len(refs) == 0 {
		return false, fmt.Errorf("default branch %q not found", defaultBranch)
	}
	for _, ref := range refs {
		cmd := exec.Command("git", "merge-base", "--is-ancestor", "HEAD", ref)
		cmd.Dir = worktreePath
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			continue
		}
		return false, err
	}
	return false, nil
}

func defaultBranchRefs(repoPath, defaultBranch string) []string {
	if gitRefExists(repoPath, "refs/remotes/origin/"+defaultBranch) {
		return []string{"origin/" + defaultBranch}
	}
	if gitRefExists(repoPath, "refs/heads/"+defaultBranch) {
		return []string{defaultBranch}
	}
	return nil
}

func gitRefExists(repoPath, ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = repoPath
	return cmd.Run() == nil
}

func tmuxSessionActive(sessionName string) (bool, error) {
	if strings.TrimSpace(sessionName) == "" {
		return false, nil
	}
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("%s (%w)", strings.TrimSpace(string(out)), err)
}

func printReapReport(w io.Writer, report reapReport, opts reapOptions) {
	if opts.DryRun {
		printDryRunReapReport(w, report, opts)
		return
	}

	if len(report.Matched) == 0 {
		fmt.Fprintln(w, "No workspaces matched.")
	} else if len(report.Failed) == 0 {
		fmt.Fprintf(w, "Reaped %d workspaces:\n", len(report.Matched))
		for _, decision := range report.Matched {
			fmt.Fprintf(w, "  %-30s %s\n", decision.Target.Label(), decision.Reason)
		}
	} else {
		fmt.Fprintf(w, "Failed to reap %d of %d workspaces.\n", len(report.Failed), len(report.Matched))
	}
	printSkippedReapReport(w, report.Skipped)
}

func printDryRunReapReport(w io.Writer, report reapReport, opts reapOptions) {
	if len(report.Matched) == 0 {
		fmt.Fprintf(w, "No workspaces matched ttl %s.\n", shortDuration(opts.TTL))
	} else {
		fmt.Fprintf(w, "Would reap %d workspaces (ttl %s):\n", len(report.Matched), shortDuration(opts.TTL))
		for _, decision := range report.Matched {
			fmt.Fprintf(w, "  %-30s %s\n", decision.Target.Label(), decision.Reason)
		}
	}
	printSkippedReapReport(w, report.Skipped)
}

func printSkippedReapReport(w io.Writer, skipped []reapDecision) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(w, "Skipped %d workspaces:\n", len(skipped))
	for _, decision := range skipped {
		fmt.Fprintf(w, "  %-30s %s\n", decision.Target.Label(), decision.SkipReason)
	}
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
