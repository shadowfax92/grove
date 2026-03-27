package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/names"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	newCmd.Flags().Bool("no-switch", false, "Don't switch to the new session after creation")
	newCmd.Flags().Bool("no-prepare", false, "Skip prepare commands before worktree creation")
	newCmd.Flags().Bool("cd", false, "Create workspace, print path (no tmux session)")
	rootCmd.AddCommand(newCmd)
}

var newCmd = &cobra.Command{
	Use:         "new [name] [branch]",
	Aliases:     []string{"n"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Create a new workspace",
	Long: `Create a new workspace and switch to it.

  grove new              — pick repo or type session name via fzf
  grove new <repo>       — pick or auto-generate branch in repo
  grove new <repo> <br>  — specific branch in repo
  grove new <name>       — plain session (if name doesn't match a repo)
  grove new --cd         — create workspace, print path: cd (gv n --cd / gv nd)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		noSwitch, _ := cmd.Flags().GetBool("no-switch")
		noPrepare, _ := cmd.Flags().GetBool("no-prepare")
		dirOnly, _ := cmd.Flags().GetBool("cd")

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

		repo := cfg.FindRepo(name)
		if repo != nil {
			if repo.Type == "plain" {
				return createPlainRepo(repo, branch, mgr, st, noSwitch, dirOnly)
			}
			if repo.Type == "dir" {
				return createDirWorkspace(repo, branch, mgr, st, noSwitch, dirOnly)
			}
			return createWorktree(repo, branch, cfg, mgr, st, noSwitch, noPrepare, dirOnly)
		}
		return createPlain(name, mgr, st, noSwitch, dirOnly)
	},
}

func createPlain(name string, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	sessionName := fmt.Sprintf("g/%s", name)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", name)
	}

	dir, _ := os.UserHomeDir()

	ws := state.Workspace{
		Name:        name,
		Type:        "plain",
		Path:        dir,
		SessionName: sessionName,
	}
	mgr.AddWorkspace(st, ws)
	if err := mgr.Save(st); err != nil {
		return err
	}

	if dirOnly {
		fmt.Println(dir)
		return nil
	}

	if err := tmux.NewSession(sessionName, dir); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	fmt.Printf("Created workspace %q\n", name)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func createWorktree(repo *config.RepoConfig, branch string, _ *config.Config, mgr *state.StateManager, st *state.State, noSwitch, noPrepare, dirOnly bool) error {
	if branch == "" {
		existing := existingWorktreeNames(st, repo.Name)

		branches, _ := git.ListRecentBranches(repo.Path, 7)
		usedSet := make(map[string]bool)
		for _, e := range existing {
			usedSet[e] = true
		}
		var available []string
		for _, b := range branches {
			if !usedSet[b] {
				available = append(available, b)
			}
		}

		prompted, selected, err := promptNameFzf("branch > ", "Select branch, type new name, or enter for random", available)
		if err != nil {
			return err
		}
		if prompted != "" {
			if !selected && (git.LocalBranchExists(repo.Path, prompted) || git.RemoteBranchExists(repo.Path, prompted)) {
				return fmt.Errorf("branch %q already exists — select it from the list to use it", prompted)
			}
			branch = prompted
		} else {
			branch = names.Generate(existing)
		}
	}

	sessionName := fmt.Sprintf("g/%s/%s", repo.Name, branch)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", repo.Name+"/"+branch)
	}

	worktreePath := filepath.Join(repo.Path, ".grove", "worktrees", branch)

	if !noPrepare {
		statusOut, childOut := commandWriters(dirOnly)
		for _, prepCmd := range repo.Prepare {
			fmt.Fprintf(statusOut, "Preparing: %s\n", prepCmd)
			c := exec.Command("sh", "-c", prepCmd)
			c.Dir = repo.Path
			c.Stdout = childOut
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("prepare command %q failed: %w", prepCmd, err)
			}
		}
	}

	if err := git.EnsureGitignore(repo.Path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}

	if err := git.AddWorktree(repo.Path, worktreePath, branch); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	setupDir := worktreePath
	if repo.Workdir != "" {
		setupDir = filepath.Join(worktreePath, repo.Workdir)
	}

	statusOut, childOut := commandWriters(dirOnly)
	for _, setupCmd := range repo.Setup {
		fmt.Fprintf(statusOut, "Running: %s\n", setupCmd)
		c := exec.Command("sh", "-c", setupCmd)
		c.Dir = setupDir
		c.Stdout = childOut
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup command failed: %v\n", err)
		}
	}

	ws := state.Workspace{
		Name:         fmt.Sprintf("%s/%s", repo.Name, branch),
		Type:         "worktree",
		Repo:         repo.Name,
		RepoPath:     repo.Path,
		WorktreePath: worktreePath,
		Branch:       branch,
		SessionName:  sessionName,
	}
	mgr.AddWorkspace(st, ws)
	if err := mgr.Save(st); err != nil {
		return err
	}

	if dirOnly {
		fmt.Println(setupDir)
		return nil
	}

	if err := tmux.CreateSessionWithLayout(sessionName, setupDir, repo.Layout); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	fmt.Printf("Created worktree workspace %s/%s\n", repo.Name, branch)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func createDirWorkspace(repo *config.RepoConfig, name string, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	if name == "" {
		existing := existingDirNames(st, repo.Name)
		prompted, _, err := promptNameFzf("name > ", "Type a name or enter for random", nil)
		if err != nil {
			return err
		}
		if prompted != "" {
			name = prompted
		} else {
			name = names.Generate(existing)
		}
	}

	sessionName := fmt.Sprintf("g/%s/%s", repo.Name, name)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", repo.Name+"/"+name)
	}

	startDir := repo.Path
	if repo.Workdir != "" {
		startDir = filepath.Join(repo.Path, repo.Workdir)
	}

	ws := state.Workspace{
		Name:        fmt.Sprintf("%s/%s", repo.Name, name),
		Type:        "dir",
		Repo:        repo.Name,
		RepoPath:    repo.Path,
		Path:        startDir,
		SessionName: sessionName,
	}
	mgr.AddWorkspace(st, ws)
	if err := mgr.Save(st); err != nil {
		return err
	}

	if dirOnly {
		fmt.Println(startDir)
		return nil
	}

	if err := tmux.CreateSessionWithLayout(sessionName, startDir, repo.Layout); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	fmt.Printf("Created dir workspace %s/%s\n", repo.Name, name)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func createPlainRepo(repo *config.RepoConfig, name string, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	if name == "" {
		existing := existingDirNames(st, repo.Name)
		prompted, _, err := promptNameFzf("name > ", "Type a name or enter for random", nil)
		if err != nil {
			return err
		}
		if prompted != "" {
			name = prompted
		} else {
			name = names.Generate(existing)
		}
	}

	sessionName := fmt.Sprintf("g/%s/%s", repo.Name, name)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", repo.Name+"/"+name)
	}

	home, _ := os.UserHomeDir()

	ws := state.Workspace{
		Name:        fmt.Sprintf("%s/%s", repo.Name, name),
		Type:        "plain",
		Repo:        repo.Name,
		Path:        home,
		SessionName: sessionName,
	}
	mgr.AddWorkspace(st, ws)
	if err := mgr.Save(st); err != nil {
		return err
	}

	if dirOnly {
		fmt.Println(home)
		return nil
	}

	if err := tmux.NewSession(sessionName, home); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	fmt.Printf("Created plain workspace %s/%s\n", repo.Name, name)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
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

// promptNameFzf returns (name, selectedFromList, error).
func promptNameFzf(prompt, header string, options []string) (string, bool, error) {
	lines := append([]string{autoGenerateLabel}, options...)

	fzfCmd := exec.Command("fzf",
		"--prompt", prompt,
		"--header", header,
		"--print-query",
		"--height", "100%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil && len(out) == 0 {
		return "", false, ErrCancelled
	}

	outputLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	selected := false
	result := ""
	if len(outputLines) >= 2 && outputLines[1] != "" {
		result = outputLines[1]
		selected = true
	} else if len(outputLines) >= 1 {
		result = outputLines[0]
	}
	result = strings.TrimSpace(result)

	if result == autoGenerateLabel || result == "" {
		return "", false, nil
	}
	return result, selected, nil
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

func commandWriters(dirOnly bool) (io.Writer, io.Writer) {
	if dirOnly {
		return os.Stderr, os.Stderr
	}
	return os.Stdout, os.Stdout
}
