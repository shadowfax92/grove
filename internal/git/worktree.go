package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type WorktreeInfo struct {
	Path   string
	Branch string
	Head   string
	Bare   bool
}

func AddWorktree(repoPath, destPath, branch string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("creating worktree parent dir: %w", err)
	}

	if LocalBranchExists(repoPath, branch) {
		return worktreeAddExisting(repoPath, destPath, branch)
	}

	if RemoteBranchExists(repoPath, branch) {
		return worktreeAddTracking(repoPath, destPath, branch)
	}

	// New branch from HEAD
	return worktreeAddNew(repoPath, destPath, branch)
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

func worktreeAddNew(repoPath, destPath, branch string) error {
	cmd := exec.Command("git", "worktree", "add", destPath, "-b", branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return worktreeError(branch, out, err)
	}
	return nil
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

func RemoveWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoPath
	if _, err := cmd.CombinedOutput(); err != nil {
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("removing worktree directory: %w", err)
		}
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = repoPath
		_ = pruneCmd.Run()
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
		}
	}
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

func ListRecentBranches(repoPath string, days int) ([]string, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	cmd := exec.Command("git", "for-each-ref",
		"--sort=-committerdate",
		"--format=%(committerdate:unix) %(refname:short)",
		"refs/heads/", "refs/remotes/origin/")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var branches []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		ts, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || ts < cutoff {
			continue
		}
		name := parts[1]
		if strings.HasPrefix(name, "origin/") {
			name = name[len("origin/"):]
		}
		if name == "" || name == "HEAD" || seen[name] {
			continue
		}
		seen[name] = true
		branches = append(branches, name)
	}
	return branches, nil
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
