package git

import (
	"fmt"
	"os/exec"
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

func DefaultBranch(repoPath string) string {
	if branch := remoteHeadBranch(repoPath); branch != "" {
		return branch
	}
	for _, branch := range []string{"main", "master"} {
		if refExists(repoPath, "refs/heads/"+branch) {
			return branch
		}
	}
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

func remoteHeadBranch(repoPath string) string {
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
}

func refExists(repoPath, ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = repoPath
	return cmd.Run() == nil
}
