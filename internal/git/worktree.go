package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	// Try checking out an existing branch (local or remote tracking)
	cmd := exec.Command("git", "worktree", "add", destPath, branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	outStr := strings.TrimSpace(string(out))

	if strings.Contains(outStr, "is already used by worktree") || strings.Contains(outStr, "is already checked out") {
		return fmt.Errorf("branch %q is already checked out in another worktree", branch)
	}

	// Branch doesn't exist — create a new one from HEAD
	cmd = exec.Command("git", "worktree", "add", destPath, "-b", branch)
	cmd.Dir = repoPath
	out, err = cmd.CombinedOutput()
	if err != nil {
		outStr = strings.TrimSpace(string(out))
		if strings.Contains(outStr, "is already used by worktree") || strings.Contains(outStr, "is already checked out") {
			return fmt.Errorf("branch %q is already checked out in another worktree", branch)
		}
		return fmt.Errorf("git worktree add: %s (%w)", outStr, err)
	}
	return nil
}

func RemoveWorktree(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %s (%w)", strings.TrimSpace(string(out)), err)
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

func ListBranches(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "branch", "-a", "--format", "%(refname:short)")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var branches []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := line
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
