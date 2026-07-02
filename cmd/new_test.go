package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
	"grove/internal/state"
)

func TestNewCommandExposesFromFlag(t *testing.T) {
	flag := newCmd.Flags().Lookup("from")
	if flag == nil {
		t.Fatal("new command missing --from flag")
	}
	if got, want := flag.Usage, "Create a new branch from this start point"; got != want {
		t.Fatalf("--from usage = %q, want %q", got, want)
	}
}

func TestValidateNewFromFlagRequiresBranch(t *testing.T) {
	if err := validateNewFromFlag("feat/base", ""); err == nil {
		t.Fatal("validateNewFromFlag() error = nil, want missing branch error")
	}
}

func TestValidateNewFromFlagAllowsBranch(t *testing.T) {
	if err := validateNewFromFlag("feat/base", "agent"); err != nil {
		t.Fatalf("validateNewFromFlag() error = %v", err)
	}
}

func TestNewHereBranchRequiresExactlyOneBranch(t *testing.T) {
	for _, args := range [][]string{nil, []string{"repo", "branch"}} {
		if _, err := newHereBranch(args); err == nil {
			t.Fatalf("newHereBranch(%#v) error = nil, want validation error", args)
		}
	}
}

func TestNewHereBranchReturnsBranch(t *testing.T) {
	branch, err := newHereBranch([]string{"feat/here"})
	if err != nil {
		t.Fatalf("newHereBranch() error = %v", err)
	}
	if branch != "feat/here" {
		t.Fatalf("branch = %q, want feat/here", branch)
	}
}

func TestCreateWorktreeUsesFromStartPoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	runNewTestGit(t, repoPath, "checkout", "-b", "feat/base")
	writeNewTestCommit(t, repoPath, "feature.txt", "feature")
	baseHead := newTestGitOutput(t, repoPath, "rev-parse", "HEAD")
	runNewTestGit(t, repoPath, "checkout", "main")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "mono",
		Path: repoPath,
		Type: "worktree",
	}

	if err := createWorktree(&config.Config{}, repo, "agent", "feat/base", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree() error = %v", err)
	}

	worktreePath := filepath.Join(repoPath, ".grove", "worktrees", "agent")
	if got := newTestGitOutput(t, worktreePath, "rev-parse", "HEAD"); got != baseHead {
		t.Fatalf("worktree HEAD = %s, want %s", got, baseHead)
	}
	if got := st.Workspaces[0].WorktreePath; got != worktreePath {
		t.Fatalf("workspace WorktreePath = %q, want %q", got, worktreePath)
	}
}

func TestCreateWorktreeWithResultReturnsJSONMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "mono",
		Path: repoPath,
		Type: "worktree",
	}

	result, err := createWorktreeWithResult(&config.Config{}, repo, "feat/json", "", mgr, st, true, false)
	if err != nil {
		t.Fatalf("createWorktreeWithResult() error = %v", err)
	}

	worktreePath := filepath.Join(repoPath, ".grove", "worktrees", "feat/json")
	if got := result.Workspace.WorktreePath; got != worktreePath {
		t.Fatalf("WorktreePath = %q, want %q", got, worktreePath)
	}
	if got := result.Workspace.Branch; got != "feat/json" {
		t.Fatalf("Branch = %q, want feat/json", got)
	}
	if got := result.Workspace.Repo; got != "mono" {
		t.Fatalf("Repo = %q, want mono", got)
	}
	if got := result.Workspace.RepoPath; got != repoPath {
		t.Fatalf("RepoPath = %q, want %q", got, repoPath)
	}
	if result.Workspace.CreatedAt == "" {
		t.Fatal("CreatedAt is empty")
	}
}

func TestNewWorkspaceJSONContainsRequestedFields(t *testing.T) {
	ws := state.Workspace{
		Name:         "mono/feat-json",
		Repo:         "mono",
		RepoPath:     "/repo",
		WorktreePath: "/worktrees/mono/feat-json",
		Branch:       "feat/json",
		SessionName:  "g/mono/feat/json",
		CreatedAt:    "2026-07-01T18:06:00Z",
		LastUsedAt:   "2026-07-01T18:07:00Z",
	}

	data, err := json.Marshal(newWorkspaceJSON(ws))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	want := map[string]string{
		"worktree_path": "/worktrees/mono/feat-json",
		"branch":        "feat/json",
		"repo":          "mono",
		"repo_path":     "/repo",
		"created_at":    "2026-07-01T18:06:00Z",
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

func TestCreateWorktreeUsesGlobalRootAndDashedBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	root := t.TempDir()

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "mono",
		Path: repoPath,
		Type: "worktree",
	}
	cfg := &config.Config{WorktreeRoot: root}

	if err := createWorktree(cfg, repo, "feat/build-payments", "", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree() error = %v", err)
	}

	worktreePath := filepath.Join(root, "mono", "feat-build-payments")
	if got := newTestGitOutput(t, worktreePath, "branch", "--show-current"); got != "feat/build-payments" {
		t.Fatalf("worktree branch = %q, want feat/build-payments", got)
	}
	if got := st.Workspaces[0].WorktreePath; got != worktreePath {
		t.Fatalf("workspace WorktreePath = %q, want %q", got, worktreePath)
	}
}

func TestCreateWorktreeUsesRepoRootOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	root := t.TempDir()
	overrideRoot := t.TempDir()

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name:         "mono",
		Path:         repoPath,
		Type:         "worktree",
		WorktreeRoot: overrideRoot,
	}
	cfg := &config.Config{WorktreeRoot: root}

	if err := createWorktree(cfg, repo, "feat/build-payments", "", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree() error = %v", err)
	}

	worktreePath := filepath.Join(overrideRoot, "feat-build-payments")
	if got := st.Workspaces[0].WorktreePath; got != worktreePath {
		t.Fatalf("workspace WorktreePath = %q, want %q", got, worktreePath)
	}
}

func TestCreateWorktreeStoresConfiguredWorkdirAsStartPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name:    "mono",
		Path:    repoPath,
		Type:    "worktree",
		Workdir: "packages/app",
	}

	if err := createWorktree(&config.Config{}, repo, "agent", "", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree() error = %v", err)
	}

	worktreePath := filepath.Join(repoPath, ".grove", "worktrees", "agent")
	want := filepath.Join(worktreePath, "packages/app")
	if got := st.Workspaces[0].Path; got != want {
		t.Fatalf("workspace Path = %q, want %q", got, want)
	}
}

func TestCreateWorktreeHereAddsUnregisteredRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	cfg := &config.Config{}

	if err := createWorktreeHere(repoPath, "feat/here-thing", "", cfg, mgr, st, true); err != nil {
		t.Fatalf("createWorktreeHere() error = %v", err)
	}

	repoName := filepath.Base(repoPath)
	worktreePath := filepath.Join(home, "worktrees", repoName, "feat-here-thing")
	if got := newTestGitOutput(t, worktreePath, "branch", "--show-current"); got != "feat/here-thing" {
		t.Fatalf("worktree branch = %q, want feat/here-thing", got)
	}
	if got := st.Workspaces[0].WorktreePath; got != worktreePath {
		t.Fatalf("workspace WorktreePath = %q, want %q", got, worktreePath)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if got, want := len(loaded.Repos), 1; got != want {
		t.Fatalf("config repo count = %d, want %d", got, want)
	}
	if got := loaded.Repos[0].Name; got != repoName {
		t.Fatalf("config repo name = %q, want %q", got, repoName)
	}
	if got, want := loaded.WorktreeRoot, filepath.Join(home, "worktrees"); got != want {
		t.Fatalf("config WorktreeRoot = %q, want %q", got, want)
	}
}

func TestCreateWorktreeHereReusesRegisteredRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	root := t.TempDir()

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	cfg := &config.Config{Repos: []config.RepoConfig{{
		Name:         "custom",
		Path:         repoPath,
		Type:         "worktree",
		WorktreeRoot: root,
	}}}

	if err := createWorktreeHere(repoPath, "fix/sort-order", "", cfg, mgr, st, true); err != nil {
		t.Fatalf("createWorktreeHere() error = %v", err)
	}

	worktreePath := filepath.Join(root, "fix-sort-order")
	if got := st.Workspaces[0].Repo; got != "custom" {
		t.Fatalf("workspace Repo = %q, want custom", got)
	}
	if got := st.Workspaces[0].WorktreePath; got != worktreePath {
		t.Fatalf("workspace WorktreePath = %q, want %q", got, worktreePath)
	}
}

func TestCreateWorktreeHereFromManagedWorktreeUsesRegisteredRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	root := t.TempDir()

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	cfg := &config.Config{Repos: []config.RepoConfig{{
		Name:         "custom",
		Path:         repoPath,
		Type:         "worktree",
		WorktreeRoot: root,
	}}}

	if err := createWorktree(cfg, &cfg.Repos[0], "feat/source", "", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree(source) error = %v", err)
	}
	cwd := filepath.Join(st.Workspaces[0].WorktreePath, "packages", "app")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("creating cwd: %v", err)
	}

	if err := createWorktreeHere(cwd, "feat/child", "", cfg, mgr, st, true); err != nil {
		t.Fatalf("createWorktreeHere() error = %v", err)
	}

	child := st.Workspaces[1]
	if got := child.Repo; got != "custom" {
		t.Fatalf("child Repo = %q, want custom", got)
	}
	if got, want := child.WorktreePath, filepath.Join(root, "feat-child"); got != want {
		t.Fatalf("child WorktreePath = %q, want %q", got, want)
	}
	configPath, err := config.DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error = %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config file stat = %v, want not exist", err)
	}
}

func TestCreateWorktreeHereUsesNestedGitRepoOverRegisteredParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	parentPath := initNewTestRepo(t)
	writeNewTestCommit(t, parentPath, "base.txt", "base")
	nestedPath := filepath.Join(parentPath, "deps", "nested")
	if err := os.MkdirAll(nestedPath, 0755); err != nil {
		t.Fatalf("creating nested repo dir: %v", err)
	}
	runNewTestGit(t, nestedPath, "init", "-b", "main")
	runNewTestGit(t, nestedPath, "config", "user.name", "Grove Test")
	runNewTestGit(t, nestedPath, "config", "user.email", "grove@example.test")
	writeNewTestCommit(t, nestedPath, "nested.txt", "nested")

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	parentRoot := t.TempDir()
	cfg := &config.Config{Repos: []config.RepoConfig{{
		Name:         "parent",
		Path:         parentPath,
		Type:         "worktree",
		WorktreeRoot: parentRoot,
	}}}

	if err := createWorktreeHere(nestedPath, "feat/nested", "", cfg, mgr, st, true); err != nil {
		t.Fatalf("createWorktreeHere() error = %v", err)
	}

	if got, want := st.Workspaces[0].Repo, filepath.Base(nestedPath); got != want {
		t.Fatalf("workspace Repo = %q, want %q", got, want)
	}
	wantWorktreePath := filepath.Join(home, "worktrees", filepath.Base(nestedPath), "feat-nested")
	if got := st.Workspaces[0].WorktreePath; got != wantWorktreePath {
		t.Fatalf("workspace WorktreePath = %q, want %q", got, wantWorktreePath)
	}
	if got := newTestGitOutput(t, wantWorktreePath, "branch", "--show-current"); got != "feat/nested" {
		t.Fatalf("worktree branch = %q, want feat/nested", got)
	}
}

func TestCreateWorktreeUsesHashSuffixForDashedBranchCollision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := initNewTestRepo(t)
	writeNewTestCommit(t, repoPath, "base.txt", "base")
	root := t.TempDir()

	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "mono",
		Path: repoPath,
		Type: "worktree",
	}

	cfg := &config.Config{WorktreeRoot: root}

	if err := createWorktree(cfg, repo, "feat/foo", "", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree(feat/foo) error = %v", err)
	}
	if err := createWorktree(cfg, repo, "feat-foo", "", mgr, st, true, false); err != nil {
		t.Fatalf("createWorktree(feat-foo) error = %v", err)
	}

	if got, want := st.Workspaces[0].WorktreePath, filepath.Join(root, "mono", "feat-foo"); got != want {
		t.Fatalf("first WorktreePath = %q, want %q", got, want)
	}
	wantSecond := filepath.Join(root, "mono", "feat-foo-"+branchPathHash("feat-foo"))
	if got := st.Workspaces[1].WorktreePath; got != wantSecond {
		t.Fatalf("second WorktreePath = %q, want %q", got, wantSecond)
	}
}

func TestCreateDirWorkspaceRunsPrepareCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "main",
		Path: repoPath,
		Type: "dir",
		Prepare: []string{
			"printf prepared > prepared.txt",
		},
	}

	if err := createDirWorkspace(repo, "agent", mgr, st, false); err != nil {
		t.Fatalf("createDirWorkspace() error = %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(repoPath, "prepared.txt")); err != nil || string(got) != "prepared" {
		t.Fatalf("prepared.txt = %q, %v; want prepared", got, err)
	}
}

func TestCreateDirWorkspaceSkipsPrepareWhenRequested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := t.TempDir()
	mgr, err := state.NewManager()
	if err != nil {
		t.Fatalf("state.NewManager() error = %v", err)
	}
	st := &state.State{Version: 1}
	repo := &config.RepoConfig{
		Name: "main",
		Path: repoPath,
		Type: "dir",
		Prepare: []string{
			"printf prepared > prepared.txt",
		},
	}

	if err := createDirWorkspace(repo, "agent", mgr, st, true); err != nil {
		t.Fatalf("createDirWorkspace() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoPath, "prepared.txt")); !os.IsNotExist(err) {
		t.Fatalf("prepared.txt stat error = %v, want not exist", err)
	}
}

func initNewTestRepo(t *testing.T) string {
	t.Helper()

	repoPath := t.TempDir()
	runNewTestGit(t, repoPath, "init", "-b", "main")
	runNewTestGit(t, repoPath, "config", "user.name", "Grove Test")
	runNewTestGit(t, repoPath, "config", "user.email", "grove@example.test")
	return repoPath
}

func writeNewTestCommit(t *testing.T, repoPath, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repoPath, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	runNewTestGit(t, repoPath, "add", name)
	runNewTestGit(t, repoPath, "commit", "-m", name)
}

func newTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s (%v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out))
}

func runNewTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = newTestGitOutput(t, dir, args...)
}
