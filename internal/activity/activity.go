// Package activity measures source activity without walking ignored build output.
// This is independent of creation age and of whether a process currently uses a tree.
package activity

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Result retains partial timestamps on failure. Consumers must check Error
// before applying an inactivity threshold.
type Result struct {
	LastActive time.Time `json:"last_active"`
	Source     string    `json:"source,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Measure uses the newest tracked/untracked non-ignored file or private index.
// HEAD and reflog mtimes are excluded: maintenance rewrites reflogs without edits.
// Lstat counts a symlink itself, never the contents of a shared context store.
// Missing files (including unstaged deletions) have no mtime; this is a heuristic,
// never proof that a worktree is disposable. Failures remain explicit in Error.
func Measure(ctx context.Context, path string) Result {
	result := Result{}
	update := func(timestamp time.Time, source string) {
		if timestamp.After(result.LastActive) {
			result.LastActive = timestamp
			result.Source = source
		}
	}
	index, err := indexPath(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if info, err := os.Stat(index); err == nil {
		update(info.ModTime(), "index")
	} else if !os.IsNotExist(err) {
		result.Error = err.Error()
		return result
	}
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = path
	// Bound pipe draining too: a killed helper can leave a child holding stdout.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		result.Error = fmt.Sprintf("listing working files: %v: %s", err, strings.TrimSpace(stderr.String()))
		return result
	}
	newest, err := workingFiles(ctx, path, data)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	update(newest, "working_file")

	if result.LastActive.IsZero() {
		result.Error = "no activity timestamp available"
	}
	return result
}

// Scan bounds concurrent Git processes and filesystem reads across an inventory.
// Results retain per-worktree errors so a cleanup caller cannot mistake a failed
// measurement for an old timestamp. The caller owns cancellation.
func Scan(ctx context.Context, paths []string, workers int) map[string]Result {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string)
	results := make(map[string]Result, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				result := Measure(ctx, path)
				mu.Lock()
				results[path] = result
				mu.Unlock()
			}
		}()
	}
	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	return results
}

func indexPath(path string) (string, error) {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(gitPath, "index"), nil
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", err
	}
	pointer := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if !strings.HasPrefix(pointer, "gitdir: ") {
		return "", fmt.Errorf("invalid worktree Git pointer")
	}
	admin := strings.TrimPrefix(pointer, "gitdir: ")
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(path, admin)
	}
	return filepath.Join(admin, "index"), nil
}
