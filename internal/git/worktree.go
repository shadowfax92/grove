package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// pruneMu serializes `git worktree prune`. Removing distinct worktrees in
// parallel is safe (each touches its own $GIT_DIR/worktrees/<id>), but prune
// rewrites the repo-wide worktrees listing, so two firing at once — when several
// removals hit this fallback concurrently — can race each other.
var pruneMu sync.Mutex

type WorktreeInfo struct {
	Path     string
	Branch   string
	Head     string
	Bare     bool
	Prunable bool
}

func AddWorktree(repoPath, destPath, branch string) error {
	return AddWorktreeFrom(repoPath, destPath, branch, "")
}

func AddWorktreeFrom(repoPath, destPath, branch, startPoint string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("creating worktree parent dir: %w", err)
	}

	if LocalBranchExists(repoPath, branch) {
		if startPoint != "" {
			return fmt.Errorf("--from can only be used when creating a new branch; branch %q already exists", branch)
		}
		return worktreeAddExisting(repoPath, destPath, branch)
	}

	if RemoteBranchExists(repoPath, branch) {
		if startPoint != "" {
			return fmt.Errorf("--from can only be used when creating a new branch; branch %q already exists", branch)
		}
		return worktreeAddTracking(repoPath, destPath, branch)
	}

	return worktreeAddNew(repoPath, destPath, branch, resolveStartPoint(repoPath, startPoint))
}

func worktreeAddExisting(repoPath, destPath, branch string) error {
	cmd := exec.Command("git", "worktree", "add", destPath, branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return worktreeError(branch, out, err)
	}
	return nil
}

func worktreeAddTracking(repoPath, destPath, branch string) error {
	cmd := exec.Command("git", "worktree", "add", destPath, "--track", "-b", branch, "origin/"+branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return worktreeError(branch, out, err)
	}
	return nil
}

func worktreeAddNew(repoPath, destPath, branch, startPoint string) error {
	args := []string{"worktree", "add", destPath, "-b", branch}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return worktreeError(branch, out, err)
	}
	return nil
}

func resolveStartPoint(repoPath, startPoint string) string {
	if startPoint == "" || strings.HasPrefix(startPoint, "origin/") {
		return startPoint
	}
	if RemoteBranchExists(repoPath, startPoint) && !LocalBranchExists(repoPath, startPoint) {
		return "origin/" + startPoint
	}
	return startPoint
}

func worktreeError(branch string, out []byte, err error) error {
	outStr := strings.TrimSpace(string(out))
	if strings.Contains(outStr, "is already used by worktree") || strings.Contains(outStr, "is already checked out") {
		return fmt.Errorf("branch %q is already checked out in another worktree", branch)
	}
	return fmt.Errorf("git worktree add: %s (%w)", outStr, err)
}

func LocalBranchExists(repoPath, branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repoPath
	return cmd.Run() == nil
}

func RemoteBranchExists(repoPath, branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	cmd.Dir = repoPath
	return cmd.Run() == nil
}

func CurrentBranch(dir string) string {
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DefaultBranch returns the branch Grove should reset to before creating new worktrees.
// Remote HEAD is preferred because the current checkout may already be on a feature branch.
func DefaultBranch(repoPath string) string {
	if branch := remoteHeadBranch(repoPath); branch != "" {
		return branch
	}
	for _, branch := range []string{"main", "master"} {
		if LocalBranchExists(repoPath, branch) {
			return branch
		}
	}
	return CurrentBranch(repoPath)
}

func remoteHeadBranch(repoPath string) string {
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	return strings.TrimPrefix(branch, "origin/")
}

func RepoRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func HeadShortSha(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func RemoveWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoPath
	if _, err := cmd.CombinedOutput(); err != nil {
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("removing worktree directory: %w", err)
		}
		pruneMu.Lock()
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = repoPath
		_ = pruneCmd.Run()
		pruneMu.Unlock()
	}
	return nil
}

func ListWorktrees(repoPath string) ([]WorktreeInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	var worktrees []WorktreeInfo
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	var current WorktreeInfo
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = WorktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			current.Bare = true
		case strings.HasPrefix(line, "prunable"):
			current.Prunable = true
		}
	}
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

func EnsureGitignore(repoPath string) error {
	gitignorePath := filepath.Join(repoPath, ".gitignore")
	entry := ".grove/"

	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if strings.Contains(string(data), entry) {
		return nil
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + entry + "\n")
	return err
}
