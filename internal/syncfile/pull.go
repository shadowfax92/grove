package syncfile

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	gitutil "grove/internal/git"
)

type RepoState struct {
	Exists        bool
	Git           bool
	Dirty         bool
	Detached      bool
	Operation     string
	CurrentBranch string
	DefaultBranch string
	Err           error
}

type PullTarget struct {
	Repo  Repo
	State RepoState
}

type PullAction string

const (
	PullSkip              PullAction = "skip"
	PullFail              PullAction = "fail"
	PullCheckedOutDefault PullAction = "pull-default"
	PullAdvanceDefault    PullAction = "advance-default"
)

type PullDecision struct {
	Repo   Repo
	State  RepoState
	Action PullAction
	Reason string
}

type PullResultStatus string

const (
	PullUpdated PullResultStatus = "updated"
	PullCurrent PullResultStatus = "current"
	PullSkipped PullResultStatus = "skipped"
	PullFailed  PullResultStatus = "failed"
)

type PullResult struct {
	Repo   Repo
	Status PullResultStatus
	Reason string
}

type RepoInspector func(repo Repo) RepoState
type PullExecutor func(decision PullDecision) PullResult
type DefaultBranchResolver func(repoPath string) string

func FilterRepos(repos []Repo, only string) ([]Repo, error) {
	if _, err := MatchOnly("validate", only); err != nil {
		return nil, err
	}
	filtered := make([]Repo, 0, len(repos))
	for _, repo := range repos {
		matched, err := MatchOnly(repo.Key(), only)
		if err != nil {
			return nil, err
		}
		if matched {
			filtered = append(filtered, repo)
		}
	}
	return filtered, nil
}

func InspectRepos(repos []Repo, jobs int, inspect RepoInspector) []PullTarget {
	targets := make([]PullTarget, len(repos))
	if len(repos) == 0 {
		return targets
	}
	if inspect == nil {
		inspect = InspectLocal
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(repos) {
		jobs = len(repos)
	}

	queue := make(chan int)
	var wg sync.WaitGroup
	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			for index := range queue {
				targets[index] = PullTarget{Repo: repos[index], State: inspect(repos[index])}
			}
		}()
	}
	for index := range repos {
		queue <- index
	}
	close(queue)
	wg.Wait()
	return targets
}

// InspectLocal reads only local filesystem and Git metadata. It never fetches.
func InspectLocal(repo Repo) RepoState {
	kind, err := InspectPath(repo.Path)
	if err != nil {
		return RepoState{Exists: true, Err: err}
	}
	if kind == PathMissing {
		return RepoState{}
	}
	state := RepoState{Exists: true, Git: kind == PathGit}
	if !state.Git {
		return state
	}

	operation, err := inProgressOperation(repo.Path)
	if err != nil {
		state.Err = err
		return state
	}
	state.Operation = operation
	state.CurrentBranch = gitutil.CurrentBranch(repo.Path)
	state.Detached = state.CurrentBranch == ""

	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=normal")
	cmd.Dir = repo.Path
	out, err := cmd.CombinedOutput()
	if err != nil {
		state.Err = fmt.Errorf("git status: %s", commandOutput(out, err))
		return state
	}
	state.Dirty = len(strings.TrimSpace(string(out))) > 0
	return state
}

func ResolveDefaultBranches(targets []PullTarget, resolve DefaultBranchResolver) {
	if resolve == nil {
		resolve = gitutil.DefaultBranch
	}
	for i := range targets {
		if !targets[i].State.Exists || !targets[i].State.Git {
			continue
		}
		if targets[i].Repo.Branch != "" {
			targets[i].State.DefaultBranch = targets[i].Repo.Branch
		} else {
			targets[i].State.DefaultBranch = resolve(targets[i].Repo.Path)
		}
		if branch := targets[i].State.DefaultBranch; branch != "" && !validBranchName(branch) {
			targets[i].State.Err = fmt.Errorf("invalid default branch %q", branch)
		}
	}
}

func ClassifyPull(repo Repo, state RepoState) PullDecision {
	decision := PullDecision{Repo: repo, State: state}
	switch {
	case state.Err != nil:
		decision.Action = PullFail
		decision.Reason = state.Err.Error()
	case !state.Exists:
		decision.Action = PullSkip
		decision.Reason = "missing — run grove sync"
	case !state.Git:
		decision.Action = PullFail
		decision.Reason = "not a git repository"
	case state.Operation != "":
		decision.Action = PullFail
		decision.Reason = state.Operation + " in progress"
	case state.Detached:
		decision.Action = PullFail
		decision.Reason = "detached HEAD"
	case state.Dirty:
		decision.Action = PullFail
		decision.Reason = "uncommitted changes"
	case state.DefaultBranch == "":
		decision.Action = PullFail
		decision.Reason = "could not resolve default branch"
	case state.CurrentBranch == state.DefaultBranch:
		decision.Action = PullCheckedOutDefault
	default:
		decision.Action = PullAdvanceDefault
	}
	return decision
}

func RunPull(targets []PullTarget, jobs int, dryRun bool, execute PullExecutor) []PullResult {
	results := make([]PullResult, len(targets))
	decisions := make([]PullDecision, len(targets))
	var runnable []int
	for i, target := range targets {
		decision := ClassifyPull(target.Repo, target.State)
		decisions[i] = decision
		results[i].Repo = target.Repo
		switch decision.Action {
		case PullSkip:
			results[i].Status = PullSkipped
			results[i].Reason = decision.Reason
		case PullFail:
			results[i].Status = PullFailed
			results[i].Reason = decision.Reason
		default:
			if dryRun {
				results[i].Status = PullSkipped
				results[i].Reason = "would git " + strings.Join(PullArgs(decision), " ")
			} else {
				runnable = append(runnable, i)
			}
		}
	}
	if len(runnable) == 0 {
		return results
	}
	if execute == nil {
		execute = GitPull
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(runnable) {
		jobs = len(runnable)
	}

	queue := make(chan int)
	var wg sync.WaitGroup
	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			for index := range queue {
				result := execute(decisions[index])
				result.Repo = decisions[index].Repo
				results[index] = result
			}
		}()
	}
	for _, index := range runnable {
		queue <- index
	}
	close(queue)
	wg.Wait()
	return results
}

func PullArgs(decision PullDecision) []string {
	if decision.Action == PullCheckedOutDefault {
		return []string{"pull", "--ff-only"}
	}
	branch := decision.State.DefaultBranch
	ref := "refs/heads/" + branch
	return []string{"fetch", "origin", ref + ":" + ref}
}

func GitPull(decision PullDecision) PullResult {
	freshTarget := []PullTarget{{Repo: decision.Repo, State: InspectLocal(decision.Repo)}}
	ResolveDefaultBranches(freshTarget, nil)
	decision = ClassifyPull(decision.Repo, freshTarget[0].State)
	result := PullResult{Repo: decision.Repo}
	if decision.Action == PullSkip {
		result.Status = PullSkipped
		result.Reason = decision.Reason
		return result
	}
	if decision.Action == PullFail {
		result.Status = PullFailed
		result.Reason = decision.Reason
		return result
	}

	before := shortBranchSHA(decision.Repo.Path, decision.State.DefaultBranch)
	cmd := exec.Command("git", PullArgs(decision)...)
	cmd.Dir = decision.Repo.Path
	out, err := cmd.CombinedOutput()
	if err != nil {
		result.Status = PullFailed
		result.Reason = classifyPullCommandFailure(commandOutput(out, err))
		return result
	}

	if decision.Action == PullCheckedOutDefault {
		localOnly, checkErr := localCommitsNotOnOrigin(decision.Repo.Path, decision.State.DefaultBranch)
		if checkErr != nil {
			result.Status = PullFailed
			result.Reason = checkErr.Error()
			return result
		}
		if localOnly {
			result.Status = PullFailed
			result.Reason = "local commits not on origin"
			return result
		}
	}

	after := shortBranchSHA(decision.Repo.Path, decision.State.DefaultBranch)
	if before == after {
		result.Status = PullCurrent
		result.Reason = decision.State.DefaultBranch
		if decision.Action == PullAdvanceDefault {
			result.Reason += " (on " + decision.State.CurrentBranch + ")"
		}
		return result
	}
	result.Status = PullUpdated
	result.Reason = decision.State.DefaultBranch + " → " + after
	if decision.Action == PullAdvanceDefault {
		result.Reason += " (on " + decision.State.CurrentBranch + ")"
	}
	return result
}

func classifyPullCommandFailure(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "checked out at") || strings.Contains(lower, "checked out in another worktree"):
		return "default branch is checked out in another worktree"
	case strings.Contains(lower, "non-fast-forward"), strings.Contains(lower, "not possible to fast-forward"), strings.Contains(lower, "divergent branches"):
		return "local commits not on origin"
	default:
		return lastNonEmptyLine(output)
	}
}

func shortBranchSHA(repoPath, branch string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "refs/heads/"+branch)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func localCommitsNotOnOrigin(repoPath, branch string) (bool, error) {
	remoteRef := "refs/remotes/origin/" + branch
	show := exec.Command("git", "show-ref", "--verify", "--quiet", remoteRef)
	show.Dir = repoPath
	if err := show.Run(); err != nil {
		return false, nil
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", "refs/heads/"+branch, remoteRef)
	cmd.Dir = repoPath
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("checking default branch ancestry: %w", err)
}

func inProgressOperation(repoPath string) (string, error) {
	merge := exec.Command("git", "rev-parse", "--verify", "--quiet", "MERGE_HEAD")
	merge.Dir = repoPath
	if merge.Run() == nil {
		return "merge", nil
	}
	for _, marker := range []string{"rebase-merge", "rebase-apply"} {
		cmd := exec.Command("git", "rev-parse", "--git-path", marker)
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("locating git operation state: %s", commandOutput(out, err))
		}
		markerPath := strings.TrimSpace(string(out))
		if !filepath.IsAbs(markerPath) {
			markerPath = filepath.Join(repoPath, markerPath)
		}
		if _, err := os.Stat(markerPath); err == nil {
			return "rebase", nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("checking git operation state: %w", err)
		}
	}
	return "", nil
}

func lastNonEmptyLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return "git command failed"
}

func validBranchName(branch string) bool {
	cmd := exec.Command("git", "check-ref-format", "--branch", branch)
	return cmd.Run() == nil
}
