package merged

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func fixture(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	git(t, path, "init", "-b", "main")
	git(t, path, "config", "user.name", "Fixture")
	git(t, path, "config", "user.email", "fixture@example.invalid")
	write(t, filepath.Join(path, "base"), "initial\n")
	git(t, path, "add", ".")
	git(t, path, "commit", "-m", "initial")
	return path
}
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func branch(t *testing.T, path, name string) Branch {
	t.Helper()
	git(t, path, "checkout", "-b", name)
	write(t, filepath.Join(path, "change"), name+"\n")
	git(t, path, "add", ".")
	git(t, path, "commit", "-m", "change")
	tip := git(t, path, "rev-parse", "HEAD")
	git(t, path, "checkout", "main")
	return Branch{Name: name, Tip: tip}
}
func check(t *testing.T, path string, branch Branch, options Options) Result {
	t.Helper()
	return Check(context.Background(), path, "main", []Branch{branch}, options)[branch.Name]
}
func TestAncestryUsesNewerRemote(t *testing.T) {
	path := fixture(t)
	topic := branch(t, path, "topic")
	git(t, path, "update-ref", "refs/remotes/origin/main", topic.Tip)
	got := check(t, path, topic, Options{})
	if !got.Merged || got.Tier != "ancestor" || got.Base != "refs/remotes/origin/main" {
		t.Fatalf("%+v", got)
	}
	// Local can also be newer than the remote-tracking ref.
	git(t, path, "update-ref", "refs/heads/main", topic.Tip)
	git(t, path, "update-ref", "refs/remotes/origin/main", topic.Tip+"^")
	got = check(t, path, topic, Options{})
	if !got.Merged || got.Base != "refs/heads/main" {
		t.Fatalf("newer local: %+v", got)
	}
}
func TestSquashMergeAndConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "squash", true: "conflict"}[conflict], func(t *testing.T) {
			path := fixture(t)
			topic := branch(t, path, "topic")
			git(t, path, "merge", "--squash", topic.Name)
			git(t, path, "commit", "-m", "squash")
			if conflict {
				write(t, filepath.Join(path, "change"), "different\n")
				git(t, path, "commit", "-am", "different")
			}
			// No trial-merge objects, index refresh, or worktree files may escape the
			// temporary object store, even on a conflicted merge.
			before := git(t, path, "count-objects", "-v")
			index := filepath.Join(path, ".git", "index")
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(index, old, old); err != nil {
				t.Fatal(err)
			}
			got := check(t, path, topic, Options{})
			if got.Merged == conflict || (!conflict && got.Tier != "merge_tree") {
				t.Fatalf("%+v", got)
			}
			if got.Error != "" {
				t.Fatal(got.Error)
			}
			if after := git(t, path, "count-objects", "-v"); before != after {
				t.Fatalf("objects changed: %s -> %s", before, after)
			}
			info, _ := os.Stat(index)
			if !info.ModTime().Equal(old) {
				t.Fatal("index refreshed")
			}
		})
	}
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func fakeGH(t *testing.T, script string) string {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "gh")
	write(t, path, "#!/bin/sh\n"+script+"\n")
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}
func TestForgeExactTipMovedBranchAndCache(t *testing.T) {
	path := fixture(t)
	a := branch(t, path, "topic")
	b := branch(t, path, "other")
	calls := filepath.Join(t.TempDir(), "calls")
	prs, _ := json.Marshal([]pullRequest{{Number: 42, HeadRefName: a.Name, HeadRefOID: a.Tip}, {Number: 41, HeadRefName: b.Name, HeadRefOID: strings.Repeat("a", 40)}})
	fakeGH(t, "echo call >> "+shellQuote(calls)+"\nprintf '%s' "+shellQuote(string(prs)))
	options := Options{Forge: true, CacheDir: t.TempDir()}
	results := Check(context.Background(), path, "main", []Branch{a, b}, options)
	if got := results[a.Name]; !got.Merged || got.Tier != "forge" || got.PR != 42 {
		t.Fatalf("exact tip %+v", got)
	}
	if got := results[b.Name]; got.Merged || got.PR != 41 || got.Reason != "branch moved since merged pull request" {
		t.Fatalf("moved %+v", got)
	}
	data, _ := os.ReadFile(calls)
	if string(data) != "call\n" {
		t.Fatalf("queries = %q", data)
	}
	// Cached merged evidence survives an unavailable forge, but never authorizes
	// a different tip under the same branch name.
	fakeGH(t, "exit 1")
	if got := check(t, path, a, options); !got.Merged || got.PR != 42 {
		t.Fatalf("cache %+v", got)
	}
	git(t, path, "checkout", a.Name)
	write(t, filepath.Join(path, "new"), "new\n")
	git(t, path, "add", ".")
	git(t, path, "commit", "-m", "new")
	a.Tip = git(t, path, "rev-parse", "HEAD")
	git(t, path, "checkout", "main")
	if got := check(t, path, a, options); got.Merged || len(got.Skipped) == 0 {
		t.Fatalf("cache reused across tips %+v", got)
	}
}
func TestForgeUnavailable(t *testing.T) {
	for _, mode := range []string{"absent", "timeout", "invalid", "failure"} {
		t.Run(mode, func(t *testing.T) {
			path := fixture(t)
			topic := branch(t, path, "topic")
			options := Options{Forge: true, Timeout: 250 * time.Millisecond}
			switch mode {
			case "absent":
				realGit, err := exec.LookPath("git")
				if err != nil {
					t.Fatal(err)
				}
				bin := t.TempDir()
				if err := os.Symlink(realGit, filepath.Join(bin, "git")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
			case "timeout":
				fakeGH(t, "exec sleep 30")
			case "invalid":
				fakeGH(t, "echo malformed")
			case "failure":
				fakeGH(t, "echo 'offline' >&2; exit 1")
			}
			started := time.Now()
			got := check(t, path, topic, options)
			if got.Merged || len(got.Skipped) == 0 || got.Error != "" {
				t.Fatalf("%s: %+v", mode, got)
			}
			if mode == "timeout" && time.Since(started) > 3*time.Second {
				t.Fatal("forge timeout not bounded")
			}
		})
	}
}
func TestOldGitSkipsMergeTreeButStillUsesForge(t *testing.T) {
	path := fixture(t)
	topic := branch(t, path, "topic")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nfor arg in \"$@\"; do if [ \"$arg\" = 'merge-tree' ]; then echo 'unknown option write-tree' >&2; exit 129; fi; done\nexec " + shellQuote(realGit) + " \"$@\"\n"
	write(t, filepath.Join(bin, "git"), script)
	if err := os.Chmod(filepath.Join(bin, "git"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got := check(t, path, topic, Options{})
	if got.Merged || len(got.Skipped) < 2 || !strings.Contains(got.Skipped[0], "--write-tree") {
		t.Fatalf("old git %+v", got)
	}
	prs, _ := json.Marshal([]pullRequest{{Number: 9, HeadRefName: topic.Name, HeadRefOID: topic.Tip}})
	fakeGH(t, "printf '%s' "+shellQuote(string(prs)))
	got = check(t, path, topic, Options{Forge: true})
	if !got.Merged || got.Tier != "forge" || len(got.Skipped) != 1 {
		t.Fatalf("forge fallback %+v", got)
	}
}
func TestMissingBaseAndInvalidTip(t *testing.T) {
	path := fixture(t)
	topic := branch(t, path, "topic")
	got := Check(context.Background(), path, "missing", []Branch{topic}, Options{})[topic.Name]
	if got.Merged || got.Error == "" {
		t.Fatalf("missing base %+v", got)
	}
	topic.Tip = "--bad"
	got = check(t, path, topic, Options{})
	if got.Merged || got.Error == "" {
		t.Fatalf("invalid tip %+v", got)
	}
}
