package syncfile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type PathKind int

const (
	PathMissing PathKind = iota
	PathGit
	PathOccupied
)

type PathInspector func(target string) (PathKind, error)

type ApplyAction string

const (
	ApplyClone   ApplyAction = "clone"
	ApplyAlready ApplyAction = "already"
	ApplyFail    ApplyAction = "fail"
)

type ApplyItem struct {
	Repo   Repo
	Action ApplyAction
	Reason string
}

type ApplyResultStatus string

const (
	ApplyCloned  ApplyResultStatus = "cloned"
	ApplyPresent ApplyResultStatus = "present"
	ApplyFailed  ApplyResultStatus = "failed"
	ApplyPlanned ApplyResultStatus = "planned"
)

type ApplyResult struct {
	Repo   Repo
	Status ApplyResultStatus
	Reason string
}

type CloneFunc func(repo Repo) error

func PlanApply(manifest *Manifest, only string, inspect PathInspector) ([]ApplyItem, error) {
	if manifest == nil {
		return nil, fmt.Errorf("sync manifest is nil")
	}
	if _, err := MatchOnly("validate", only); err != nil {
		return nil, err
	}
	if inspect == nil {
		inspect = InspectPath
	}

	var plan []ApplyItem
	for _, repo := range manifest.Repos() {
		matched, err := MatchOnly(repo.Key(), only)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		kind, inspectErr := inspect(repo.Path)
		item := ApplyItem{Repo: repo}
		switch {
		case inspectErr != nil:
			item.Action = ApplyFail
			item.Reason = inspectErr.Error()
		case kind == PathMissing:
			item.Action = ApplyClone
		case kind == PathGit:
			item.Action = ApplyAlready
		default:
			item.Action = ApplyFail
			item.Reason = "path exists but is not a git repository"
		}
		plan = append(plan, item)
	}
	return plan, nil
}

func RunApply(items []ApplyItem, jobs int, dryRun bool, clone CloneFunc) []ApplyResult {
	results := make([]ApplyResult, len(items))
	var cloneIndexes []int
	for i, item := range items {
		results[i].Repo = item.Repo
		switch item.Action {
		case ApplyAlready:
			results[i].Status = ApplyPresent
		case ApplyFail:
			results[i].Status = ApplyFailed
			results[i].Reason = item.Reason
		case ApplyClone:
			if dryRun {
				results[i].Status = ApplyPlanned
				results[i].Reason = strings.Join(append([]string{"git"}, CloneArgs(item.Repo)...), " ")
			} else {
				cloneIndexes = append(cloneIndexes, i)
			}
		}
	}
	if len(cloneIndexes) == 0 {
		return results
	}
	if clone == nil {
		clone = GitClone
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(cloneIndexes) {
		jobs = len(cloneIndexes)
	}

	queue := make(chan int)
	var wg sync.WaitGroup
	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			for index := range queue {
				repo := items[index].Repo
				if err := clone(repo); err != nil {
					results[index].Status = ApplyFailed
					results[index].Reason = err.Error()
				} else {
					results[index].Status = ApplyCloned
				}
			}
		}()
	}
	for _, index := range cloneIndexes {
		queue <- index
	}
	close(queue)
	wg.Wait()
	return results
}

// InspectPath treats any existing path conservatively. A Git checkout must be
// rooted at the exact target; a directory merely nested inside another repo is
// still occupied, not already present.
func InspectPath(target string) (PathKind, error) {
	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			return PathMissing, nil
		}
		return PathOccupied, fmt.Errorf("stat target: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return PathOccupied, nil
	}
	if !info.IsDir() {
		return PathOccupied, nil
	}

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = target
	out, err := cmd.Output()
	if err != nil {
		return PathOccupied, nil
	}
	repoRoot := canonicalPath(strings.TrimSpace(string(out)))
	if repoRoot != canonicalPath(target) {
		return PathOccupied, nil
	}
	return PathGit, nil
}

func CloneArgs(repo Repo) []string {
	args := []string{"clone"}
	if repo.Branch != "" {
		args = append(args, "-b", repo.Branch)
	}
	return append(args, repo.URL, repo.Path)
}

func GitClone(repo Repo) error {
	if err := os.MkdirAll(filepath.Dir(repo.Path), 0755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}
	cmd := exec.Command("git", CloneArgs(repo)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %s", commandOutput(out, err))
	}
	return nil
}

func canonicalPath(value string) string {
	abs, err := filepath.Abs(value)
	if err == nil {
		value = abs
	}
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		value = resolved
	}
	return filepath.Clean(value)
}
