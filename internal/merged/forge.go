package merged

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type pullRequest struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOID  string `json:"headRefOid"`
}

type cacheEntry struct {
	Branch, Tip, Base string
	PR                int
}

func DefaultCacheDir() (string, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("XDG_STATE_HOME must be absolute")
	}
	return filepath.Join(root, "grove", "merged"), nil
}

func (c checker) forge(ctx context.Context, base string, branches []Branch, results map[string]Result) {
	skip := func(reason string) {
		for _, branch := range branches {
			result := results[branch.Name]
			result.Skipped = append(result.Skipped, "forge: "+reason)
			results[branch.Name] = result
		}
	}
	if !c.options.Forge {
		skip("disabled")
		return
	}
	var pending []Branch
	for _, branch := range branches {
		if pr := c.cached(base, branch); pr != 0 {
			result := results[branch.Name]
			result.Merged = true
			result.Tier = "forge"
			result.Reason = "merged pull request at current tip (cached)"
			result.PR = pr
			results[branch.Name] = result
		} else {
			pending = append(pending, branch)
		}
	}
	branches = pending
	if len(branches) == 0 {
		return
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		skip("gh unavailable")
		return
	}
	bounded, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer cancel()
	// One repository-wide query, rather than one network request per worktree.
	// A finite limit bounds response memory. At the cap, negative results say
	// truncated; a missing PR is never promoted into positive merge evidence.
	const limit = 10000
	cmd := exec.CommandContext(bounded, gh, "pr", "list", "--state", "merged", "--base", base, "--limit", fmt.Sprint(limit), "--json", "number,headRefName,headRefOid")
	cmd.Dir = c.path
	configureForgeProcess(cmd)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if bounded.Err() != nil {
		skip("query timed out or canceled")
		return
	}
	if err != nil {
		skip("query failed: " + strings.TrimSpace(stderr.String()))
		return
	}
	var prs []pullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		skip("invalid gh response")
		return
	}
	byBranch := make(map[string][]pullRequest)
	for _, pr := range prs {
		if pr.Number > 0 && validOID(pr.HeadRefOID) {
			byBranch[pr.HeadRefName] = append(byBranch[pr.HeadRefName], pr)
		}
	}
	for _, branch := range branches {
		result := results[branch.Name]
		for _, pr := range byBranch[branch.Name] {
			if pr.HeadRefOID == branch.Tip {
				result.Merged = true
				result.Tier = "forge"
				result.Reason = "merged pull request at current tip"
				result.PR = pr.Number
				c.cache(base, branch, pr.Number)
				break
			}
			result.Reason = "branch moved since merged pull request"
			result.PR = pr.Number
		}
		if !result.Merged && len(prs) >= limit {
			result.Skipped = append(result.Skipped, "forge: result limit reached")
		}
		results[branch.Name] = result
	}
}

func (c checker) cachePath(base string, branch Branch) string {
	if c.options.CacheDir == "" {
		return ""
	}
	identity, err := filepath.EvalSymlinks(c.path)
	if err != nil {
		return ""
	}
	key := sha256.Sum256([]byte(identity + "\x00" + base + "\x00" + branch.Name + "\x00" + branch.Tip))
	return filepath.Join(c.options.CacheDir, fmt.Sprintf("%x.json", key))
}
func (c checker) cached(base string, branch Branch) int {
	path := c.cachePath(base, branch)
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var entry cacheEntry
	if json.Unmarshal(data, &entry) != nil || entry.Branch != branch.Name || entry.Tip != branch.Tip || entry.Base != base || entry.PR <= 0 {
		return 0
	}
	return entry.PR
}
func (c checker) cache(base string, branch Branch, pr int) {
	path := c.cachePath(base, branch)
	if path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	data, err := json.Marshal(cacheEntry{Branch: branch.Name, Tip: branch.Tip, Base: base, PR: pr})
	if err != nil {
		return
	}
	// Per-tip atomic files avoid lost updates across concurrent Grove commands.
	// Cache failures only lose acceleration; freshly queried evidence still stands.
	f, err := os.CreateTemp(filepath.Dir(path), ".merged-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return
	}
	if f.Close() != nil {
		return
	}
	_ = os.Rename(f.Name(), path)
}
