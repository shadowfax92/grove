package cmd

import (
	"fmt"
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
	newCmd.Flags().BoolP("plain", "p", false, "Create a plain workspace (not tied to a repo)")
	newCmd.Flags().String("path", "", "Working directory for plain workspace (default: $HOME)")
	newCmd.Flags().Bool("no-switch", false, "Don't switch to the new session after creation")
	newCmd.Flags().Bool("cd", false, "Create workspace, print path (no tmux session)")
	rootCmd.AddCommand(newCmd)
}

var newCmd = &cobra.Command{
	Use:     "new [repo] [branch] | --plain [name]",
	Aliases:     []string{"n"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Create a new workspace",
	Long: `Create a new workspace and switch to it.

  grove new              — pick repo via fzf, pick or auto-generate branch
  grove new <repo>       — pick or auto-generate branch in repo
  grove new <repo> <br>  — specific branch in repo
  grove new -p           — plain session, pick or auto-generate name
  grove new -p <name>    — plain session with given name
  grove new --cd         — create worktree, print path: cd (grove n --cd)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		plain, _ := cmd.Flags().GetBool("plain")
		noSwitch, _ := cmd.Flags().GetBool("no-switch")
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

		if plain {
			return createPlainWorkspace(cmd, args, cfg, mgr, st, noSwitch, dirOnly)
		}
		return createWorktreeWorkspace(cmd, args, cfg, mgr, st, noSwitch, dirOnly)
	},
}

func createPlainWorkspace(cmd *cobra.Command, args []string, _ *config.Config, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	var name string
	if len(args) >= 1 {
		name = args[0]
	} else {
		prompted, err := promptNameFzf("name > ", "Type a name or enter for random", nil)
		if err != nil {
			return err
		}
		if prompted != "" {
			name = prompted
		} else {
			existing := existingPlainNames(st)
			name = names.Generate(existing)
		}
	}

	sessionName := fmt.Sprintf("g/%s", name)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", name)
	}

	dir, _ := cmd.Flags().GetString("path")
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}

	if dirOnly {
		fmt.Println(dir)
		return nil
	}

	if err := tmux.NewSession(sessionName, dir); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

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

	fmt.Printf("Created plain workspace %q\n", name)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func createWorktreeWorkspace(_ *cobra.Command, args []string, cfg *config.Config, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	var repoName, branch string

	switch len(args) {
	case 0:
		picked, err := pickRepoFzf(cfg)
		if err != nil {
			return err
		}
		repoName = picked
	case 1:
		repoName = args[0]
	default:
		repoName = args[0]
		branch = args[1]
	}

	repo := cfg.FindRepo(repoName)
	if repo == nil {
		return fmt.Errorf("repo %q not found in config", repoName)
	}

	if branch == "" {
		existing := existingWorktreeNames(st, repoName)

		branches, _ := git.ListBranches(repo.Path)
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

		prompted, err := promptNameFzf("branch > ", "Select branch, type new name, or enter for random", available)
		if err != nil {
			return err
		}
		if prompted != "" {
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

	if err := git.EnsureGitignore(repo.Path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}

	if err := git.AddWorktree(repo.Path, worktreePath, branch); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	for _, setupCmd := range repo.Setup {
		fmt.Printf("Running: %s\n", setupCmd)
		c := exec.Command("sh", "-c", setupCmd)
		c.Dir = worktreePath
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup command failed: %v\n", err)
		}
	}

	if dirOnly {
		fmt.Println(worktreePath)
		return nil
	}

	if err := tmux.NewSession(sessionName, worktreePath); err != nil {
		return fmt.Errorf("creating session: %w", err)
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

	fmt.Printf("Created worktree workspace %s/%s\n", repo.Name, branch)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func pickRepoFzf(cfg *config.Config) (string, error) {
	if len(cfg.Repos) == 0 {
		return "", fmt.Errorf("no repos configured")
	}
	if len(cfg.Repos) == 1 {
		return cfg.Repos[0].Name, nil
	}

	var repoNames []string
	for _, r := range cfg.Repos {
		repoNames = append(repoNames, r.Name)
	}

	fzfCmd := exec.Command("fzf", "--prompt", "repo > ", "--height", "~40%", "--reverse")
	fzfCmd.Stdin = strings.NewReader(strings.Join(repoNames, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", ErrCancelled
		}
		return "", fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	return strings.TrimSpace(string(out)), nil
}

const autoGenerateLabel = "(auto-generate)"

func promptNameFzf(prompt, header string, options []string) (string, error) {
	lines := append([]string{autoGenerateLabel}, options...)

	fzfCmd := exec.Command("fzf",
		"--prompt", prompt,
		"--header", header,
		"--print-query",
		"--height", "~40%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
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

func existingPlainNames(st *state.State) []string {
	var result []string
	for _, ws := range st.Workspaces {
		if ws.Type == "plain" {
			result = append(result, ws.Name)
		}
	}
	return result
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
