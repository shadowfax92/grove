package syncfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClassifyPullState(t *testing.T) {
	repo := Repo{Group: "clis", Entry: Entry{Name: "grove"}, Path: "/code/clis/grove"}
	tests := []struct {
		name   string
		state  RepoState
		action PullAction
		reason string
	}{
		{"missing", RepoState{}, PullSkip, "missing — run grove sync"},
		{"inspection error", RepoState{Exists: true, Err: errors.New("cannot inspect")}, PullFail, "cannot inspect"},
		{"not git", RepoState{Exists: true}, PullFail, "not a git repository"},
		{"merge", RepoState{Exists: true, Git: true, Operation: "merge"}, PullFail, "merge in progress"},
		{"rebase", RepoState{Exists: true, Git: true, Operation: "rebase"}, PullFail, "rebase in progress"},
		{"detached", RepoState{Exists: true, Git: true, Detached: true}, PullFail, "detached HEAD"},
		{"dirty", RepoState{Exists: true, Git: true, Dirty: true, CurrentBranch: "main"}, PullFail, "uncommitted changes"},
		{"unknown default", RepoState{Exists: true, Git: true, CurrentBranch: "feat/x"}, PullFail, "could not resolve default branch"},
		{"on default", RepoState{Exists: true, Git: true, CurrentBranch: "main", DefaultBranch: "main"}, PullCheckedOutDefault, ""},
		{"on feature", RepoState{Exists: true, Git: true, CurrentBranch: "feat/x", DefaultBranch: "main"}, PullAdvanceDefault, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPull(repo, tt.state)
			if got.Action != tt.action || got.Reason != tt.reason {
				t.Fatalf("ClassifyPull() = %#v, want action %s reason %q", got, tt.action, tt.reason)
			}
		})
	}
}

func TestResolveDefaultBranchesPrefersManifestBranch(t *testing.T) {
	targets := []PullTarget{
		{Repo: Repo{Entry: Entry{Name: "explicit", Branch: "trunk"}}, State: RepoState{Exists: true, Git: true}},
		{Repo: Repo{Entry: Entry{Name: "inferred"}, Path: "/repo"}, State: RepoState{Exists: true, Git: true}},
	}
	calls := 0
	ResolveDefaultBranches(targets, func(path string) string {
		calls++
		if path != "/repo" {
			t.Fatalf("resolver called for %q", path)
		}
		return "main"
	})
	if calls != 1 || targets[0].State.DefaultBranch != "trunk" || targets[1].State.DefaultBranch != "main" {
		t.Fatalf("resolved targets = %#v, calls = %d", targets, calls)
	}
}

func TestPullArgsNeverForceDefaultRefspec(t *testing.T) {
	repo := Repo{Path: "/repo"}
	tests := []struct {
		decision PullDecision
		want     []string
	}{
		{PullDecision{Repo: repo, State: RepoState{DefaultBranch: "main"}, Action: PullCheckedOutDefault}, []string{"pull", "--ff-only"}},
		{PullDecision{Repo: repo, State: RepoState{DefaultBranch: "main"}, Action: PullAdvanceDefault}, []string{"fetch", "origin", "refs/heads/main:refs/heads/main"}},
		{PullDecision{Repo: repo, State: RepoState{DefaultBranch: "+main"}, Action: PullAdvanceDefault}, []string{"fetch", "origin", "refs/heads/+main:refs/heads/+main"}},
	}
	for _, tt := range tests {
		got := PullArgs(tt.decision)
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("PullArgs() = %v, want %v", got, tt.want)
		}
		if len(got) == 3 && strings.HasPrefix(got[2], "+") {
			t.Fatalf("forced refspec in %v", got)
		}
	}
}

func TestClassifyPullCommandFailure(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"fatal: Not possible to fast-forward, aborting.", "local commits not on origin"},
		{" ! [rejected] main -> main (non-fast-forward)", "local commits not on origin"},
		{"fatal: refusing to fetch into branch 'refs/heads/main' checked out at '/other'", "default branch is checked out in another worktree"},
		{"fatal: unable to access remote", "fatal: unable to access remote"},
	}
	for _, tt := range tests {
		if got := classifyPullCommandFailure(tt.output); got != tt.want {
			t.Fatalf("classifyPullCommandFailure(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestRunPullContinuesAfterFailuresAndDryRunSkipsExecution(t *testing.T) {
	targets := []PullTarget{
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "dirty"}}, State: RepoState{Exists: true, Git: true, Dirty: true, CurrentBranch: "main", DefaultBranch: "main"}},
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "default"}}, State: RepoState{Exists: true, Git: true, CurrentBranch: "main", DefaultBranch: "main"}},
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "feature"}}, State: RepoState{Exists: true, Git: true, CurrentBranch: "feat/x", DefaultBranch: "main"}},
	}
	called := 0
	results := RunPull(targets, 2, false, func(decision PullDecision) PullResult {
		called++
		if decision.Repo.Name == "default" {
			return PullResult{Repo: decision.Repo, Status: PullFailed, Reason: "network"}
		}
		return PullResult{Repo: decision.Repo, Status: PullUpdated, Reason: "main → abc123 (on feat/x)"}
	})
	if called != 2 || results[0].Reason != "uncommitted changes" || results[1].Status != PullFailed || results[2].Status != PullUpdated {
		t.Fatalf("results = %#v, called = %d", results, called)
	}

	called = 0
	dry := RunPull(targets[1:], 2, true, func(PullDecision) PullResult { called++; return PullResult{} })
	if called != 0 || dry[0].Status != PullSkipped || !strings.Contains(dry[0].Reason, "would git pull --ff-only") || !strings.Contains(dry[1].Reason, "would git fetch origin refs/heads/main:refs/heads/main") {
		t.Fatalf("dry results = %#v, called = %d", dry, called)
	}
}

func TestResolveDefaultBranchesRejectsInvalidBranch(t *testing.T) {
	targets := []PullTarget{{
		Repo:  Repo{Entry: Entry{Name: "bad", Branch: "bad:name"}},
		State: RepoState{Exists: true, Git: true},
	}}
	ResolveDefaultBranches(targets, nil)
	if targets[0].State.Err == nil || !strings.Contains(targets[0].State.Err.Error(), "invalid default branch") {
		t.Fatalf("resolved state = %#v", targets[0].State)
	}
}

func TestGitPullAdvancesDefaultWithoutSwitchingFeatureBranch(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	checkout := filepath.Join(root, "checkout")
	runTestGit(t, root, "init", "--bare", origin)
	runTestGit(t, root, "init", seed)
	runTestGit(t, seed, "config", "user.email", "test@example.com")
	runTestGit(t, seed, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(seed, "file.txt"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, seed, "add", "file.txt")
	runTestGit(t, seed, "commit", "-m", "one")
	runTestGit(t, seed, "branch", "-M", "main")
	runTestGit(t, seed, "remote", "add", "origin", origin)
	runTestGit(t, seed, "push", "-u", "origin", "main")
	runTestGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	runTestGit(t, root, "clone", origin, checkout)
	runTestGit(t, checkout, "checkout", "-b", "feat/x")
	before := strings.TrimSpace(runTestGit(t, checkout, "rev-parse", "refs/heads/main"))

	if err := os.WriteFile(filepath.Join(seed, "file.txt"), []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, seed, "add", "file.txt")
	runTestGit(t, seed, "commit", "-m", "two")
	runTestGit(t, seed, "push", "origin", "main")

	decision := PullDecision{
		Repo:   Repo{Group: "clis", Entry: Entry{Name: "checkout"}, Path: checkout},
		State:  RepoState{Exists: true, Git: true, CurrentBranch: "feat/x", DefaultBranch: "main"},
		Action: PullAdvanceDefault,
	}
	result := GitPull(decision)
	if result.Status != PullUpdated || !strings.Contains(result.Reason, "(on feat/x)") {
		t.Fatalf("GitPull() = %#v", result)
	}
	after := strings.TrimSpace(runTestGit(t, checkout, "rev-parse", "refs/heads/main"))
	if before == after {
		t.Fatal("local main did not advance")
	}
	if branch := strings.TrimSpace(runTestGit(t, checkout, "branch", "--show-current")); branch != "feat/x" {
		t.Fatalf("current branch = %q, want feat/x", branch)
	}
}

func TestGitPullRefreshesSafetyStateBeforeExecuting(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "repo")
	runTestGit(t, filepath.Dir(repoPath), "init", repoPath)
	runTestGit(t, repoPath, "config", "user.email", "test@example.com")
	runTestGit(t, repoPath, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoPath, "file.txt"), []byte("clean"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoPath, "add", "file.txt")
	runTestGit(t, repoPath, "commit", "-m", "base")
	runTestGit(t, repoPath, "branch", "-M", "main")

	staleDecision := PullDecision{
		Repo:   Repo{Group: "clis", Entry: Entry{Name: "repo", Branch: "main"}, Path: repoPath},
		State:  RepoState{Exists: true, Git: true, CurrentBranch: "main", DefaultBranch: "main"},
		Action: PullCheckedOutDefault,
	}
	if err := os.WriteFile(filepath.Join(repoPath, "file.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	result := GitPull(staleDecision)
	if result.Status != PullFailed || result.Reason != "uncommitted changes" {
		t.Fatalf("GitPull() = %#v, want dirty failure", result)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return string(out)
}
