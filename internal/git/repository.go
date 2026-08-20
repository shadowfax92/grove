package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("branch is required")
	}
	if strings.TrimSpace(branch) != branch {
		return fmt.Errorf("invalid branch %q: leading or trailing whitespace is not allowed", branch)
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

func (r *Repository) WorktreePath(branch string) (string, error) {
	path, _, _, err := r.resolveWorktreePath(branch)
	return path, err
}

func (r *Repository) CreateWorktree(branch, startPoint string) (string, bool, error) {
	destination, existing, worktrees, err := r.resolveWorktreePath(branch)
	if err != nil {
		return "", false, err
	}
	if existing {
		return destination, false, nil
	}
	for _, worktree := range worktrees {
		if worktree.Main || worktree.Prunable {
			continue
		}
		if pathStrictlyContains(worktree.Path, destination) || pathStrictlyContains(destination, worktree.Path) {
			return "", false, fmt.Errorf("refusing nested worktree destination %s because it overlaps registered worktree %s", destination, worktree.Path)
		}
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", false, fmt.Errorf("destination already exists but is not a registered worktree: %s", destination)
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("checking destination: %w", err)
	}
	root, err := r.EnsureManagedRoot()
	if err != nil {
		return "", false, err
	}
	if err := rejectSymlinkComponents(root, destination); err != nil {
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

func (r *Repository) resolveWorktreePath(branch string) (string, bool, []WorktreeInfo, error) {
	destination, err := r.ManagedPath(branch)
	if err != nil {
		return "", false, nil, err
	}
	worktrees, err := r.Worktrees()
	if err != nil {
		return "", false, nil, err
	}
	var matches []WorktreeInfo
	for _, worktree := range worktrees {
		if worktree.Branch == branch {
			matches = append(matches, worktree)
		}
	}
	if len(matches) > 1 {
		paths := make([]string, 0, len(matches))
		for _, match := range matches {
			paths = append(paths, match.Path)
		}
		return "", false, nil, fmt.Errorf("branch %q is checked out in multiple worktrees: %s; use an absolute path", branch, strings.Join(paths, ", "))
	}
	if len(matches) == 1 {
		if matches[0].Prunable {
			return "", false, nil, fmt.Errorf("branch %q has a prunable worktree registration at %s; run git worktree prune first", branch, matches[0].Path)
		}
		return matches[0].Path, true, worktrees, nil
	}
	return destination, false, worktrees, nil
}

func (r *Repository) RemoveWorktree(path string, discard bool) error {
	target, err := r.validateWorktreeRemoval(path, discard)
	if err != nil {
		return err
	}
	if discard {
		if err := r.repairWorktreeGitFileIfNeeded(target); err != nil {
			return err
		}
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

func (r *Repository) repairWorktreeGitFileIfNeeded(target string) error {
	gitDirRaw, err := runGitText(target, "rev-parse", "--path-format=absolute", "--git-dir")
	if err == nil {
		gitDir, resolveErr := canonicalPath(strings.TrimSpace(gitDirRaw))
		if resolveErr == nil && r.worktreeAdminTargets(gitDir, target) {
			return nil
		}
	}

	adminRoot := filepath.Join(r.CommonDir, "worktrees")
	entries, err := os.ReadDir(adminRoot)
	if err != nil {
		return fmt.Errorf("repairing worktree registration: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		adminDir := filepath.Join(adminRoot, entry.Name())
		if !r.worktreeAdminTargets(adminDir, target) {
			continue
		}
		pointer := []byte("gitdir: " + adminDir + "\n")
		gitFile := filepath.Join(target, ".git")
		info, err := os.Lstat(gitFile)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("repairing worktree registration: %w", err)
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(gitFile); err != nil {
				return fmt.Errorf("repairing worktree registration: %w", err)
			}
		}
		if err := os.WriteFile(gitFile, pointer, 0644); err != nil {
			return fmt.Errorf("repairing worktree registration: %w", err)
		}
		return nil
	}
	return fmt.Errorf("repairing worktree registration: no administrative directory found for %s", target)
}

func (r *Repository) worktreeAdminTargets(adminDir, target string) bool {
	relative, err := filepath.Rel(filepath.Join(r.CommonDir, "worktrees"), adminDir)
	if err != nil || relative == "." || filepath.Dir(relative) != "." {
		return false
	}
	gitdir, err := os.ReadFile(filepath.Join(adminDir, "gitdir"))
	if err != nil {
		return false
	}
	registeredPath, err := canonicalPath(filepath.Dir(strings.TrimSpace(string(gitdir))))
	return err == nil && samePath(registeredPath, target)
}

func (r *Repository) PruneWorktrees() error {
	_, err := runGitText(r.MainPath, "worktree", "prune", "--expire", "now")
	return err
}

func (r *Repository) ValidateWorktreeRemoval(path string, discard bool) error {
	_, err := r.validateWorktreeRemoval(path, discard)
	return err
}

func (r *Repository) validateWorktreeRemoval(path string, discard bool) (string, error) {
	target, err := canonicalPath(path)
	if err != nil {
		return "", fmt.Errorf("resolving worktree path: %w", err)
	}
	worktrees, err := r.Worktrees()
	if err != nil {
		return "", err
	}
	var found *WorktreeInfo
	for i := range worktrees {
		if samePath(worktrees[i].Path, target) {
			found = &worktrees[i]
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("path is not a registered worktree: %s", target)
	}
	if found.Main {
		return "", fmt.Errorf("refusing to remove the main worktree: %s", target)
	}
	if found.Locked {
		if found.LockReason != "" {
			return "", fmt.Errorf("worktree is locked: %s", found.LockReason)
		}
		return "", fmt.Errorf("worktree is locked")
	}
	for _, worktree := range worktrees {
		if worktree.Prunable || samePath(worktree.Path, target) {
			continue
		}
		if pathStrictlyContains(target, worktree.Path) {
			return "", fmt.Errorf("refusing to remove %s because it contains registered worktree %s", target, worktree.Path)
		}
	}
	if !discard {
		nested, err := nestedGitRepository(target)
		if err != nil {
			return "", fmt.Errorf("checking for nested Git repositories: %w", err)
		}
		if nested != "" {
			return "", fmt.Errorf("refusing to remove %s because it contains nested Git repository %s", target, nested)
		}
	}
	return target, nil
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
	for _, ref := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
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
	if len(worktrees) != 0 && !worktrees[0].Bare {
		worktrees[0].Main = true
	}
	return worktrees
}

func rejectSymlinkComponents(root, destination string) error {
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("destination escapes the managed worktree root: %s", destination)
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("checking destination component %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlinked destination component %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("destination component is not a directory: %s", current)
		}
	}
	return nil
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

func pathStrictlyContains(parent, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type nestedRepositoryScan struct {
	children []string
	nested   string
	err      error
}

func nestedGitRepository(root string) (string, error) {
	const workerCount = 8
	jobs := make(chan string)
	results := make(chan nestedRepositoryScan, workerCount)
	cancelled := make(chan struct{})
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for directory := range jobs {
				result := scanDirectoryForNestedRepository(root, directory)
				select {
				case results <- result:
				case <-cancelled:
					return
				}
			}
		}()
	}

	queue := []string{root}
	active := 0
	stop := func() {
		close(cancelled)
		close(jobs)
		workers.Wait()
	}
	for len(queue) != 0 || active != 0 {
		var next chan<- string
		var directory string
		if len(queue) != 0 {
			next = jobs
			directory = queue[0]
		}
		select {
		case next <- directory:
			queue = queue[1:]
			active++
		case result := <-results:
			active--
			if result.err != nil {
				stop()
				return "", result.err
			}
			if result.nested != "" {
				stop()
				return result.nested, nil
			}
			queue = append(queue, result.children...)
		}
	}
	stop()
	return "", nil
}

func scanDirectoryForNestedRepository(root, directory string) nestedRepositoryScan {
	stream, err := os.Open(directory)
	if err != nil {
		return nestedRepositoryScan{err: err}
	}
	// File.ReadDir avoids sorting every cache directory during the safety scan.
	entries, err := stream.ReadDir(-1)
	closeErr := stream.Close()
	if err != nil {
		return nestedRepositoryScan{err: err}
	}
	if closeErr != nil {
		return nestedRepositoryScan{err: closeErr}
	}

	result := nestedRepositoryScan{}
	hasHead := false
	hasObjects := false
	hasRefs := false
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(directory, name)
		if name == ".git" {
			if directory != root {
				return nestedRepositoryScan{nested: directory}
			}
			continue
		}
		if name == "HEAD" && !entry.IsDir() {
			hasHead = true
		}
		switch name {
		case "objects", "refs":
			info, statErr := os.Stat(path)
			if statErr == nil && info.IsDir() {
				if name == "objects" {
					hasObjects = true
				} else {
					hasRefs = true
				}
			}
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			result.children = append(result.children, path)
		}
	}
	if directory != root && hasHead && hasObjects && hasRefs {
		result.nested = directory
	}
	return result
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
