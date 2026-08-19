package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Repository struct {
	MainPath  string
	CommonDir string
}

func OpenRepository(dir string) (*Repository, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving repository path: %w", err)
	}
	commonRaw, err := runGitText(abs, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("%s is not a Git repository: %w", abs, err)
	}
	commonDir, err := canonicalPath(strings.TrimSpace(commonRaw))
	if err != nil {
		return nil, fmt.Errorf("resolving common Git directory: %w", err)
	}
	worktrees, err := ListWorktrees(abs)
	if err != nil {
		return nil, err
	}
	for _, worktree := range worktrees {
		if !worktree.Main {
			continue
		}
		mainPath, err := canonicalPath(worktree.Path)
		if err != nil {
			return nil, fmt.Errorf("resolving main worktree: %w", err)
		}
		return &Repository{MainPath: mainPath, CommonDir: commonDir}, nil
	}
	return nil, fmt.Errorf("repository at %s has no main worktree", abs)
}

func (r *Repository) Worktrees() ([]WorktreeInfo, error) {
	worktrees, err := ListWorktrees(r.MainPath)
	if err != nil {
		return nil, err
	}
	for i := range worktrees {
		if worktrees[i].Prunable {
			continue
		}
		if path, err := canonicalPath(worktrees[i].Path); err == nil {
			worktrees[i].Path = path
		}
		worktrees[i].Main = samePath(worktrees[i].Path, r.MainPath)
	}
	return worktrees, nil
}

func (r *Repository) EnsureManagedRoot() (string, error) {
	tracked, err := runGitBytes(r.MainPath, "ls-files", "-z", "--", ".wt")
	if err != nil {
		return "", err
	}
	if len(tracked) != 0 {
		return "", fmt.Errorf("refusing to use .wt because it contains tracked files")
	}

	root := filepath.Join(r.MainPath, ".wt")
	info, err := os.Lstat(root)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		return "", fmt.Errorf("refusing to use symlinked worktree root %s", root)
	case err == nil && !info.IsDir():
		return "", fmt.Errorf("worktree root %s is not a directory", root)
	case err != nil && !os.IsNotExist(err):
		return "", fmt.Errorf("checking worktree root: %w", err)
	}

	if err := os.MkdirAll(root, 0755); err != nil {
		return "", fmt.Errorf("creating worktree root: %w", err)
	}
	if err := r.ensureSharedExclude(); err != nil {
		return "", err
	}
	return root, nil
}

func (r *Repository) ManagedPath(branch string) (string, error) {
	if err := r.ValidateBranch(branch); err != nil {
		return "", err
	}
	root := filepath.Join(r.MainPath, ".wt")
	destination := filepath.Clean(filepath.Join(root, filepath.FromSlash(branch)))
	rel, err := filepath.Rel(root, destination)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("branch %q escapes the managed worktree root", branch)
	}
	return destination, nil
}

func (r *Repository) ValidateBranch(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch is required")
	}
	cmd := exec.Command("git", "check-ref-format", "--branch", branch)
	cmd.Dir = r.MainPath
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = "invalid Git branch name"
		}
		return fmt.Errorf("invalid branch %q: %s", branch, detail)
	}
	return nil
}

func (r *Repository) CreateWorktree(branch, startPoint string) (string, bool, error) {
	destination, err := r.ManagedPath(branch)
	if err != nil {
		return "", false, err
	}
	worktrees, err := r.Worktrees()
	if err != nil {
		return "", false, err
	}
	for _, worktree := range worktrees {
		if worktree.Branch == branch {
			return worktree.Path, false, nil
		}
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", false, fmt.Errorf("destination already exists but is not a registered worktree: %s", destination)
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("checking destination: %w", err)
	}
	if _, err := r.EnsureManagedRoot(); err != nil {
		return "", false, err
	}

	args := []string{"worktree", "add"}
	switch {
	case r.RefExists("refs/heads/" + branch):
		if startPoint != "" {
			return "", false, fmt.Errorf("start point can only be used when creating a new branch; branch %q already exists", branch)
		}
		args = append(args, destination, branch)
	case r.RefExists("refs/remotes/origin/" + branch):
		if startPoint != "" {
			return "", false, fmt.Errorf("start point can only be used when creating a new branch; branch %q already exists", branch)
		}
		args = append(args, "--track", "-b", branch, destination, "origin/"+branch)
	default:
		args = append(args, "-b", branch, destination)
		if startPoint != "" {
			args = append(args, startPoint)
		}
	}
	if _, err := runGitText(r.MainPath, args...); err != nil {
		return "", false, err
	}
	return destination, true, nil
}

func (r *Repository) RemoveWorktree(path string, discard bool) error {
	target, err := canonicalPath(path)
	if err != nil {
		return fmt.Errorf("resolving worktree path: %w", err)
	}
	worktrees, err := r.Worktrees()
	if err != nil {
		return err
	}
	var found *WorktreeInfo
	for i := range worktrees {
		if samePath(worktrees[i].Path, target) {
			found = &worktrees[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("path is not a registered worktree: %s", target)
	}
	if found.Main {
		return fmt.Errorf("refusing to remove the main worktree: %s", target)
	}
	if found.Locked {
		if found.LockReason != "" {
			return fmt.Errorf("worktree is locked: %s", found.LockReason)
		}
		return fmt.Errorf("worktree is locked")
	}

	args := []string{"worktree", "remove"}
	if discard {
		args = append(args, "--force")
	}
	args = append(args, target)
	if _, err := runGitText(r.MainPath, args...); err != nil {
		return err
	}
	return nil
}

func (r *Repository) Dirty(path string) (bool, error) {
	out, err := runGitBytes(path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return false, err
	}
	return len(out) != 0, nil
}

func (r *Repository) RefExists(ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = r.MainPath
	return cmd.Run() == nil
}

func (r *Repository) BaseRef(defaultBranch string) (string, error) {
	branch := strings.TrimPrefix(defaultBranch, "refs/heads/")
	branch = strings.TrimPrefix(branch, "refs/remotes/origin/")
	branch = strings.TrimPrefix(branch, "origin/")
	if branch == "" {
		branch = DefaultBranch(r.MainPath)
	}
	for _, ref := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch} {
		if r.RefExists(ref) {
			return ref, nil
		}
	}
	return "", fmt.Errorf("default branch %q does not exist locally or at origin", branch)
}

func (r *Repository) BranchMerged(branch, defaultBranch string) (bool, string, error) {
	base, err := r.BaseRef(defaultBranch)
	if err != nil {
		return false, "", err
	}
	ref := "refs/heads/" + branch
	if !r.RefExists(ref) {
		return false, base, fmt.Errorf("branch %q does not exist", branch)
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ref, base)
	cmd.Dir = r.MainPath
	err = cmd.Run()
	if err == nil {
		return true, base, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, base, nil
	}
	return false, base, fmt.Errorf("checking whether %s is merged into %s: %w", branch, base, err)
}

func (r *Repository) AheadBehind(path, defaultBranch string) (int, int, error) {
	base, err := r.BaseRef(defaultBranch)
	if err != nil {
		return 0, 0, err
	}
	out, err := runGitText(path, "rev-list", "--left-right", "--count", "HEAD..."+base)
	if err != nil {
		return 0, 0, err
	}
	var ahead, behind int
	if _, err := fmt.Sscanf(out, "%d %d", &ahead, &behind); err != nil {
		return 0, 0, fmt.Errorf("parsing ahead/behind counts %q: %w", out, err)
	}
	return ahead, behind, nil
}

func (r *Repository) ensureSharedExclude() error {
	path := filepath.Join(r.CommonDir, "info", "exclude")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading shared exclude: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "/.wt/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating Git info directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening shared exclude: %w", err)
	}
	defer f.Close()
	prefix := ""
	if len(data) != 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + "/.wt/\n"); err != nil {
		return fmt.Errorf("writing shared exclude: %w", err)
	}
	return nil
}

func parseWorktreesPorcelain(data []byte) []WorktreeInfo {
	records := bytes.Split(data, []byte{0, 0})
	worktrees := make([]WorktreeInfo, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		var worktree WorktreeInfo
		for _, field := range bytes.Split(record, []byte{0}) {
			text := string(field)
			switch {
			case strings.HasPrefix(text, "worktree "):
				worktree.Path = strings.TrimPrefix(text, "worktree ")
			case strings.HasPrefix(text, "HEAD "):
				worktree.Head = strings.TrimPrefix(text, "HEAD ")
			case strings.HasPrefix(text, "branch "):
				worktree.Branch = strings.TrimPrefix(strings.TrimPrefix(text, "branch "), "refs/heads/")
			case text == "bare":
				worktree.Bare = true
			case text == "detached":
				worktree.Detached = true
			case text == "locked":
				worktree.Locked = true
			case strings.HasPrefix(text, "locked "):
				worktree.Locked = true
				worktree.LockReason = strings.TrimPrefix(text, "locked ")
			case text == "prunable":
				worktree.Prunable = true
			case strings.HasPrefix(text, "prunable "):
				worktree.Prunable = true
				worktree.PrunableReason = strings.TrimPrefix(text, "prunable ")
			}
		}
		if worktree.Path != "" {
			worktrees = append(worktrees, worktree)
		}
	}
	for i := range worktrees {
		if !worktrees[i].Bare {
			worktrees[i].Main = true
			break
		}
	}
	return worktrees
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func runGitText(dir string, args ...string) (string, error) {
	out, err := runGitBytes(dir, args...)
	return strings.TrimSpace(string(out)), err
}

func runGitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return out, nil
}
