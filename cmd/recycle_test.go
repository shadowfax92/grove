package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"grove/internal/config"
	"grove/internal/state"
)

func TestResolveRecycleTargetFromCwdOrExplicitWorkspace(t *testing.T) {
	root := t.TempDir()
	worktreePath := filepath.Join(root, "slot")
	nested := filepath.Join(worktreePath, "packages", "app")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	worktreePath = cleanAbsPath(worktreePath)
	nested = filepath.Join(worktreePath, "packages", "app")
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	mgr := &state.StateManager{}
	st := &state.State{Workspaces: []state.Workspace{{
		Name:         "mono/feat/old",
		Type:         "worktree",
		Repo:         "mono",
		WorktreePath: worktreePath,
		Branch:       "feat/old",
		SessionName:  "g/mono/feat/old",
	}}}

	tests := []struct {
		name       string
		args       []string
		wantBranch string
	}{
		{name: "cwd auto"},
		{name: "cwd explicit branch", args: []string{"feat/next"}, wantBranch: "feat/next"},
		{name: "explicit workspace auto", args: []string{"mono/feat/old"}},
		{name: "explicit workspace and branch", args: []string{"g/mono/feat/old", "feat/next"}, wantBranch: "feat/next"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws, branch, err := resolveRecycleTarget(mgr, st, tt.args)
			if err != nil {
				t.Fatalf("resolveRecycleTarget() error = %v", err)
			}
			if ws.Name != "mono/feat/old" {
				t.Fatalf("workspace = %q, want mono/feat/old", ws.Name)
			}
			if branch != tt.wantBranch {
				t.Fatalf("branch = %q, want %q", branch, tt.wantBranch)
			}
		})
	}
}

func TestRecycleRefusesUntrackedFilesWithClearError(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	repoPath := initRecycleTestRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "slot")
	runRecycleTestGit(t, repoPath, "worktree", "add", "-b", "feat/old", worktreePath)
	if err := os.WriteFile(filepath.Join(worktreePath, "untracked.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace(repoPath, worktreePath),
	}}
	cfg := recycleTestConfig(repoPath)

	_, err = recycleWorkspace(cfg, mgr, st, &st.Workspaces[0], "feat/next", false)
	if err == nil {
		t.Fatal("recycleWorkspace() error = nil, want dirty-worktree refusal")
	}
	for _, want := range []string{"dirty worktree", "untracked files"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want containing %q", err, want)
		}
	}
	if got := recycleTestGitOutput(t, worktreePath, "branch", "--show-current"); got != "feat/old" {
		t.Fatalf("branch changed to %q despite dirty-worktree refusal", got)
	}
}

func TestRecycleRefusesUnmergedUnlessForced(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	worktreePath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace("/repo", worktreePath),
	}}
	cfg := recycleTestConfig("/repo")

	var calls []string
	recycleCurrentBranch = func(string) string { return "feat/old" }
	recycleWorktreeClean = func(string) (bool, error) { return true, nil }
	recycleFetchOrigin = func(string) error {
		calls = append(calls, "fetch")
		return nil
	}
	recycleMergedIntoDefault = func(_, _, _ string) (bool, error) {
		calls = append(calls, "merged")
		return false, nil
	}
	recycleSwitchBranch = func(_, _, _ string) error {
		calls = append(calls, "switch")
		return nil
	}
	recycleResolveRef = func(_, _ string) (string, error) { return "start-oid", nil }
	recycleSessionExists = func(string) (bool, error) { return false, nil }
	recycleRenameSession = func(_, _ string) (bool, error) {
		calls = append(calls, "rename")
		return true, nil
	}

	_, err = recycleWorkspace(cfg, mgr, st, &st.Workspaces[0], "feat/next", false)
	if err == nil {
		t.Fatal("recycleWorkspace() error = nil, want unmerged refusal")
	}
	for _, want := range []string{"not reachable from origin/main", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want containing %q", err, want)
		}
	}
	if got := strings.Join(calls, ","); got != "fetch,merged" {
		t.Fatalf("calls = %q, want fetch,merged", got)
	}

	calls = nil
	result, err := recycleWorkspace(cfg, mgr, st, &st.Workspaces[0], "feat/next", true)
	if err != nil {
		t.Fatalf("recycleWorkspace(force) error = %v", err)
	}
	if result.Workspace.Branch != "feat/next" {
		t.Fatalf("branch = %q, want feat/next", result.Workspace.Branch)
	}
	if got := strings.Join(calls, ","); got != "fetch,switch,rename" {
		t.Fatalf("force calls = %q, want fetch,switch,rename", got)
	}
}

func TestRecycleHappyPathKeepsWarmWorktreeAndUpdatesState(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	originPath := filepath.Join(t.TempDir(), "origin.git")
	runRecycleTestGit(t, "", "init", "--bare", "--initial-branch=main", originPath)

	repoPath := initRecycleTestRepo(t)
	runRecycleTestGit(t, repoPath, "remote", "add", "origin", originPath)
	runRecycleTestGit(t, repoPath, "push", "-u", "origin", "main")

	worktreePath := filepath.Join(t.TempDir(), "warm-slot")
	runRecycleTestGit(t, repoPath, "worktree", "add", "-b", "feat/old", worktreePath)
	writeRecycleTestFile(t, filepath.Join(worktreePath, "feature.txt"), "feature\n")
	runRecycleTestGit(t, worktreePath, "add", "feature.txt")
	runRecycleTestGit(t, worktreePath, "commit", "-m", "feature")
	oldHead := recycleTestGitOutput(t, worktreePath, "rev-parse", "HEAD")
	runRecycleTestGit(t, repoPath, "merge", "--no-ff", "--no-edit", "feat/old")
	runRecycleTestGit(t, repoPath, "push", "origin", "main")

	contributorPath := filepath.Join(t.TempDir(), "contributor")
	runRecycleTestGit(t, "", "clone", originPath, contributorPath)
	configureRecycleTestGitUser(t, contributorPath)
	writeRecycleTestFile(t, filepath.Join(contributorPath, "remote.txt"), "new remote work\n")
	runRecycleTestGit(t, contributorPath, "add", "remote.txt")
	runRecycleTestGit(t, contributorPath, "commit", "-m", "remote update")
	runRecycleTestGit(t, contributorPath, "push", "origin", "main")
	remoteHead := recycleTestGitOutput(t, contributorPath, "rev-parse", "HEAD")
	if stale := recycleTestGitOutput(t, repoPath, "rev-parse", "origin/main"); stale == remoteHead {
		t.Fatal("test setup failed: origin/main is already current before recycle fetch")
	}

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	createdAt := "2026-07-01T18:06:00Z"
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace(repoPath, worktreePath),
	}}
	st.Workspaces[0].CreatedAt = createdAt
	st.Workspaces[0].LastUsedAt = "2026-07-01T18:07:00Z"
	if err := mgr.Save(st); err != nil {
		t.Fatal(err)
	}
	cfg := recycleTestConfig(repoPath)
	cfg.Repos[0].Setup = []string{"touch setup-ran"}

	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	recycleNow = func() time.Time { return now }
	recycleSessionExists = func(string) (bool, error) { return false, nil }
	var renamedFrom, renamedTo string
	recycleRenameSession = func(oldName, newName string) (bool, error) {
		renamedFrom, renamedTo = oldName, newName
		return true, nil
	}

	result, err := recycleWorkspace(cfg, mgr, st, &st.Workspaces[0], "feat/next", false)
	if err != nil {
		t.Fatalf("recycleWorkspace() error = %v", err)
	}
	if result.Path != worktreePath {
		t.Fatalf("result path = %q, want unchanged slot %q", result.Path, worktreePath)
	}
	if got := recycleTestGitOutput(t, worktreePath, "branch", "--show-current"); got != "feat/next" {
		t.Fatalf("current branch = %q, want feat/next", got)
	}
	if got := recycleTestGitOutput(t, worktreePath, "rev-parse", "HEAD"); got != remoteHead {
		t.Fatalf("new branch HEAD = %s, want fetched origin/main %s", got, remoteHead)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "setup-ran")); !os.IsNotExist(err) {
		t.Fatalf("setup hook ran during recycle; stat error = %v", err)
	}
	if got := recycleTestGitOutput(t, repoPath, "rev-parse", "feat/old"); got != oldHead {
		t.Fatalf("old branch head = %s, want preserved %s", got, oldHead)
	}
	if got := recycleTestGitOutput(t, repoPath, "ls-remote", "--heads", "origin", "feat/next"); got != "" {
		t.Fatalf("new branch was unexpectedly pushed: %q", got)
	}

	loaded, err := mgr.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Workspaces) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(loaded.Workspaces))
	}
	got := loaded.Workspaces[0]
	if got.Name != "mono/feat/next" || got.Branch != "feat/next" || got.SessionName != "g/mono/feat/next" {
		t.Fatalf("recycled workspace identity = %#v", got)
	}
	if got.WorktreePath != worktreePath {
		t.Fatalf("worktree path = %q, want unchanged %q", got.WorktreePath, worktreePath)
	}
	if got.CreatedAt != createdAt {
		t.Fatalf("created_at = %q, want preserved %q", got.CreatedAt, createdAt)
	}
	if got.LastUsedAt != now.Format(time.RFC3339) {
		t.Fatalf("last_used_at = %q, want %q", got.LastUsedAt, now.Format(time.RFC3339))
	}
	if renamedFrom != "g/mono/feat/old" || renamedTo != "g/mono/feat/next" {
		t.Fatalf("tmux rename = %q -> %q", renamedFrom, renamedTo)
	}
}

func TestRecycleAutoGeneratesUniqueAnimalBranch(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	worktreePath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace("/repo", worktreePath),
		{
			Name:         "mono/feat/otter",
			Type:         "worktree",
			Repo:         "mono",
			RepoPath:     "/repo",
			WorktreePath: "/other",
			Branch:       "feat/otter",
			SessionName:  "g/mono/feat/otter",
		},
	}}
	recycleCurrentBranch = func(string) string { return "feat/old" }
	recycleWorktreeClean = func(string) (bool, error) { return true, nil }
	recycleFetchOrigin = func(string) error { return nil }
	recycleResolveRef = func(_, _ string) (string, error) { return "start-oid", nil }
	recycleSwitchBranch = func(_, _, _ string) error { return nil }
	recycleSessionExists = func(string) (bool, error) { return false, nil }
	recycleRenameSession = func(_, _ string) (bool, error) { return false, nil }

	result, err := recycleWorkspace(recycleTestConfig("/repo"), mgr, st, &st.Workspaces[0], "", true)
	if err != nil {
		t.Fatalf("recycleWorkspace() error = %v", err)
	}
	if !regexp.MustCompile(`^feat/\d{2}-\d{2}-[a-z]+-[a-z]+$`).MatchString(result.Workspace.Branch) {
		t.Fatalf("auto branch = %q, want feat/<MM-DD>-<adjective>-<animal>", result.Workspace.Branch)
	}
	if result.Workspace.Branch == "feat/old" || result.Workspace.Branch == "feat/otter" {
		t.Fatalf("auto branch reused existing name %q", result.Workspace.Branch)
	}
}

func TestRecycleJSONMatchesNewJSONShape(t *testing.T) {
	result := &newWorkspaceResult{
		Path: "/worktrees/mono/warm-slot",
		Workspace: state.Workspace{
			Repo:         "mono",
			RepoPath:     "/repo",
			WorktreePath: "/worktrees/mono/warm-slot",
			Branch:       "feat/next",
			CreatedAt:    "2026-07-01T18:06:00Z",
			SessionName:  "g/mono/feat/next",
			LastUsedAt:   "2026-07-30T19:00:00Z",
		},
	}
	var out bytes.Buffer
	if err := printRecycleResult(&out, result, true); err != nil {
		t.Fatalf("printRecycleResult() error = %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	wantData, err := json.Marshal(newWorkspaceJSON(result.Workspace))
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(wantData, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("field count = %d, want %d: %v", len(got), len(want), got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s = %q, want %q", key, got[key], value)
		}
	}
}

func TestRecycleCommandDocumentsSafetyFlags(t *testing.T) {
	for _, name := range []string{"force", "json"} {
		if recycleCmd.Flags().Lookup(name) == nil {
			t.Fatalf("recycle command missing --%s", name)
		}
	}
	for _, want := range []string{"clean", "origin/<default>", "without running prepare or setup hooks"} {
		if !strings.Contains(recycleCmd.Long, want) {
			t.Fatalf("recycle help missing %q", want)
		}
	}
}

func TestRecycleRefusesExistingDestinationTmuxSessionBeforeSwitch(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	worktreePath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace("/repo", worktreePath),
	}}
	recycleCurrentBranch = func(string) string { return "feat/old" }
	recycleWorktreeClean = func(string) (bool, error) { return true, nil }
	recycleFetchOrigin = func(string) error { return nil }
	recycleSessionExists = func(name string) (bool, error) {
		if name != "g/mono/feat/next" {
			t.Fatalf("destination session check = %q", name)
		}
		return true, nil
	}
	switched := false
	recycleSwitchBranch = func(_, _, _ string) error {
		switched = true
		return nil
	}

	_, err = recycleWorkspace(recycleTestConfig("/repo"), mgr, st, &st.Workspaces[0], "feat/next", true)
	if err == nil || !strings.Contains(err.Error(), `tmux session "g/mono/feat/next" already exists`) {
		t.Fatalf("recycleWorkspace() error = %v, want destination session collision", err)
	}
	if switched {
		t.Fatal("git branch switched despite destination tmux session collision")
	}
}

func TestRenameTmuxSessionUsesExactTargets(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_TEST_LOG\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_LOG", logPath)

	renamed, err := renameTmuxSessionIfLive("g/mono/feat/foo", "g/mono/feat/bar")
	if err != nil {
		t.Fatalf("renameTmuxSessionIfLive() error = %v", err)
	}
	if !renamed {
		t.Fatal("renameTmuxSessionIfLive() renamed = false, want true")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"has-session -t =g/mono/feat/foo",
		"rename-session -t =g/mono/feat/foo g/mono/feat/bar",
	}
	if len(got) != len(want) {
		t.Fatalf("tmux calls = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tmux call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRecycleRestoresOldBranchWhenStateSaveFails(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	worktreePath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace("/repo", worktreePath),
	}}
	original := st.Workspaces[0]
	if err := mgr.Save(st); err != nil {
		t.Fatal(err)
	}

	recycleCurrentBranch = func(string) string { return "feat/old" }
	recycleWorktreeClean = func(string) (bool, error) { return true, nil }
	recycleFetchOrigin = func(string) error { return nil }
	recycleResolveRef = func(_, _ string) (string, error) { return "start-oid", nil }
	recycleSwitchBranch = func(_, _, _ string) error { return nil }
	recycleSessionExists = func(string) (bool, error) { return false, nil }
	recycleSaveState = func(*state.StateManager, *state.State) error {
		return errors.New("disk full")
	}
	var restoredOld, removedNew, restoredExpected string
	recycleRestoreBranch = func(_ string, oldBranch, newBranch, expectedOID string) error {
		restoredOld, removedNew, restoredExpected = oldBranch, newBranch, expectedOID
		return nil
	}
	var renameCalls []string
	recycleRenameSession = func(oldName, newName string) (bool, error) {
		renameCalls = append(renameCalls, oldName+"->"+newName)
		return true, nil
	}

	_, err = recycleWorkspace(recycleTestConfig("/repo"), mgr, st, &st.Workspaces[0], "feat/next", true)
	if err == nil || !strings.Contains(err.Error(), "saving state: disk full") {
		t.Fatalf("recycleWorkspace() error = %v, want state-save failure", err)
	}
	if st.Workspaces[0] != original {
		t.Fatalf("in-memory workspace = %#v, want restored %#v", st.Workspaces[0], original)
	}
	if restoredOld != "feat/old" || removedNew != "feat/next" {
		t.Fatalf("branch rollback = old %q, new %q", restoredOld, removedNew)
	}
	if restoredExpected != "start-oid" {
		t.Fatalf("rollback expected OID = %q, want start-oid", restoredExpected)
	}
	if got := strings.Join(renameCalls, ","); got != "g/mono/feat/old->g/mono/feat/next,g/mono/feat/next->g/mono/feat/old" {
		t.Fatalf("tmux rename/rollback calls = %q", got)
	}
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workspaces[0] != original {
		t.Fatalf("persisted workspace = %#v, want unchanged %#v", loaded.Workspaces[0], original)
	}
}

func TestGitRestoreRecycledBranchSwitchesBackAndDeletesNewBranch(t *testing.T) {
	repoPath := initRecycleTestRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "slot")
	runRecycleTestGit(t, repoPath, "worktree", "add", "-b", "feat/old", worktreePath)
	expectedOID := recycleTestGitOutput(t, worktreePath, "rev-parse", "main")
	runRecycleTestGit(t, worktreePath, "switch", "-c", "feat/new", "main")

	if err := gitRestoreRecycledBranch(worktreePath, "feat/old", "feat/new", expectedOID); err != nil {
		t.Fatalf("gitRestoreRecycledBranch() error = %v", err)
	}
	if got := recycleTestGitOutput(t, worktreePath, "branch", "--show-current"); got != "feat/old" {
		t.Fatalf("current branch = %q, want feat/old", got)
	}
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/feat/new")
	cmd.Dir = repoPath
	if err := cmd.Run(); err == nil {
		t.Fatal("rolled-back branch feat/new still exists")
	}
}

func TestGitRestoreRecycledBranchPreservesAdvancedNewBranch(t *testing.T) {
	repoPath := initRecycleTestRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "slot")
	runRecycleTestGit(t, repoPath, "worktree", "add", "-b", "feat/old", worktreePath)
	expectedOID := recycleTestGitOutput(t, worktreePath, "rev-parse", "main")
	runRecycleTestGit(t, worktreePath, "switch", "-c", "feat/new", "main")
	writeRecycleTestFile(t, filepath.Join(worktreePath, "hook-work.txt"), "preserve me\n")
	runRecycleTestGit(t, worktreePath, "add", "hook-work.txt")
	runRecycleTestGit(t, worktreePath, "commit", "-m", "hook-created work")
	advancedOID := recycleTestGitOutput(t, worktreePath, "rev-parse", "HEAD")

	err := gitRestoreRecycledBranch(worktreePath, "feat/old", "feat/new", expectedOID)
	if err == nil || !strings.Contains(err.Error(), "deleting unchanged rolled-back branch") {
		t.Fatalf("gitRestoreRecycledBranch() error = %v, want changed-ref refusal", err)
	}
	if got := recycleTestGitOutput(t, worktreePath, "branch", "--show-current"); got != "feat/old" {
		t.Fatalf("current branch = %q, want restored feat/old", got)
	}
	if got := recycleTestGitOutput(t, repoPath, "rev-parse", "feat/new"); got != advancedOID {
		t.Fatalf("advanced branch = %s, want preserved %s", got, advancedOID)
	}
}

func TestRecycleCompensatesWhenSwitchFailsAfterCheckout(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	worktreePath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace("/repo", worktreePath),
	}}
	original := st.Workspaces[0]
	switched := false
	recycleCurrentBranch = func(string) string {
		if switched {
			return "feat/next"
		}
		return "feat/old"
	}
	recycleWorktreeClean = func(string) (bool, error) { return true, nil }
	recycleFetchOrigin = func(string) error { return nil }
	recycleResolveRef = func(_, _ string) (string, error) { return "start-oid", nil }
	recycleSessionExists = func(string) (bool, error) { return false, nil }
	recycleSwitchBranch = func(_, _, _ string) error {
		switched = true
		return errors.New("post-checkout hook failed")
	}
	restored := false
	recycleRestoreBranch = func(_ string, oldBranch, newBranch, expectedOID string) error {
		restored = true
		if oldBranch != "feat/old" || newBranch != "feat/next" || expectedOID != "start-oid" {
			t.Fatalf("rollback args = %q, %q, %q", oldBranch, newBranch, expectedOID)
		}
		return nil
	}

	_, err = recycleWorkspace(recycleTestConfig("/repo"), mgr, st, &st.Workspaces[0], "feat/next", true)
	if err == nil || !strings.Contains(err.Error(), "post-checkout hook failed") {
		t.Fatalf("recycleWorkspace() error = %v, want switch failure", err)
	}
	if !restored {
		t.Fatal("partially successful git switch was not rolled back")
	}
	if st.Workspaces[0] != original {
		t.Fatalf("workspace changed after failed switch: %#v", st.Workspaces[0])
	}
}

func TestRecycleRollsBackGitWhenTmuxRenameFails(t *testing.T) {
	preserveRecycleHooks(t)
	t.Setenv("HOME", t.TempDir())

	worktreePath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: 1, Workspaces: []state.Workspace{
		recycleTestWorkspace("/repo", worktreePath),
	}}
	original := st.Workspaces[0]
	recycleCurrentBranch = func(string) string { return "feat/old" }
	recycleWorktreeClean = func(string) (bool, error) { return true, nil }
	recycleFetchOrigin = func(string) error { return nil }
	recycleResolveRef = func(_, _ string) (string, error) { return "start-oid", nil }
	recycleSessionExists = func(string) (bool, error) { return false, nil }
	recycleSwitchBranch = func(_, _, _ string) error { return nil }
	recycleRenameSession = func(_, _ string) (bool, error) {
		return false, errors.New("tmux server stopped")
	}
	restored := false
	recycleRestoreBranch = func(_ string, oldBranch, newBranch, expectedOID string) error {
		restored = true
		return nil
	}
	saved := false
	recycleSaveState = func(*state.StateManager, *state.State) error {
		saved = true
		return nil
	}

	_, err = recycleWorkspace(recycleTestConfig("/repo"), mgr, st, &st.Workspaces[0], "feat/next", true)
	if err == nil || !strings.Contains(err.Error(), "renaming tmux session: tmux server stopped") {
		t.Fatalf("recycleWorkspace() error = %v, want tmux rename failure", err)
	}
	if !restored {
		t.Fatal("git branch was not rolled back after tmux rename failure")
	}
	if saved {
		t.Fatal("state was saved before failed tmux rename")
	}
	if st.Workspaces[0] != original {
		t.Fatalf("workspace changed after tmux rename failure: %#v", st.Workspaces[0])
	}
}

func preserveRecycleHooks(t *testing.T) {
	t.Helper()
	origNow := recycleNow
	origCurrentBranch := recycleCurrentBranch
	origClean := recycleWorktreeClean
	origMerged := recycleMergedIntoDefault
	origFetch := recycleFetchOrigin
	origResolveRef := recycleResolveRef
	origSwitch := recycleSwitchBranch
	origRestore := recycleRestoreBranch
	origSaveState := recycleSaveState
	origSessionExists := recycleSessionExists
	origRename := recycleRenameSession
	t.Cleanup(func() {
		recycleNow = origNow
		recycleCurrentBranch = origCurrentBranch
		recycleWorktreeClean = origClean
		recycleMergedIntoDefault = origMerged
		recycleFetchOrigin = origFetch
		recycleResolveRef = origResolveRef
		recycleSwitchBranch = origSwitch
		recycleRestoreBranch = origRestore
		recycleSaveState = origSaveState
		recycleSessionExists = origSessionExists
		recycleRenameSession = origRename
	})
}

func recycleTestWorkspace(repoPath, worktreePath string) state.Workspace {
	return state.Workspace{
		Name:         "mono/feat/old",
		Type:         "worktree",
		Repo:         "mono",
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Branch:       "feat/old",
		SessionName:  "g/mono/feat/old",
		CreatedAt:    "2026-07-01T18:06:00Z",
		LastUsedAt:   "2026-07-01T18:07:00Z",
	}
}

func recycleTestConfig(repoPath string) *config.Config {
	return &config.Config{Repos: []config.RepoConfig{{
		Name:          "mono",
		Path:          repoPath,
		Type:          "worktree",
		DefaultBranch: "main",
	}}}
}

func initRecycleTestRepo(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()
	runRecycleTestGit(t, repoPath, "init", "-b", "main")
	configureRecycleTestGitUser(t, repoPath)
	writeRecycleTestFile(t, filepath.Join(repoPath, "README.md"), "base\n")
	runRecycleTestGit(t, repoPath, "add", "README.md")
	runRecycleTestGit(t, repoPath, "commit", "-m", "base")
	return repoPath
}

func configureRecycleTestGitUser(t *testing.T, repoPath string) {
	t.Helper()
	runRecycleTestGit(t, repoPath, "config", "user.email", "test@example.com")
	runRecycleTestGit(t, repoPath, "config", "user.name", "Test User")
}

func runRecycleTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s (%v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
}

func recycleTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s (%v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out))
}

func writeRecycleTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
