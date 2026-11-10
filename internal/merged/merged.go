// Package merged classifies immutable branch tips using local ancestry, an
// isolated trial merge, then optional forge evidence. It never fetches refs.
package merged

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gitx "grove/internal/git"
)

// Branch pins the commit being classified, independently of later ref movement.
type Branch struct {
	Name string
	Tip  string
}

// Result contains positive evidence for this tip, or an explicit negative/unknown
// reason. Skipped tiers are incomplete checks, never evidence that a tip merged.
type Result struct {
	Merged  bool     `json:"merged"`
	Tier    string   `json:"tier"`
	Reason  string   `json:"reason"`
	Base    string   `json:"base,omitempty"`
	PR      int      `json:"pr,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Options limits each subprocess, including the optional single forge query per
// repository. CacheDir is disposable positive evidence, keyed by repo/base/name/tip.
// An empty CacheDir disables persistence. Zero Timeout defaults to five seconds.
type Options struct {
	Forge    bool
	Timeout  time.Duration
	CacheDir string
}

type refInfo struct {
	name, tip, tree string
	timestamp       int64
}

type checker struct {
	path    string
	options Options
}

// Check handles a repository's branches together so forge lookup happens at most
// once. Supplied Tips are pinned inventory commits; an empty Tip resolves Name.
// Results are keyed by Name. Local failures never count as merged, and skipped
// tiers are explicit so cleanup policy can distinguish evidence from uncertainty.
func Check(ctx context.Context, path, defaultBranch string, branches []Branch, options Options) map[string]Result {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	c := checker{path: path, options: options}
	if defaultBranch == "" {
		defaultBranch = gitx.DefaultBranch(path)
	}
	results := make(map[string]Result, len(branches))
	base, err := c.base(ctx, defaultBranch)
	if err != nil {
		for _, branch := range branches {
			results[branch.Name] = Result{Tier: "unknown", Reason: "default branch unavailable", Error: err.Error()}
		}
		return results
	}
	var pending []Branch
	objectDir := ""
	objectErr := ""
	mergeTreeUnsupported := false
	defer func() {
		if objectDir != "" {
			os.RemoveAll(objectDir)
		}
	}()
	for _, branch := range branches {
		result := Result{Tier: "not_merged", Reason: "not merged", Base: base.name}
		if branch.Tip == "" {
			out, err := c.git(ctx, nil, "rev-parse", "--verify", "refs/heads/"+branch.Name+"^{commit}")
			if err != nil {
				result.Error = err.Error()
				results[branch.Name] = result
				continue
			}
			branch.Tip = strings.TrimSpace(string(out))
		}
		// Only complete object ids may become revision arguments or cache keys.
		// This also prevents branch names from being interpreted as Git options.
		if !validOID(branch.Tip) {
			result.Error = "invalid branch tip"
			results[branch.Name] = result
			continue
		}
		_, err := c.git(ctx, nil, "merge-base", "--is-ancestor", branch.Tip, base.tip)
		if err == nil {
			result.Merged = true
			result.Tier = "ancestor"
			result.Reason = "ancestor of default branch"
			results[branch.Name] = result
			continue
		}
		if !exitCode(err, 1) {
			result.Error = err.Error()
			results[branch.Name] = result
			continue
		}
		if objectDir == "" && objectErr == "" {
			if safetyErr := c.trialMergeSafe(ctx); safetyErr != nil {
				objectErr = safetyErr.Error()
			}
		}
		if objectDir == "" && objectErr == "" {
			objectDir, err = os.MkdirTemp("", "grove-merge-objects-*")
			if err != nil {
				objectErr = err.Error()
			} else {
				objects, err := c.git(ctx, nil, "rev-parse", "--path-format=absolute", "--git-path", "objects")
				if err != nil {
					objectErr = err.Error()
				} else {
					// merge-tree writes objects even without checkout/index changes. A private
					// object store with a read-only alternate keeps real repositories untouched.
					err = os.MkdirAll(filepath.Join(objectDir, "info"), 0700)
					if err == nil {
						err = os.WriteFile(filepath.Join(objectDir, "info", "alternates"), []byte(strconv.Quote(strings.TrimSpace(string(objects)))+"\n"), 0600)
					}
					if err != nil {
						objectErr = err.Error()
					}
				}
			}
		}
		switch {
		case mergeTreeUnsupported:
			result.Skipped = append(result.Skipped, "merge_tree: Git lacks --write-tree")
		case objectErr != "":
			result.Skipped = append(result.Skipped, "merge_tree: "+objectErr)
		default:
			out, err := c.git(ctx, []string{"GIT_OBJECT_DIRECTORY=" + objectDir}, "-c", "core.hooksPath=/dev/null", "merge-tree", "--write-tree", base.tip, branch.Tip)
			if err == nil {
				fields := strings.Fields(string(out))
				if len(fields) > 0 && fields[0] == base.tree {
					result.Merged = true
					result.Tier = "merge_tree"
					result.Reason = "merge changes nothing"
					results[branch.Name] = result
					continue
				}
			} else if exitCode(err, 129) {
				mergeTreeUnsupported = true
				result.Skipped = append(result.Skipped, "merge_tree: Git lacks --write-tree")
			} else if !exitCode(err, 1) {
				result.Skipped = append(result.Skipped, "merge_tree: "+err.Error())
			}
		}
		results[branch.Name] = result
		pending = append(pending, branch)
	}
	c.forge(ctx, defaultName(defaultBranch), pending, results)
	return results
}

func defaultName(branch string) string {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/origin/", "origin/"} {
		branch = strings.TrimPrefix(branch, prefix)
	}
	return branch
}

func (c checker) base(ctx context.Context, branch string) (refInfo, error) {
	branch = defaultName(branch)
	var refs []refInfo
	for _, name := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
		out, err := c.git(ctx, nil, "show", "-s", "--format=%H %T %ct", name, "--")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(out))
		if len(fields) != 3 {
			continue
		}
		timestamp, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		refs = append(refs, refInfo{name: name, tip: fields[0], tree: fields[1], timestamp: timestamp})
	}
	if len(refs) == 0 {
		return refInfo{}, fmt.Errorf("default branch %q does not exist locally or at origin", branch)
	}
	if len(refs) == 1 {
		return refs[0], nil
	}
	// Ancestry establishes newer even with skewed commit clocks. If local and
	// remote diverged, compare commit times; no network refresh is implicit.
	if _, err := c.git(ctx, nil, "merge-base", "--is-ancestor", refs[0].tip, refs[1].tip); err == nil {
		return refs[1], nil
	}
	if _, err := c.git(ctx, nil, "merge-base", "--is-ancestor", refs[1].tip, refs[0].tip); err == nil {
		return refs[0], nil
	}
	if refs[1].timestamp > refs[0].timestamp {
		return refs[1], nil
	}
	return refs[0], nil
}

// commandError preserves the exit code separately from diagnostics. merge-tree's
// conflict exit (1) is ordinary negative evidence; unsupported Git (129) skips a tier.
type commandError struct {
	err    error
	detail string
}

func (e *commandError) Error() string { return e.detail }
func (e *commandError) Unwrap() error { return e.err }
func exitCode(err error, code int) bool {
	e, ok := err.(*commandError)
	if !ok {
		return false
	}
	status, ok := e.err.(*exec.ExitError)
	return ok && status.ExitCode() == code
}
func (c checker) git(ctx context.Context, extra []string, args ...string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer cancel()
	cmd := exec.CommandContext(bounded, "git", args...)
	cmd.Dir = c.path
	// Bound pipe draining too: a killed helper can leave a child holding stdout.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1"), extra...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if bounded.Err() != nil {
		return nil, &commandError{err: bounded.Err(), detail: bounded.Err().Error()}
	}
	if err != nil {
		return nil, &commandError{err: err, detail: fmt.Sprintf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))}
	}
	return out, nil
}
func validOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

// Trial merges honor attributes and may otherwise execute a configured external
// driver in the real checkout. Skip that tier when commands could run; isolated
// object storage alone is not an isolation boundary for user-provided programs.
func (c checker) trialMergeSafe(ctx context.Context) error {
	names, err := c.git(ctx, nil, "config", "--name-only", "--get-regexp", `^merge\..*\.driver$`)
	if err != nil && !exitCode(err, 1) {
		return err
	}
	if len(bytes.TrimSpace(names)) != 0 {
		return fmt.Errorf("external merge drivers configured")
	}
	value, err := c.git(ctx, nil, "config", "--bool", "--get", "merge.renormalize")
	if err != nil && !exitCode(err, 1) {
		return err
	}
	if strings.TrimSpace(string(value)) == "true" {
		return fmt.Errorf("merge renormalization may execute external filters")
	}
	return nil
}
