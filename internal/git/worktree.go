package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type WorktreeInfo struct {
	Path           string
	Branch         string
	Head           string
	Bare           bool
	Detached       bool
	Locked         bool
	LockReason     string
	Prunable       bool
	PrunableReason string
	Main           bool
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

func FetchOrigin(repoPath string) error {
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fetching origin: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func CleanWorktree(worktreePath string) error {
	reset := exec.Command("git", "reset", "--hard", "HEAD")
	reset.Dir = worktreePath
	out, err := reset.CombinedOutput()
	if err != nil {
		return fmt.Errorf("resetting worktree: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	clean := exec.Command("git", "clean", "-fd")
	clean.Dir = worktreePath
	out, err = clean.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cleaning worktree: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func ResetToRemoteBranch(repoPath, branch string) error {
	if err := FetchOrigin(repoPath); err != nil {
		return err
	}
	if err := CleanWorktree(repoPath); err != nil {
		return err
	}

	startPoint := "origin/" + branch
	cmd := exec.Command("git", "switch", "--discard-changes", "-C", branch, startPoint)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("resetting %s to %s: %s (%w)", branch, startPoint, strings.TrimSpace(string(out)), err)
	}
	return CleanWorktree(repoPath)
}

// DefaultBranch prefers remote HEAD because the current checkout may be a feature branch.
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
	repo, err := OpenRepository(repoPath)
	if err != nil {
		return err
	}
	return repo.RemoveWorktree(worktreePath, true)
}

func ListWorktrees(repoPath string) ([]WorktreeInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain", "-z")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return parseWorktreesPorcelain(out), nil
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
