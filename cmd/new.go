package cmd

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/names"
	"grove/internal/state"

	"github.com/spf13/cobra"
)

func init() {
	newCmd.Flags().Bool("here", false, "Create a worktree for the current git repo")
	newCmd.Flags().Bool("no-prepare", false, "Skip prepare commands before workspace creation")
	newCmd.Flags().BoolP("manual", "m", false, "Prompt for the branch name instead of auto-generating")
	newCmd.Flags().String("from", "", "Create a new branch from this start point")
	newCmd.Flags().Bool("json", false, "Print workspace metadata as JSON")
	newCmd.Flags().BoolP("tmux", "t", false, "Create and switch to a tmux session for the workspace")
	rootCmd.AddCommand(newCmd)
}

var newCmd = &cobra.Command{
	Use:         "new [name] [branch]",
	Aliases:     []string{"n"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Create a new workspace",
	Long: `Create a new workspace and print its path. With --tmux, create a detached
tmux session and switch the current tmux client to it.

  grove new                 — pick a repo (or type a plain workspace name), then create + cd
  grove new <repo>          — auto-create a fix/<mmdd>-<hhmm>-<animal> branch in repo + cd
  grove new -m              — pick a repo, then prompt for the branch name
  grove new <repo> -m       — prompt for the branch name instead of auto-generating
  grove new <repo> <branch> — create (or check out) a specific branch + cd
  grove new <repo> <branch> --from <base>
                            — create <branch> from <base>
  grove new --here <branch> — create a worktree for the current git repo + cd
  grove new -t <repo> <branch>
                            — create the workspace and open it in tmux
  grove new <name>          — plain workspace (if name doesn't match a repo)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		here, _ := cmd.Flags().GetBool("here")
		noPrepare, _ := cmd.Flags().GetBool("no-prepare")
		manual, _ := cmd.Flags().GetBool("manual")
		from, _ := cmd.Flags().GetString("from")
		jsonOut, _ := cmd.Flags().GetBool("json")
		tmuxMode, _ := cmd.Flags().GetBool("tmux")

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		if here {
			branch, err := newHereBranch(args)
			if err != nil {
				return err
			}
			if err := validateNewFromFlag(from, branch); err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			result, err := createWorktreeHereWithResult(cwd, branch, from, cfg, mgr, st, noPrepare)
			if err != nil {
				return err
			}
			return finishNewResult(result, jsonOut, tmuxMode)
		}

		var name, branch string
		switch len(args) {
		case 0:
			picked, err := pickRepoOrNameFzf(cfg)
			if err != nil {
				return err
			}
			name = picked
		case 1:
			name = args[0]
		default:
			name = args[0]
			branch = args[1]
		}

		if err := validateNewFromFlag(from, branch); err != nil {
			return err
		}

		repo := cfg.FindRepo(name)
		if from != "" && (repo == nil || repo.Type != "worktree") {
			return fmt.Errorf("--from can only be used with worktree repos")
		}
		if repo != nil {
			var result *newWorkspaceResult
			if repo.Type == "plain" {
				result, err = createPlainRepoWithResult(repo, branch, mgr, st)
			} else if repo.Type == "dir" {
				result, err = createDirWorkspaceWithResult(repo, branch, mgr, st, noPrepare)
			} else {
				result, err = createWorktreeWithResult(cfg, repo, branch, from, mgr, st, noPrepare, manual)
			}
			if err != nil {
				return err
			}
			return finishNewResult(result, jsonOut, tmuxMode)
		}
		result, err := createPlainWithResult(name, mgr, st)
		if err != nil {
			return err
		}
		return finishNewResult(result, jsonOut, tmuxMode)
	},
}

type newWorkspaceResult struct {
	Path      string
	Workspace state.Workspace
}

type newWorkspaceJSONOutput struct {
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	Repo         string `json:"repo"`
	RepoPath     string `json:"repo_path"`
	CreatedAt    string `json:"created_at"`
}

func newWorkspaceJSON(ws state.Workspace) newWorkspaceJSONOutput {
	return newWorkspaceJSONOutput{
		WorktreePath: ws.WorktreePath,
		Branch:       ws.Branch,
		Repo:         ws.Repo,
		RepoPath:     ws.RepoPath,
		CreatedAt:    ws.CreatedAt,
	}
}

func printNewResult(result *newWorkspaceResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(newWorkspaceJSON(result.Workspace))
	}
	fmt.Println(result.Path)
	return nil
}

var newTmuxCommand = func(args ...string) ([]byte, error) {
	return exec.Command("tmux", args...).CombinedOutput()
}

func finishNewResult(result *newWorkspaceResult, jsonOut, tmuxMode bool) error {
	if !tmuxMode {
		return printNewResult(result, jsonOut)
	}
	if err := openNewTmuxSession(result); err != nil {
		return err
	}
	if jsonOut {
		return printNewResult(result, true)
	}
	return nil
}

func openNewTmuxSession(result *newWorkspaceResult) error {
	sessionName := result.Workspace.SessionName
	if err := runNewTmuxCommand("new-session", "-d", "-s", sessionName, "-c", result.Path); err != nil {
		return fmt.Errorf("workspace created at %s, but creating tmux session %q failed: %w", result.Path, sessionName, err)
	}
	if os.Getenv("TMUX") == "" {
		return nil
	}
	if err := runNewTmuxCommand("switch-client", "-t", "="+sessionName); err != nil {
		return fmt.Errorf("tmux session %q created, but switching client failed: %w", sessionName, err)
	}
	return nil
}

func runNewTmuxCommand(args ...string) error {
	out, err := newTmuxCommand(args...)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		return fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("tmux %s: %s: %w", strings.Join(args, " "), detail, err)
}

func validateNewFromFlag(from, branch string) error {
	if from != "" && branch == "" {
		return fmt.Errorf("--from requires <repo> <branch>")
	}
	return nil
}

func newHereBranch(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("--here requires exactly one <branch>")
	}
	return args[0], nil
}

func createPlain(name string, mgr *state.StateManager, st *state.State) error {
	_, err := createPlainWithResult(name, mgr, st)
	return err
}

func createPlainWithResult(name string, mgr *state.StateManager, st *state.State) (*newWorkspaceResult, error) {
	sessionName := fmt.Sprintf("gv/%s", name)
	if mgr.FindWorkspace(st, name) != nil {
		return nil, fmt.Errorf("workspace %q already exists", name)
	}

	dir, _ := os.UserHomeDir()
	mgr.AddWorkspace(st, state.Workspace{
		Name:        name,
		Type:        "plain",
		Path:        dir,
		SessionName: sessionName,
	})
	if err := mgr.Save(st); err != nil {
		return nil, err
	}

	return &newWorkspaceResult{Path: dir, Workspace: st.Workspaces[len(st.Workspaces)-1]}, nil
}

func createWorktree(cfg *config.Config, repo *config.RepoConfig, branch, from string, mgr *state.StateManager, st *state.State, noPrepare, manual bool) error {
	_, err := createWorktreeWithResult(cfg, repo, branch, from, mgr, st, noPrepare, manual)
	return err
}

func createWorktreeWithResult(cfg *config.Config, repo *config.RepoConfig, branch, from string, mgr *state.StateManager, st *state.State, noPrepare, manual bool) (*newWorkspaceResult, error) {
	if branch == "" && manual {
		prompted, err := promptNameFzf("branch > ", "Type a branch name or Enter for auto")
		if err != nil {
			return nil, err
		}
		branch = prompted
	}
	if branch == "" {
		branch = names.GenerateBranch(existingWorktreeNames(st, repo.Name))
	}

	workspaceName := fmt.Sprintf("%s/%s", repo.Name, branch)
	sessionName := "gv/" + workspaceName
	if mgr.FindWorkspace(st, workspaceName) != nil {
		return nil, fmt.Errorf("workspace %q already exists", repo.Name+"/"+branch)
	}

	worktreePath := collisionSafeWorktreePath(cfg, repo, branch, st)

	if !noPrepare {
		if err := runPrepareCommands(repo); err != nil {
			return nil, err
		}
	}

	if err := git.EnsureGitignore(repo.Path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}

	if err := git.AddWorktreeFrom(repo.Path, worktreePath, branch, from); err != nil {
		return nil, fmt.Errorf("creating worktree: %w", err)
	}

	setupDir := pathWithWorkdir(worktreePath, repo.Workdir)

	for _, setupCmd := range repo.Setup {
		fmt.Fprintf(os.Stderr, "Running: %s\n", setupCmd)
		c := exec.Command("sh", "-c", setupCmd)
		c.Dir = setupDir
		c.Stdout = os.Stderr
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup command failed: %v\n", err)
		}
	}

	ws := state.Workspace{
		Name:         workspaceName,
		Type:         "worktree",
		Repo:         repo.Name,
		RepoPath:     repo.Path,
		WorktreePath: worktreePath,
		Branch:       branch,
		SessionName:  sessionName,
	}
	if repo.Workdir != "" {
		ws.Path = setupDir
	}
	mgr.AddWorkspace(st, ws)
	if err := mgr.Save(st); err != nil {
		return nil, err
	}

	return &newWorkspaceResult{Path: setupDir, Workspace: st.Workspaces[len(st.Workspaces)-1]}, nil
}

func createWorktreeHere(cwd, branch, from string, cfg *config.Config, mgr *state.StateManager, st *state.State, noPrepare bool) error {
	_, err := createWorktreeHereWithResult(cwd, branch, from, cfg, mgr, st, noPrepare)
	return err
}

func createWorktreeHereWithResult(cwd, branch, from string, cfg *config.Config, mgr *state.StateManager, st *state.State, noPrepare bool) (*newWorkspaceResult, error) {
	repoRoot := git.RepoRoot(cwd)
	if repoRoot == "" {
		return nil, fmt.Errorf("not inside a git repository")
	}

	if repo := findRepoByPath(cfg, repoRoot); repo != nil {
		if repo.Type != "" && repo.Type != "worktree" {
			return nil, fmt.Errorf("repo %s is registered as type %s, not worktree", repo.Name, repo.Type)
		}
		return createWorktreeWithResult(cfg, repo, branch, from, mgr, st, noPrepare, false)
	}

	if repoName := repoNameForManagedWorktreeRoot(st, repoRoot); repoName != "" {
		if cfg == nil {
			return nil, fmt.Errorf("repo %s is tracked in state but config is unavailable", repoName)
		}
		repo := cfg.FindRepo(repoName)
		if repo == nil {
			return nil, fmt.Errorf("repo %s is tracked in state but missing from config", repoName)
		}
		if repo.Type != "" && repo.Type != "worktree" {
			return nil, fmt.Errorf("repo %s is registered as type %s, not worktree", repo.Name, repo.Type)
		}
		return createWorktreeWithResult(cfg, repo, branch, from, mgr, st, noPrepare, false)
	}

	defaultBranch := git.DefaultBranch(repoRoot)
	if defaultBranch == "" {
		return nil, fmt.Errorf("could not infer default branch; run grove init --default-branch first")
	}
	newRepo := config.NewWorktreeRepo(repoRoot, filepath.Base(repoRoot), defaultBranch)
	configPath, err := config.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	if err := config.AddRepoToFile(configPath, newRepo); err != nil {
		return nil, err
	}
	refreshed, err := config.Load()
	if err != nil {
		return nil, err
	}
	repo := findRepoByPath(refreshed, repoRoot)
	if repo == nil {
		return nil, fmt.Errorf("repo %s was added to config but could not be loaded", repoRoot)
	}
	if repo.Type != "" && repo.Type != "worktree" {
		return nil, fmt.Errorf("repo %s is registered as type %s, not worktree", repo.Name, repo.Type)
	}

	return createWorktreeWithResult(refreshed, repo, branch, from, mgr, st, noPrepare, false)
}

func repoNameForManagedWorktreeRoot(st *state.State, repoRoot string) string {
	if st == nil {
		return ""
	}
	target := cleanAbsPath(repoRoot)
	for _, ws := range st.Workspaces {
		if ws.Repo == "" || ws.WorktreePath == "" {
			continue
		}
		if cleanAbsPath(ws.WorktreePath) == target {
			return ws.Repo
		}
	}
	return ""
}

func findRepoByPath(cfg *config.Config, repoPath string) *config.RepoConfig {
	if cfg == nil {
		return nil
	}
	target := cleanAbsPath(repoPath)
	for i := range cfg.Repos {
		if cleanAbsPath(cfg.Repos[i].Path) == target {
			return &cfg.Repos[i]
		}
	}
	return nil
}

func cleanAbsPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if realPath, err := filepath.EvalSymlinks(path); err == nil {
		path = realPath
		return filepath.Clean(path)
	}

	current := path
	suffix := ""
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Clean(filepath.Join(realParent, suffix))
		}
		current = parent
	}

	return filepath.Clean(path)
}

func worktreePathFor(cfg *config.Config, repo *config.RepoConfig, branch string) string {
	if root := effectiveWorktreeRoot(cfg, repo); root != "" {
		return filepath.Join(root, dashedBranchDir(branch))
	}
	return filepath.Join(repo.Path, ".grove", "worktrees", branch)
}

func collisionSafeWorktreePath(cfg *config.Config, repo *config.RepoConfig, branch string, st *state.State) string {
	root := effectiveWorktreeRoot(cfg, repo)
	path := worktreePathFor(cfg, repo, branch)
	if root == "" || !worktreePathCollides(path, branch, st) {
		return path
	}
	return path + "-" + branchPathHash(branch)
}

func effectiveWorktreeRoot(cfg *config.Config, repo *config.RepoConfig) string {
	if cfg != nil {
		return cfg.EffectiveWorktreeRoot(repo)
	}
	if repo != nil {
		return repo.WorktreeRoot
	}
	return ""
}

func worktreePathCollides(path, branch string, st *state.State) bool {
	if st != nil {
		for _, ws := range st.Workspaces {
			if ws.WorktreePath == "" {
				continue
			}
			if cleanAbsPath(ws.WorktreePath) == cleanAbsPath(path) && ws.Branch != branch {
				return true
			}
		}
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

func dashedBranchDir(branch string) string {
	parts := strings.FieldsFunc(branch, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return strings.Join(parts, "-")
}

func branchPathHash(branch string) string {
	sum := sha1.Sum([]byte(branch))
	return hex.EncodeToString(sum[:])[:8]
}

func createDirWorkspace(repo *config.RepoConfig, name string, mgr *state.StateManager, st *state.State, noPrepare bool) error {
	_, err := createDirWorkspaceWithResult(repo, name, mgr, st, noPrepare)
	return err
}

func createDirWorkspaceWithResult(repo *config.RepoConfig, name string, mgr *state.StateManager, st *state.State, noPrepare bool) (*newWorkspaceResult, error) {
	if name == "" {
		existing := existingDirNames(st, repo.Name)
		prompted, err := promptNameFzf("name > ", "Type a name or enter for random")
		if err != nil {
			return nil, err
		}
		if prompted != "" {
			name = prompted
		} else {
			name = names.Generate(existing)
		}
	}

	workspaceName := fmt.Sprintf("%s/%s", repo.Name, name)
	sessionName := "gv/" + workspaceName
	if mgr.FindWorkspace(st, workspaceName) != nil {
		return nil, fmt.Errorf("workspace %q already exists", repo.Name+"/"+name)
	}

	if !noPrepare {
		if err := runPrepareCommands(repo); err != nil {
			return nil, err
		}
	}

	startDir := repo.Path
	if repo.Workdir != "" {
		startDir = filepath.Join(repo.Path, repo.Workdir)
	}

	mgr.AddWorkspace(st, state.Workspace{
		Name:        workspaceName,
		Type:        "dir",
		Repo:        repo.Name,
		RepoPath:    repo.Path,
		Path:        startDir,
		SessionName: sessionName,
	})
	if err := mgr.Save(st); err != nil {
		return nil, err
	}

	return &newWorkspaceResult{Path: startDir, Workspace: st.Workspaces[len(st.Workspaces)-1]}, nil
}

// runPrepareCommands runs repo-scoped shell commands before Grove records a workspace.
func runPrepareCommands(repo *config.RepoConfig) error {
	for _, prepCmd := range repo.Prepare {
		fmt.Fprintf(os.Stderr, "Preparing: %s\n", prepCmd)
		c := exec.Command("sh", "-c", prepCmd)
		c.Dir = repo.Path
		c.Stdout = os.Stderr
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("prepare command %q failed: %w", prepCmd, err)
		}
	}
	return nil
}

func createPlainRepo(repo *config.RepoConfig, name string, mgr *state.StateManager, st *state.State) error {
	_, err := createPlainRepoWithResult(repo, name, mgr, st)
	return err
}

func createPlainRepoWithResult(repo *config.RepoConfig, name string, mgr *state.StateManager, st *state.State) (*newWorkspaceResult, error) {
	if name == "" {
		existing := existingDirNames(st, repo.Name)
		prompted, err := promptNameFzf("name > ", "Type a name or enter for random")
		if err != nil {
			return nil, err
		}
		if prompted != "" {
			name = prompted
		} else {
			name = names.Generate(existing)
		}
	}

	workspaceName := fmt.Sprintf("%s/%s", repo.Name, name)
	sessionName := "gv/" + workspaceName
	if mgr.FindWorkspace(st, workspaceName) != nil {
		return nil, fmt.Errorf("workspace %q already exists", repo.Name+"/"+name)
	}

	home, _ := os.UserHomeDir()
	mgr.AddWorkspace(st, state.Workspace{
		Name:        workspaceName,
		Type:        "plain",
		Repo:        repo.Name,
		Path:        home,
		SessionName: sessionName,
	})
	if err := mgr.Save(st); err != nil {
		return nil, err
	}

	return &newWorkspaceResult{Path: home, Workspace: st.Workspaces[len(st.Workspaces)-1]}, nil
}

func existingDirNames(st *state.State, repoName string) []string {
	var result []string
	for _, ws := range st.Workspaces {
		if ws.Type == "dir" && ws.Repo == repoName {
			parts := strings.SplitN(ws.Name, "/", 2)
			if len(parts) == 2 {
				result = append(result, parts[1])
			}
		}
	}
	return result
}

func pickRepoOrNameFzf(cfg *config.Config) (string, error) {
	var repoNames []string
	for _, r := range cfg.Repos {
		repoNames = append(repoNames, r.Name)
	}

	fzfCmd := exec.Command("fzf",
		"--prompt", "repo or name > ",
		"--header", "Pick a repo or type a session name",
		"--print-query",
		"--height", "100%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(repoNames, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil && len(out) == 0 {
		return "", ErrCancelled
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := ""
	if len(lines) >= 2 && lines[1] != "" {
		result = lines[1]
	} else if len(lines) >= 1 {
		result = lines[0]
	}
	result = strings.TrimSpace(result)

	if result == "" {
		return "", ErrCancelled
	}
	return result, nil
}

const autoGenerateLabel = "(auto-generate)"

// promptNameFzf shows an fzf prompt with a single "(auto-generate)" entry and
// returns the typed name, or "" when the user picks auto-generate or enters nothing.
func promptNameFzf(prompt, header string) (string, error) {
	fzfCmd := exec.Command("fzf",
		"--prompt", prompt,
		"--header", header,
		"--print-query",
		"--height", "100%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(autoGenerateLabel)
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil && len(out) == 0 {
		return "", ErrCancelled
	}

	outputLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := ""
	if len(outputLines) >= 2 && outputLines[1] != "" {
		result = outputLines[1]
	} else if len(outputLines) >= 1 {
		result = outputLines[0]
	}
	result = strings.TrimSpace(result)

	if result == autoGenerateLabel || result == "" {
		return "", nil
	}
	return result, nil
}

func existingWorktreeNames(st *state.State, repoName string) []string {
	var result []string
	for _, ws := range st.Workspaces {
		if ws.Type == "worktree" && ws.Repo == repoName {
			result = append(result, ws.Branch)
		}
	}
	return result
}
