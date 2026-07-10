package syncfile

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Candidate struct {
	Path     string
	Relative string
	Group    string
	Name     string
	URL      string
}

func (c Candidate) Key() string {
	return Repo{Group: c.Group, Entry: Entry{Name: c.Name}}.Key()
}

func (c Candidate) Entry() Entry {
	return Entry{URL: c.URL, Name: c.Name}
}

type ScanWarning struct {
	Path   string
	Reason string
}

type OriginReader func(repoPath string) (string, error)

// Scan discovers only standalone clones: every checkout prunes descent, while
// .git files (worktrees and submodules) are deliberately excluded.
func Scan(root string, jobs int, origin OriginReader) ([]Candidate, []ScanWarning, error) {
	paths, err := scanRepoPaths(root)
	if err != nil {
		return nil, nil, err
	}
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if origin == nil {
		origin = GitOrigin
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(paths) {
		jobs = len(paths)
	}

	type result struct {
		path string
		url  string
		err  error
	}
	queue := make(chan string)
	results := make(chan result, len(paths))
	var wg sync.WaitGroup
	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			for repoPath := range queue {
				url, err := origin(repoPath)
				results <- result{path: repoPath, url: strings.TrimSpace(url), err: err}
			}
		}()
	}
	go func() {
		for _, repoPath := range paths {
			queue <- repoPath
		}
		close(queue)
		wg.Wait()
		close(results)
	}()

	var candidates []Candidate
	var warnings []ScanWarning
	for item := range results {
		if item.err != nil || item.url == "" {
			reason := "origin URL is empty"
			if item.err != nil {
				reason = item.err.Error()
			}
			warnings = append(warnings, ScanWarning{Path: item.path, Reason: reason})
			continue
		}
		rel, err := filepath.Rel(root, item.path)
		if err != nil {
			return nil, nil, fmt.Errorf("relative repo path: %w", err)
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			warnings = append(warnings, ScanWarning{Path: item.path, Reason: "manifest root itself cannot be represented as an entry"})
			continue
		}
		group, name := splitCandidatePath(rel)
		candidates = append(candidates, Candidate{Path: item.path, Relative: rel, Group: group, Name: name, URL: item.url})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Relative < candidates[j].Relative })
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Path < warnings[j].Path })
	return candidates, warnings, nil
}

func GitOrigin(repoPath string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s", commandOutput(out, err))
	}
	return strings.TrimSpace(string(out)), nil
}

func FilterNewCandidates(candidates []Candidate, manifest *Manifest) []Candidate {
	seen := make(map[string]bool)
	if manifest != nil {
		for _, repo := range manifest.Repos() {
			seen[repo.Key()] = true
		}
	}
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !seen[candidate.Key()] {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func GroupCandidates(candidates []Candidate) map[string][]Entry {
	groups := make(map[string][]Entry)
	for _, candidate := range candidates {
		groups[candidate.Group] = append(groups[candidate.Group], candidate.Entry())
	}
	return groups
}

func scanRepoPaths(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sync root %s is not a directory", root)
	}

	var repos []string
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if current != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}

		gitPath := filepath.Join(current, ".git")
		gitInfo, statErr := os.Lstat(gitPath)
		switch {
		case statErr == nil:
			if gitInfo.IsDir() {
				repos = append(repos, current)
			}
			return filepath.SkipDir
		case !os.IsNotExist(statErr):
			return fmt.Errorf("stat %s: %w", gitPath, statErr)
		default:
			return nil
		}
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, err)
	}
	sort.Strings(repos)
	return repos, nil
}

func splitCandidatePath(relative string) (string, string) {
	parts := strings.Split(relative, "/")
	if len(parts) == 1 {
		return ".", parts[0]
	}
	return parts[0], strings.Join(parts[1:], "/")
}

func commandOutput(out []byte, err error) string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		return trimmed
	}
	return err.Error()
}
